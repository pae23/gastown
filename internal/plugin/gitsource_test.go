package plugin

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runGit runs a git command in dir, failing the test on error.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// newRepoWithPlugin creates a git repo whose main branch holds a single plugin
// with the given content, and returns the repo path.
func newRepoWithPlugin(t *testing.T, name, content string) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init", "--quiet", "--initial-branch=main")
	createTestPlugin(t, filepath.Join(repo, "plugins"), name, content, nil)
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "--quiet", "-m", "add plugin")
	return repo
}

// commitPlugin writes a plugin and commits it on the current branch.
func commitPlugin(t *testing.T, repo, name, content, msg string) {
	t.Helper()
	createTestPlugin(t, filepath.Join(repo, "plugins"), name, content, nil)
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "--quiet", "-m", msg)
}

func readPlugin(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name, "plugin.md"))
	if err != nil {
		t.Fatalf("reading exported plugin: %v", err)
	}
	return string(data)
}

func TestExportPluginsFromRef_ExportsCommittedTree(t *testing.T) {
	repo := newRepoWithPlugin(t, "dolt-backup", "v1")

	src, err := ExportPluginsFromRef(repo, "main", false)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	if got := readPlugin(t, src.Dir, "dolt-backup"); got != "v1" {
		t.Errorf("exported content = %q, want %q", got, "v1")
	}
	if src.Commit == "" {
		t.Error("expected the export to record a commit")
	}
}

// The regression this whole change exists for: a working tree that lags the ref
// must not be what gets deployed. Deploying from the ref must yield the merged
// content even when the checkout in front of us is stale.
func TestExportPluginsFromRef_IgnoresStaleWorkingTree(t *testing.T) {
	repo := newRepoWithPlugin(t, "dolt-backup", "april-buggy")
	commitPlugin(t, repo, "dolt-backup", "june-fixed", "fix data loss")

	// Roll the working tree back to the buggy version, leaving the ref ahead.
	runGit(t, repo, "checkout", "--quiet", "HEAD~1")

	if got, _ := os.ReadFile(filepath.Join(repo, "plugins", "dolt-backup", "plugin.md")); string(got) != "april-buggy" {
		t.Fatalf("test setup: working tree = %q, want the stale version", got)
	}

	src, err := ExportPluginsFromRef(repo, "main", false)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	if got := readPlugin(t, src.Dir, "dolt-backup"); got != "june-fixed" {
		t.Errorf("exported content = %q, want the merged version %q (stale worktree was deployed)", got, "june-fixed")
	}
}

func TestExportPluginsFromRef_BareRepo(t *testing.T) {
	repo := newRepoWithPlugin(t, "dolt-backup", "v1")

	bare := filepath.Join(t.TempDir(), "repo.git")
	runGit(t, t.TempDir(), "clone", "--quiet", "--bare", repo, bare)

	src, err := ExportPluginsFromRef(bare, "main", false)
	if err != nil {
		t.Fatalf("exporting from a bare repo: %v", err)
	}
	defer src.Close()

	if got := readPlugin(t, src.Dir, "dolt-backup"); got != "v1" {
		t.Errorf("exported content = %q, want %q", got, "v1")
	}
}

// A deploy that asks for the remote's state must fail loudly when it cannot
// reach the remote, rather than quietly deploying a stale local ref.
func TestExportPluginsFromRef_FetchFailureIsNotSilent(t *testing.T) {
	repo := newRepoWithPlugin(t, "dolt-backup", "v1")
	runGit(t, repo, "remote", "add", "origin", filepath.Join(t.TempDir(), "does-not-exist.git"))

	if _, err := ExportPluginsFromRef(repo, "origin/main", true); err == nil {
		t.Error("expected an error when the fetch fails, got nil")
	}
}

// newTriangularClone clones fetchRepo and points origin's push URL at pushRepo,
// reproducing the town's remote layout: fetch from upstream, push to the fork
// the refinery lands merges on.
func newTriangularClone(t *testing.T, fetchRepo, pushRepo string) string {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "clone")
	runGit(t, t.TempDir(), "clone", "--quiet", fetchRepo, clone)
	runGit(t, clone, "remote", "set-url", "--push", "origin", pushRepo)
	return clone
}

