package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitOutput runs a git command in dir and returns its stdout, failing on error.
// It complements the package's runGit helper, which discards output.
func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
	return string(out)
}

func initBareRepo(t *testing.T, parent, name string) string {
	t.Helper()
	path := filepath.Join(parent, name)
	runGit(t, parent, "init", "--bare", "--initial-branch=main", path)
	return path
}

func commitFile(t *testing.T, dir, name, contents, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", message)
}

func configureIdentity(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test User")
}

// setupTriangularRemote reproduces the Gas Town remote topology: origin FETCHES
// from upstream, but work is PUSHED to a fork, and the refinery merges into the
// fork's main. The upstream main therefore never contains merged work, and the
// origin/main remote-tracking ref (which follows the fetch url) is stale forever.
//
// Returns the polecat worktree path.
func setupTriangularRemote(t *testing.T) (worktree, forkPath string) {
	t.Helper()
	root := t.TempDir()

	upstream := initBareRepo(t, root, "upstream.git")
	fork := initBareRepo(t, root, "fork.git")

	// Seed upstream main.
	seed := filepath.Join(root, "seed")
	runGit(t, root, "clone", upstream, seed)
	configureIdentity(t, seed)
	commitFile(t, seed, "README.md", "# base\n", "base")
	runGit(t, seed, "push", "origin", "main")
	// The fork starts from the same base.
	runGit(t, seed, "push", fork, "main:main")

	// The polecat clone: fetch upstream, push fork.
	worktree = filepath.Join(root, "polecat")
	runGit(t, root, "clone", upstream, worktree)
	configureIdentity(t, worktree)
	runGit(t, worktree, "remote", "set-url", "--push", "origin", fork)

	return worktree, fork
}

// mergeIntoForkRebased simulates the refinery: it takes the polecat's patch and
// lands it on fork main with a DIFFERENT sha (a rebase), then pushes. Matching by
// sha would miss this; matching by patch-id must not.
func mergeIntoForkRebased(t *testing.T, root, fork, patchFrom, worktree string) {
	t.Helper()
	refinery := filepath.Join(root, "refinery")
	runGit(t, root, "clone", fork, refinery)
	configureIdentity(t, refinery)

	// An unrelated commit lands first, so the polecat's patch necessarily gets a
	// new sha when it is replayed on top.
	commitFile(t, refinery, "other.txt", "other work\n", "other work")

	patch := gitOutput(t, worktree, "format-patch", "-1", patchFrom, "--stdout")
	patchFile := filepath.Join(root, "polecat.patch")
	if err := os.WriteFile(patchFile, []byte(patch), 0644); err != nil {
		t.Fatalf("write patch: %v", err)
	}
	runGit(t, refinery, "am", patchFile)
	runGit(t, refinery, "push", "origin", "main")
}

func TestPushRemoteTargetStatusSeesForkMergeByPatchID(t *testing.T) {
	worktree, fork := setupTriangularRemote(t)
	root := filepath.Dir(worktree)

	// Polecat does work on its branch.
	runGit(t, worktree, "checkout", "-b", "polecat/test")
	commitFile(t, worktree, "feature.txt", "feature\n", "feat: the work")
	runGit(t, worktree, "push", "origin", "polecat/test")

	// Refinery rebases it onto fork main and merges.
	mergeIntoForkRebased(t, root, fork, "HEAD", worktree)

	g := NewGit(worktree)

	// The bug: origin/main follows the FETCH url (upstream), which never receives
	// the merge. The old target check therefore still sees an unmerged patch, and
	// the caller concludes the work was never submitted.
	stale, err := g.BranchTargetStatus("polecat/test", "origin", []string{"main"})
	if err != nil {
		t.Fatalf("BranchTargetStatus: %v", err)
	}
	if stale.Preserved || stale.UnpreservedPatchCount == 0 {
		t.Fatalf("precondition failed: fetch-side comparison should still look unmerged, got %+v", stale)
	}

	// The fix: resolve the target from the PUSH url and match by patch-id.
	status, err := g.PushRemoteTargetStatus("origin", "main")
	if err != nil {
		t.Fatalf("PushRemoteTargetStatus: %v", err)
	}
	if !status.Preserved {
		t.Errorf("merged (rebased) work not seen as preserved: %+v", status)
	}
	if status.UnpreservedPatchCount != 0 {
		t.Errorf("UnpreservedPatchCount = %d, want 0 (%+v)", status.UnpreservedPatchCount, status)
	}
}

func TestPushRemoteTargetStatusUnmergedWorkStaysUnpreserved(t *testing.T) {
	worktree, _ := setupTriangularRemote(t)

	// Polecat pushed its branch but nothing ever merged it: the crashed-between-
	// push-and-submit case, which must still be caught.
	runGit(t, worktree, "checkout", "-b", "polecat/test")
	commitFile(t, worktree, "feature.txt", "feature\n", "feat: never submitted")
	runGit(t, worktree, "push", "origin", "polecat/test")

	status, err := NewGit(worktree).PushRemoteTargetStatus("origin", "main")
	if err != nil {
		t.Fatalf("PushRemoteTargetStatus: %v", err)
	}
	if status.Preserved {
		t.Errorf("unmerged work reported as preserved: %+v", status)
	}
	if status.UnpreservedPatchCount != 1 {
		t.Errorf("UnpreservedPatchCount = %d, want 1 (%+v)", status.UnpreservedPatchCount, status)
	}
}

func TestPushRemoteTargetStatusMissingTarget(t *testing.T) {
	worktree, _ := setupTriangularRemote(t)
	if _, err := NewGit(worktree).PushRemoteTargetStatus("origin", ""); err == nil {
		t.Error("empty target should error, not silently report preserved")
	}
	if _, err := NewGit(worktree).PushRemoteTargetStatus("origin", "no-such-branch"); err == nil {
		t.Error("unknown target should error, not silently report preserved")
	}
}
