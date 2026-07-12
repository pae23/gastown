package plugin

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DefaultRef is the git ref plugins are deployed from. Runtime plugins are a
// deployment of merged code, so the source of truth is the tracking ref for
// main -- never a working tree, which can sit arbitrarily far behind main.
const DefaultRef = "origin/main"

// pluginsPrefix is the path of the plugin tree inside the gastown repo.
const pluginsPrefix = "plugins"

// ErrNoGastownRepo is returned when no canonical gastown repository can be found.
var ErrNoGastownRepo = errors.New("could not locate gastown git repository")

// GitSource is a plugin tree exported from a git ref into a temp directory.
// Callers must Close it to remove the temp directory.
type GitSource struct {
	RepoDir string // repo the tree came from (bare repo or worktree)
	Ref     string // ref that was exported, e.g. "origin/main"
	Commit  string // commit the ref resolved to
	Dir     string // exported plugins directory, suitable as a sync source

	tmpRoot string
}

// Close removes the exported tree.
func (s *GitSource) Close() error {
	if s == nil || s.tmpRoot == "" {
		return nil
	}
	return os.RemoveAll(s.tmpRoot)
}

// Describe renders the source as "origin/main (f8e6d072)" for display.
func (s *GitSource) Describe() string {
	if s == nil {
		return ""
	}
	commit := s.Commit
	if len(commit) > 8 {
		commit = commit[:8]
	}
	return fmt.Sprintf("%s (%s)", s.Ref, commit)
}

// FindGastownRepo locates the canonical gastown git repository for a town.
//
// The shared bare repo is preferred: it is CWD-independent and is the object
// store every worktree fetches into. Deploying from it is what keeps runtime
// plugins tied to merged code rather than to whichever checkout the operator
// happened to be standing in.
//
// Search order:
//  1. <townRoot>/gastown/.repo.git   (shared bare repo)
//  2. <townRoot>/gastown/mayor/rig   (mayor worktree, same object store)
//  3. walk up from CWD for a gastown module repo
func FindGastownRepo(townRoot string) (string, error) {
	candidates := []string{
		filepath.Join(townRoot, "gastown", ".repo.git"),
		filepath.Join(townRoot, "gastown", "mayor", "rig"),
	}
	for _, candidate := range candidates {
		if isGitRepo(candidate) {
			return candidate, nil
		}
	}

	if cwd, err := os.Getwd(); err == nil {
		if repo := findRepoFromDir(cwd); repo != "" {
			return repo, nil
		}
	}

	return "", ErrNoGastownRepo
}

// findRepoFromDir walks up from dir looking for a gastown module checkout.
func findRepoFromDir(dir string) string {
	current := dir
	for {
		if isGastownModule(filepath.Join(current, "go.mod")) && isGitRepo(current) {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

// isGitRepo reports whether dir is a git repository (worktree or bare).
func isGitRepo(dir string) bool {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return false
	}
	return git(dir, "rev-parse", "--git-dir") == nil
}

// git runs a git command in repoDir, discarding output.
func git(repoDir string, args ...string) error {
	_, err := gitOutput(repoDir, args...)
	return err
}

// gitOutput runs a git command in repoDir and returns trimmed stdout.
func gitOutput(repoDir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repoDir}, args...)...) //nolint:gosec // G204: args are internal, repoDir is a resolved town path
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// FetchRef updates a remote-tracking ref such as "origin/main" from its remote.
// Refs that are not remote-tracking (a local branch, a tag, a raw SHA) need no
// fetch and are a no-op.
func FetchRef(repoDir, ref string) error {
	remote, branch, ok := splitRemoteRef(repoDir, ref)
	if !ok {
		return nil
	}
	return git(repoDir, "fetch", "--quiet", remote, branch)
}

// splitRemoteRef splits "origin/main" into ("origin", "main") when origin is a
// configured remote of repoDir.
func splitRemoteRef(repoDir, ref string) (remote, branch string, ok bool) {
	name, rest, found := strings.Cut(ref, "/")
	if !found || name == "" || rest == "" {
		return "", "", false
	}
	remotes, err := gitOutput(repoDir, "remote")
	if err != nil {
		return "", "", false
	}
	for _, r := range strings.Fields(remotes) {
		if r == name {
			return name, rest, true
		}
	}
	return "", "", false
}

