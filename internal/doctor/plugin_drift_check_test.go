package doctor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitInTest runs a git command in dir, failing the test on error.
func gitInTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writePlugin(t *testing.T, pluginsDir, name, content string) {
	t.Helper()
	dir := filepath.Join(pluginsDir, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// newTownWithGastownRepo builds a town whose gastown repo tracks an origin whose
// main holds `content`, and whose runtime plugins directory holds
// `runtimeContent` (empty = absent).
func newTownWithGastownRepo(t *testing.T, content, runtimeContent string) string {
	t.Helper()
	townRoot := t.TempDir()

	// The origin, with the plugin merged to main.
	origin := t.TempDir()
	gitInTest(t, origin, "init", "--quiet", "--initial-branch=main")
	writePlugin(t, filepath.Join(origin, "plugins"), "dolt-backup", content)
	gitInTest(t, origin, "add", "-A")
	gitInTest(t, origin, "commit", "--quiet", "-m", "add plugin")

	// The town's gastown checkout, tracking that origin.
	gastown := filepath.Join(townRoot, "gastown")
	if err := os.MkdirAll(gastown, 0755); err != nil {
		t.Fatal(err)
	}
	gitInTest(t, gastown, "clone", "--quiet", origin, filepath.Join(gastown, "mayor", "rig"))

	if runtimeContent != "" {
		writePlugin(t, filepath.Join(townRoot, "plugins"), "dolt-backup", runtimeContent)
	}
	return townRoot
}

func TestPatrolPluginDriftCheck_DetectsStaleRuntime(t *testing.T) {
	townRoot := newTownWithGastownRepo(t, "june-fixed", "april-buggy")

	check := NewPatrolPluginDriftCheck()
	result := check.Run(&CheckContext{TownRoot: townRoot})

	if result.Status != StatusWarning {
		t.Fatalf("status = %v, want StatusWarning for a stale runtime plugin (message: %s)", result.Status, result.Message)
	}

	// And the fix must deploy the merged content, not the working tree.
	if err := check.Fix(&CheckContext{TownRoot: townRoot}); err != nil {
		t.Fatalf("Fix: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(townRoot, "plugins", "dolt-backup", "plugin.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "june-fixed" {
		t.Errorf("runtime plugin after Fix = %q, want %q", got, "june-fixed")
	}
}

func TestPatrolPluginDriftCheck_OKWhenRuntimeMatchesRef(t *testing.T) {
	townRoot := newTownWithGastownRepo(t, "june-fixed", "june-fixed")

	result := NewPatrolPluginDriftCheck().Run(&CheckContext{TownRoot: townRoot})

	if result.Status != StatusOK {
		t.Errorf("status = %v, want StatusOK when the runtime matches the ref (message: %s, details: %v)",
			result.Status, result.Message, result.Details)
	}
}

// The check used to report StatusOK ("Plugin source not found") whenever it
// could not resolve a source — which is exactly how three months of plugin
// staleness went unnoticed while patrol reported green.
func TestPatrolPluginDriftCheck_WarnsWhenSourceUnresolvable(t *testing.T) {
	townRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(townRoot, "plugins"), 0755); err != nil {
		t.Fatal(err)
	}

	// No gastown repo anywhere above us, so the lookup genuinely fails.
	t.Chdir(t.TempDir())

	result := NewPatrolPluginDriftCheck().Run(&CheckContext{TownRoot: townRoot})

	if result.Status != StatusWarning {
		t.Errorf("status = %v, want StatusWarning when plugin staleness cannot be verified (message: %s)",
			result.Status, result.Message)
	}
}

// Deleting a plugin from main is how a dangerous one gets disabled (session-hygiene
// was deleted for killing crew sessions three times). If the runtime still carries
// it, the check must say so rather than report a clean runtime.
func TestPatrolPluginDriftCheck_ReportsOrphanedPlugin(t *testing.T) {
	townRoot := newTownWithGastownRepo(t, "june-fixed", "june-fixed")
	writePlugin(t, filepath.Join(townRoot, "plugins"), "session-hygiene", "deleted from main, still deployed")

	result := NewPatrolPluginDriftCheck().Run(&CheckContext{TownRoot: townRoot})

	if result.Status != StatusWarning {
		t.Fatalf("status = %v, want StatusWarning for a plugin deleted from the ref but still deployed (message: %s)",
			result.Status, result.Message)
	}

	var mentioned bool
	for _, d := range result.Details {
		if strings.Contains(d, "session-hygiene") {
			mentioned = true
		}
	}
	if !mentioned {
		t.Errorf("details %v do not mention the orphaned plugin", result.Details)
	}
}

// A plugin merged to main but never deployed is drift, not a clean runtime.
func TestPatrolPluginDriftCheck_DetectsUndeployedPlugin(t *testing.T) {
	townRoot := newTownWithGastownRepo(t, "june-fixed", "")

	result := NewPatrolPluginDriftCheck().Run(&CheckContext{TownRoot: townRoot})

	if result.Status != StatusWarning {
		t.Errorf("status = %v, want StatusWarning for a plugin that was never deployed (message: %s)",
			result.Status, result.Message)
	}
}
