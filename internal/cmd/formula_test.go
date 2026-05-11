package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestResolveFormulaLegAgent_Precedence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		legAgent     string
		cliAgent     string
		formulaAgent string
		want         string
	}{
		{"all empty", "", "", "", ""},
		{"formula only", "", "", "gemini", "gemini"},
		{"cli only", "", "codex", "", "codex"},
		{"leg only", "claude-haiku", "", "", "claude-haiku"},
		{"cli overrides formula", "", "codex", "gemini", "codex"},
		{"leg overrides cli", "claude-haiku", "codex", "gemini", "claude-haiku"},
		{"leg overrides formula", "claude-haiku", "", "gemini", "claude-haiku"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := resolveFormulaLegAgent(tt.legAgent, tt.cliAgent, tt.formulaAgent)
			if got != tt.want {
				t.Errorf("resolveFormulaLegAgent(%q, %q, %q) = %q, want %q",
					tt.legAgent, tt.cliAgent, tt.formulaAgent, got, tt.want)
			}
		})
	}
}

// TestExecuteWorkflowFormula_PropagatesSetVars exercises the gt-lg0 fix:
// `gt formula run <workflow> --set k=v` must (a) record vars on the workflow
// root bead's FormulaVars field and (b) forward them as `--var k=v` flags so
// the dispatcher persists them on each slung step bead.
func TestExecuteWorkflowFormula_PropagatesSetVars(t *testing.T) {
	t.Run("argv builder forwards --set as --var for each step (b)", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			set  []string
			want []string
		}{
			{"nil set", nil, nil},
			{"empty set", []string{}, nil},
			{"single var", []string{"feature=foo"}, []string{"--var", "feature=foo"}},
			{
				"multiple vars preserve order",
				[]string{"feature=foo", "brief=hello world", "base_branch=trunk"},
				[]string{
					"--var", "feature=foo",
					"--var", "brief=hello world",
					"--var", "base_branch=trunk",
				},
			},
			{
				"value containing '=' is kept whole",
				[]string{"selector=key=value"},
				[]string{"--var", "selector=key=value"},
			},
			{"skips empty entries", []string{"feature=foo", "", "brief=bar"},
				[]string{"--var", "feature=foo", "--var", "brief=bar"}},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got := buildWorkflowSetVarArgs(tt.set)
				if !reflect.DeepEqual(got, tt.want) {
					t.Errorf("buildWorkflowSetVarArgs(%v) = %v, want %v",
						tt.set, got, tt.want)
				}
			})
		}
	})

	t.Run("workflow root bead records formula_vars (a)", func(t *testing.T) {
		// storeFieldsInBead skips the bd round-trip when
		// GT_TEST_ATTACHED_MOLECULE_LOG is set, writing the formatted
		// description to that file. We use that hook to assert the
		// FormulaVars line is emitted for the workflow root bead.
		tmp := filepath.Join(t.TempDir(), "wf-bead.log")
		t.Setenv("GT_TEST_ATTACHED_MOLECULE_LOG", tmp)

		formulaSet := []string{"feature=spec-workflow", "brief=probe"}
		if err := storeFieldsInBead("hq-wf-test", beadFieldUpdates{
			FormulaVars: strings.Join(formulaSet, "\n"),
		}); err != nil {
			t.Fatalf("storeFieldsInBead returned %v", err)
		}

		got, err := os.ReadFile(tmp)
		if err != nil {
			t.Fatalf("reading bead log: %v", err)
		}
		if !strings.Contains(string(got), "formula_vars: feature=spec-workflow") {
			t.Errorf("expected formula_vars line in bead description, got:\n%s", got)
		}
	})
}
