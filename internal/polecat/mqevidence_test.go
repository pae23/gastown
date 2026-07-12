package polecat

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/steveyegge/gastown/internal/beads"
)

type fakeMRFinder struct {
	issue *beads.Issue
	err   error
}

func (f fakeMRFinder) FindMRForBranchAny(string) (*beads.Issue, error) {
	return f.issue, f.err
}

func mqGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

func mqCommit(t *testing.T, dir, name, contents, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	mqGit(t, dir, "add", ".")
	mqGit(t, dir, "commit", "-m", message)
}

// newPolecatWorktree builds the Gas Town remote topology (origin fetches from
// upstream, pushes to a fork) with a polecat branch holding one commit. If
// merged is true, the refinery lands that patch on the FORK's main under a new
// sha, exactly as a rebase-merge does.
func newPolecatWorktree(t *testing.T, merged bool) string {
	t.Helper()
	root := t.TempDir()

	upstream := filepath.Join(root, "upstream.git")
	fork := filepath.Join(root, "fork.git")
	mqGit(t, root, "init", "--bare", "--initial-branch=main", upstream)
	mqGit(t, root, "init", "--bare", "--initial-branch=main", fork)

	seed := filepath.Join(root, "seed")
	mqGit(t, root, "clone", upstream, seed)
	mqGit(t, seed, "config", "user.email", "test@test.com")
	mqGit(t, seed, "config", "user.name", "Test User")
	mqCommit(t, seed, "README.md", "# base\n", "base")
	mqGit(t, seed, "push", "origin", "main")
	mqGit(t, seed, "push", fork, "main:main")

	worktree := filepath.Join(root, "polecat")
	mqGit(t, root, "clone", upstream, worktree)
	mqGit(t, worktree, "config", "user.email", "test@test.com")
	mqGit(t, worktree, "config", "user.name", "Test User")
	mqGit(t, worktree, "remote", "set-url", "--push", "origin", fork)
	mqGit(t, worktree, "checkout", "-b", "polecat/test")
	mqCommit(t, worktree, "feature.txt", "feature\n", "feat: the work")
	mqGit(t, worktree, "push", "origin", "polecat/test")

	if merged {
		refinery := filepath.Join(root, "refinery")
		mqGit(t, root, "clone", fork, refinery)
		mqGit(t, refinery, "config", "user.email", "test@test.com")
		mqGit(t, refinery, "config", "user.name", "Test User")
		// Land an unrelated commit first so replaying the polecat's patch is
		// guaranteed to produce a different sha than the original.
		mqCommit(t, refinery, "other.txt", "other\n", "other work")
		patch := filepath.Join(root, "polecat.patch")
		out, err := exec.Command("git", "-C", worktree, "format-patch", "-1", "HEAD", "--stdout").Output()
		if err != nil {
			t.Fatalf("format-patch: %v", err)
		}
		if err := os.WriteFile(patch, out, 0644); err != nil {
			t.Fatalf("write patch: %v", err)
		}
		mqGit(t, refinery, "am", patch)
		mqGit(t, refinery, "push", "origin", "main")
	}

	return worktree
}

