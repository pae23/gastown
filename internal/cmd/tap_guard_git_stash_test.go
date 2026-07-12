package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseGitStashCommand(t *testing.T) {
	tests := []struct {
		name    string
		command string
		wantOK  bool
		wantSub string
	}{
		{"bare stash means push", "git stash", true, "push"},
		{"explicit pop", "git stash pop", true, "pop"},
		{"flags before subcommand", "git stash -q pop", true, "pop"},
		{"global flag", "git -C /repo stash pop", true, "pop"},
		{"chained command", "cd /repo && git stash pop", true, "pop"},
		{"push with message", "git stash push -m label", true, "push"},
		{"not a git subcommand", "ls stash", false, ""},
		{"unrelated git command", "git status", false, ""},
		{"file named stash", "cat stash", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub, _, ok := parseGitStashCommand(tt.command)
			if ok != tt.wantOK {
				t.Fatalf("parseGitStashCommand(%q) ok = %v, want %v", tt.command, ok, tt.wantOK)
			}
			if ok && sub != tt.wantSub {
				t.Errorf("parseGitStashCommand(%q) sub = %q, want %q", tt.command, sub, tt.wantSub)
			}
		})
	}
}

func TestStashViolation(t *testing.T) {
	tests := []struct {
		name      string
		command   string
		wantBlock bool
	}{
		// Index-based access to the shared stack: always blocked.
		{"pop", "git stash pop", true},
		{"pop with positional ref", "git stash pop stash@{1}", true},
		{"drop", "git stash drop", true},
		{"clear", "git stash clear", true},
		{"bare apply", "git stash apply", true},
		{"apply by position", "git stash apply stash@{0}", true},
		{"branch from top of stack", "git stash branch fix-branch", true},
		{"branch by position", "git stash branch fix-branch stash@{0}", true},

		// Anonymous entries can't be traced back to their owner.
		{"bare stash", "git stash", true},
		{"push without message", "git stash push", true},
		{"push untracked without message", "git stash push -u", true},
		{"save without message", "git stash save", true},

		// SHA-addressed access names one commit — immune to the stack shifting.
		{"apply by sha", "git stash apply 4f2a9c1", false},
		{"apply by full sha", "git stash apply 0e9dc1f4a2b7c3e5d6f8091a2b3c4d5e6f708192", false},
		{"branch by sha", "git stash branch fix-branch 4f2a9c1", false},

		// Labelled entries are traceable.
		{"push with message", "git stash push -m polecat/valkyrie gt-78dk", false},
		{"push with long message flag", "git stash push --message=polecat/valkyrie", false},
		{"push with bundled short flags", "git stash push -um label", false},
		{"save with message", "git stash save gt-78dk-wip", false},

		// Read-only and index-free operations.
		{"list", "git stash list", false},
		{"show", "git stash show", false},
		{"create", "git stash create", false},
		{"store", "git stash store 4f2a9c1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub, args, ok := parseGitStashCommand(tt.command)
			if !ok {
				t.Fatalf("parseGitStashCommand(%q) did not recognize a stash command", tt.command)
			}
			reason, fix := stashViolation(sub, args)
			if got := reason != ""; got != tt.wantBlock {
				t.Fatalf("stashViolation(%q) blocked = %v (reason %q), want %v", tt.command, got, reason, tt.wantBlock)
			}
			if tt.wantBlock && fix == "" {
				t.Errorf("stashViolation(%q) blocked without suggesting a fix", tt.command)
			}
		})
	}
}

func TestStashLabelHintUsesRole(t *testing.T) {
	t.Setenv("GT_ROLE", "gastown/polecats/valkyrie")
	if hint := stashLabelHint(); !strings.Contains(hint, "gastown/polecats/valkyrie") {
		t.Errorf("stashLabelHint() = %q, want it to carry the agent's role", hint)
	}
}

func TestSharesStashStack(t *testing.T) {
	repo := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	run(repo, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(repo, "add", "f.txt")
	run(repo, "commit", "-m", "init")

	if sharesStashStack(repo) {
		t.Error("sharesStashStack() = true for a single-worktree repo, want false")
	}

	run(repo, "worktree", "add", filepath.Join(t.TempDir(), "wt"), "-b", "side")
	if !sharesStashStack(repo) {
		t.Error("sharesStashStack() = false once a second worktree exists, want true")
	}

	// Outside a repo, git errors — the guard must fail open.
	if sharesStashStack(t.TempDir()) {
		t.Error("sharesStashStack() = true outside a git repo, want false (fail open)")
	}
}

func TestExtractHookCwd(t *testing.T) {
	cwd := extractHookCwd([]byte(`{"cwd":"/repo/polecats/valkyrie","tool_input":{"command":"git stash pop"}}`))
	if cwd != "/repo/polecats/valkyrie" {
		t.Errorf("extractHookCwd() = %q, want /repo/polecats/valkyrie", cwd)
	}
	if got := extractHookCwd([]byte("not json")); got != "" {
		t.Errorf("extractHookCwd(invalid) = %q, want empty", got)
	}
}