// The bug this fix exists for: origin fetches from upstream but pushes to the
// fork, so refs/remotes/origin/main tracks upstream and lags every merge the
// refinery lands. Deploying from it ships stale plugins while reporting success.
func TestExportPluginsFromRef_DeploysFromPushRemote(t *testing.T) {
	upstream := newRepoWithPlugin(t, "dolt-backup", "stale-upstream")
	fork := newRepoWithPlugin(t, "dolt-backup", "merged-fix")

	repo := newTriangularClone(t, upstream, fork)

	src, err := ExportPluginsFromRef(repo, "origin/main", true)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	if got := readPlugin(t, src.Dir, "dolt-backup"); got != "merged-fix" {
		t.Errorf("exported content = %q, want the push remote's %q (deployed the stale fetch remote)", got, "merged-fix")
	}
	if src.PushURL != fork {
		t.Errorf("PushURL = %q, want %q", src.PushURL, fork)
	}
	if !strings.Contains(src.Describe(), fork) {
		t.Errorf("Describe() = %q, want it to name the push remote it deployed from", src.Describe())
	}
}

// A fetch must not leave the deploy reading a mirror from a previous run.
func TestExportPluginsFromRef_PushRemoteMirrorTracksNewMerges(t *testing.T) {
	upstream := newRepoWithPlugin(t, "dolt-backup", "stale-upstream")
	fork := newRepoWithPlugin(t, "dolt-backup", "v1")

	repo := newTriangularClone(t, upstream, fork)

	src, err := ExportPluginsFromRef(repo, "origin/main", true)
	if err != nil {
		t.Fatal(err)
	}
	src.Close()

	commitPlugin(t, fork, "dolt-backup", "v2", "land a fix")

	src, err = ExportPluginsFromRef(repo, "origin/main", true)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	if got := readPlugin(t, src.Dir, "dolt-backup"); got != "v2" {
		t.Errorf("exported content = %q, want the newly merged %q", got, "v2")
	}
}

// Without a fetch, the mirror from the last deploy is the best available source
// -- but it must be the mirror, never the fetch remote's stale tracking ref.
func TestExportPluginsFromRef_NoFetchUsesMirroredPushRef(t *testing.T) {
	upstream := newRepoWithPlugin(t, "dolt-backup", "stale-upstream")
	fork := newRepoWithPlugin(t, "dolt-backup", "merged-fix")

	repo := newTriangularClone(t, upstream, fork)

	// Prime the mirror.
	src, err := ExportPluginsFromRef(repo, "origin/main", true)
	if err != nil {
		t.Fatal(err)
	}
	src.Close()

	src, err = ExportPluginsFromRef(repo, "origin/main", false)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	if got := readPlugin(t, src.Dir, "dolt-backup"); got != "merged-fix" {
		t.Errorf("exported content = %q, want the mirrored %q", got, "merged-fix")
	}
}

// No mirror and no fetch means the push remote's state is unknown. Deploying the
// fetch remote's tracking ref instead would silently ship the stale tree, so the
// export must fail loudly.
func TestExportPluginsFromRef_NoFetchWithoutMirrorFails(t *testing.T) {
	upstream := newRepoWithPlugin(t, "dolt-backup", "stale-upstream")
	fork := newRepoWithPlugin(t, "dolt-backup", "merged-fix")

	repo := newTriangularClone(t, upstream, fork)

	if _, err := ExportPluginsFromRef(repo, "origin/main", false); err == nil {
		t.Error("expected an error when no push-remote mirror exists and fetching is disabled, got nil")
	}
}

// A plain remote (push URL == fetch URL) keeps deploying from its tracking ref.
func TestExportPluginsFromRef_SingleRemoteUsesTrackingRef(t *testing.T) {
	upstream := newRepoWithPlugin(t, "dolt-backup", "v1")

	clone := filepath.Join(t.TempDir(), "clone")
	runGit(t, t.TempDir(), "clone", "--quiet", upstream, clone)

	commitPlugin(t, upstream, "dolt-backup", "v2", "land a fix")

	src, err := ExportPluginsFromRef(clone, "origin/main", true)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	if got := readPlugin(t, src.Dir, "dolt-backup"); got != "v2" {
		t.Errorf("exported content = %q, want %q", got, "v2")
	}
	if src.PushURL != "" {
		t.Errorf("PushURL = %q, want empty for a remote with no distinct push URL", src.PushURL)
	}
}