// TestApplyMQEvidenceMergedPolecatWithReapedWisp is the gt-91ju acceptance pair.
// Both polecats look identical to the merge-queue lookup — neither has an MR bead
// (the reaper collects the wisp once work lands, and unsubmitted work never had
// one). Only the state of the target branch tells them apart.
func TestApplyMQEvidenceMergedPolecatWithReapedWisp(t *testing.T) {
	tests := []struct {
		name            string
		merged          bool
		wantWorkMerged  bool
		wantVerdict     string
		wantMQStatus    string
		wantSafeToNuke  bool
		wantNeedsSubmit bool
	}{
		{
			// Merged and its MR wisp reaped: the verdict must come from the work,
			// not from the GC clock.
			name:           "merged work with reaped wisp is safe to nuke",
			merged:         true,
			wantWorkMerged: true,
			wantVerdict:    WorkstateVerdictSafeToNuke,
			wantMQStatus:   "merged",
			wantSafeToNuke: true,
		},
		{
			// Pushed but never enqueued: no merged patch anywhere, so the missing MR
			// bead still means what it used to mean.
			name:            "pushed but never submitted still needs mq submit",
			merged:          false,
			wantWorkMerged:  false,
			wantVerdict:     WorkstateVerdictNeedsMQSubmit,
			wantMQStatus:    "not_submitted",
			wantNeedsSubmit: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worktree := newPolecatWorktree(t, tt.merged)

			in := WorkstateInput{
				State:              StateIdle,
				CleanupStatus:      CleanupClean,
				Branch:             "polecat/test",
				MQCheckRequired:    true,
				HasSubmittableWork: true,
			}
			// The reaper already collected the MR wisp, so the lookup finds nothing.
			ApplyMQEvidence(&in, fakeMRFinder{issue: nil}, worktree, []string{"main"})

			if in.WorkMerged != tt.wantWorkMerged {
				t.Fatalf("WorkMerged = %v, want %v", in.WorkMerged, tt.wantWorkMerged)
			}
			d := DecideWorkstate(in)
			if d.Verdict != tt.wantVerdict {
				t.Errorf("Verdict = %q, want %q", d.Verdict, tt.wantVerdict)
			}
			if d.MQStatus != tt.wantMQStatus {
				t.Errorf("MQStatus = %q, want %q", d.MQStatus, tt.wantMQStatus)
			}
			if d.SafeToNuke != tt.wantSafeToNuke {
				t.Errorf("SafeToNuke = %v, want %v", d.SafeToNuke, tt.wantSafeToNuke)
			}
			if d.NeedsMQSubmit != tt.wantNeedsSubmit {
				t.Errorf("NeedsMQSubmit = %v, want %v", d.NeedsMQSubmit, tt.wantNeedsSubmit)
			}
		})
	}
}

// A merged polecat must not be re-blocked just because bd is unreachable: the
// merged patch is local evidence and never needs the merge queue to confirm it.
func TestApplyMQEvidenceMergedWorkSkipsMRLookupEntirely(t *testing.T) {
	worktree := newPolecatWorktree(t, true)

	in := WorkstateInput{
		State:              StateIdle,
		CleanupStatus:      CleanupClean,
		Branch:             "polecat/test",
		MQCheckRequired:    true,
		HasSubmittableWork: true,
	}
	ApplyMQEvidence(&in, fakeMRFinder{err: errors.New("bd exploded")}, worktree, []string{"main"})

	if !in.WorkMerged {
		t.Fatal("WorkMerged = false, want true")
	}
	if in.MQLookupFailed {
		t.Error("MQLookupFailed = true: merged work must not consult the merge queue at all")
	}
	if d := DecideWorkstate(in); d.Verdict != WorkstateVerdictSafeToNuke {
		t.Errorf("Verdict = %q, want %q", d.Verdict, WorkstateVerdictSafeToNuke)
	}
}

// A closed bead is not proof of submission (beads get closed by hand), but a
// merged patch is — and the merged check runs first, so a merged polecat whose
// bead the refinery already closed is still correctly cleared.
func TestApplyMQEvidenceMergedWorkWithTerminalBead(t *testing.T) {
	worktree := newPolecatWorktree(t, true)

	in := WorkstateInput{
		State:                StateIdle,
		CleanupStatus:        CleanupClean,
		Branch:               "polecat/test",
		MQCheckRequired:      true,
		HasSubmittableWork:   true,
		AssignedBeadTerminal: true,
	}
	ApplyMQEvidence(&in, fakeMRFinder{issue: nil}, worktree, []string{"main"})

	if !in.WorkMerged {
		t.Fatal("WorkMerged = false, want true")
	}
	if d := DecideWorkstate(in); d.Verdict != WorkstateVerdictSafeToNuke {
		t.Errorf("Verdict = %q, want %q", d.Verdict, WorkstateVerdictSafeToNuke)
	}
}

// The cheap gates must short-circuit before the network fetch: a nil finder here
// proves no lookup happened, and a bogus worktree proves no git call happened.
func TestApplyMQEvidenceSkipsCheapGates(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   WorkstateInput
	}{
		{"mq check not required", WorkstateInput{Branch: "polecat/test", HasSubmittableWork: true}},
		{"no submittable work", WorkstateInput{Branch: "polecat/test", MQCheckRequired: true}},
		{"mq not required for source", WorkstateInput{Branch: "polecat/test", MQCheckRequired: true, HasSubmittableWork: true, MQNotRequired: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			in := tt.in
			ApplyMQEvidence(&in, nil, "/nonexistent/worktree", []string{"main"})
			if in.WorkMerged || in.MRSubmitted || in.MQLookupFailed {
				t.Errorf("evidence gathered despite cheap gate: %+v", in)
			}
		})
	}
}