// ExportPluginsFromRef extracts the plugins/ tree of ref from repoDir into a
// temp directory and returns it as a sync source.
//
// If fetch is true the ref is refreshed from its remote first, so a deploy
// reflects what is on the remote rather than the last time anyone happened to
// fetch this repo.
func ExportPluginsFromRef(repoDir, ref string, fetch bool) (*GitSource, error) {
	if ref == "" {
		ref = DefaultRef
	}

	if fetch {
		if err := FetchRef(repoDir, ref); err != nil {
			// A fetch failure (offline, auth) must not silently fall back to a
			// stale ref: the caller is asking to deploy what is on the remote.
			return nil, fmt.Errorf("fetching %s: %w", ref, err)
		}
	}

	commit, err := gitOutput(repoDir, "rev-parse", ref+"^{commit}")
	if err != nil {
		return nil, fmt.Errorf("resolving %s in %s: %w", ref, repoDir, err)
	}

	archive, err := gitArchive(repoDir, commit)
	if err != nil {
		return nil, err
	}

	tmpRoot, err := os.MkdirTemp("", "gt-plugin-src-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}

	src := &GitSource{
		RepoDir: repoDir,
		Ref:     ref,
		Commit:  commit,
		Dir:     filepath.Join(tmpRoot, pluginsPrefix),
		tmpRoot: tmpRoot,
	}

	if err := extractTar(archive, tmpRoot); err != nil {
		_ = src.Close()
		return nil, err
	}

	if !hasPlugins(src.Dir) {
		_ = src.Close()
		return nil, fmt.Errorf("ref %s has no plugins in %s/", ref, pluginsPrefix)
	}

	return src, nil
}

// gitArchive returns a tar of the plugins/ tree at commit.
func gitArchive(repoDir, commit string) ([]byte, error) {
	cmd := exec.Command("git", "-C", repoDir, "archive", "--format=tar", commit, pluginsPrefix) //nolint:gosec // G204: commit is a resolved SHA, repoDir a resolved town path
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("exporting %s from %s: %s", pluginsPrefix, commit[:min(8, len(commit))], msg)
	}
	return stdout.Bytes(), nil
}

// extractTar unpacks a git archive tar into dest.
func extractTar(data []byte, dest string) error {
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading plugin archive: %w", err)
		}

		target, err := safeJoin(dest, hdr.Name)
		if err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return fmt.Errorf("creating %s: %w", hdr.Name, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return fmt.Errorf("creating %s: %w", filepath.Dir(hdr.Name), err)
			}
			if err := writeFile(target, tr, hdr.FileInfo().Mode()); err != nil {
				return fmt.Errorf("writing %s: %w", hdr.Name, err)
			}
		default:
			// Symlinks and other exotic entries have no place in a plugin tree.
			continue
		}
	}
}

// writeFile streams a tar entry to disk with a size cap, so a malformed or
// hostile archive cannot exhaust the disk.
func writeFile(path string, r io.Reader, mode os.FileMode) error {
	const maxFileSize = 64 << 20 // 64 MiB; plugins are markdown and scripts

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm()) //nolint:gosec // G304: path is validated by safeJoin
	if err != nil {
		return err
	}
	defer f.Close()

	written, err := io.Copy(f, io.LimitReader(r, maxFileSize+1))
	if err != nil {
		return err
	}
	if written > maxFileSize {
		return fmt.Errorf("file exceeds %d bytes", maxFileSize)
	}
	return nil
}

// safeJoin joins name onto root, rejecting entries that escape root.
func safeJoin(root, name string) (string, error) {
	clean := filepath.Clean(name)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry escapes destination: %s", name)
	}

	target := filepath.Join(root, clean)
	if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry escapes destination: %s", name)
	}
	return target, nil
}