func TestExportPluginsFromRef_UnknownRef(t *testing.T) {
	repo := newRepoWithPlugin(t, "dolt-backup", "v1")

	if _, err := ExportPluginsFromRef(repo, "no-such-ref", false); err == nil {
		t.Error("expected an error for an unknown ref, got nil")
	}
}

func TestExportPluginsFromRef_RefWithoutPlugins(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "--quiet", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("no plugins here"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "--quiet", "-m", "init")

	if _, err := ExportPluginsFromRef(repo, "main", false); err == nil {
		t.Error("expected an error for a ref with no plugins/ tree, got nil")
	}
}

func TestGitSource_CloseRemovesExport(t *testing.T) {
	repo := newRepoWithPlugin(t, "dolt-backup", "v1")

	src, err := ExportPluginsFromRef(repo, "main", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src.Dir); !os.IsNotExist(err) {
		t.Errorf("export dir still present after Close: %v", err)
	}
}

// The export is the sync source, so it must satisfy SyncPlugins end to end.
func TestExportPluginsFromRef_FeedsSyncPlugins(t *testing.T) {
	repo := newRepoWithPlugin(t, "dolt-backup", "june-fixed")
	runtimeDir := t.TempDir()
	createTestPlugin(t, runtimeDir, "dolt-backup", "april-buggy", nil)

	src, err := ExportPluginsFromRef(repo, "main", false)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	result, err := SyncPlugins(src.Dir, runtimeDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Copied) != 1 {
		t.Errorf("expected the stale plugin to be redeployed, got copied=%v skipped=%v", result.Copied, result.Skipped)
	}
	if got := readPlugin(t, runtimeDir, "dolt-backup"); got != "june-fixed" {
		t.Errorf("runtime content = %q, want %q", got, "june-fixed")
	}
}

func TestFindGastownRepo_PrefersBareRepo(t *testing.T) {
	townRoot := t.TempDir()
	upstream := newRepoWithPlugin(t, "dolt-backup", "v1")

	bare := filepath.Join(townRoot, "gastown", ".repo.git")
	if err := os.MkdirAll(filepath.Dir(bare), 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, t.TempDir(), "clone", "--quiet", "--bare", upstream, bare)

	// A mayor worktree also exists; the bare repo must still win.
	mayorRig := filepath.Join(townRoot, "gastown", "mayor", "rig")
	runGit(t, t.TempDir(), "clone", "--quiet", upstream, mayorRig)

	repo, err := FindGastownRepo(townRoot)
	if err != nil {
		t.Fatal(err)
	}
	if repo != bare {
		t.Errorf("FindGastownRepo() = %q, want the bare repo %q", repo, bare)
	}
}

func TestFindGastownRepo_FallsBackToMayorRig(t *testing.T) {
	townRoot := t.TempDir()
	upstream := newRepoWithPlugin(t, "dolt-backup", "v1")

	mayorRig := filepath.Join(townRoot, "gastown", "mayor", "rig")
	if err := os.MkdirAll(filepath.Dir(mayorRig), 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, t.TempDir(), "clone", "--quiet", upstream, mayorRig)

	repo, err := FindGastownRepo(townRoot)
	if err != nil {
		t.Fatal(err)
	}
	if repo != mayorRig {
		t.Errorf("FindGastownRepo() = %q, want %q", repo, mayorRig)
	}
}

// An empty <town>/gastown/plugins directory (which is what this town actually
// had) must not be mistaken for a deploy source.
func TestFindGastownRepo_EmptyPluginsDirIsNotARepo(t *testing.T) {
	townRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(townRoot, "gastown", "plugins"), 0755); err != nil {
		t.Fatal(err)
	}

	// Run from a directory with no gastown repo above it, so the CWD walk-up
	// cannot rescue the lookup.
	t.Chdir(t.TempDir())

	_, err := FindGastownRepo(townRoot)
	if !errors.Is(err, ErrNoGastownRepo) {
		t.Errorf("FindGastownRepo() error = %v, want ErrNoGastownRepo", err)
	}
}

func TestSafeJoin_RejectsEscape(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"../escape", "plugins/../../escape"} {
		if _, err := safeJoin(root, name); err == nil {
			t.Errorf("safeJoin(%q) = nil error, want rejection", name)
		}
	}
	if _, err := safeJoin(root, "plugins/dolt-backup/plugin.md"); err != nil {
		t.Errorf("safeJoin rejected a legitimate entry: %v", err)
	}
}
