package health

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeStore lays down a dolt chunk store: a manifest plus n archive chunk files
// of the given size.
func writeStore(t *testing.T, dir string, chunks int, chunkSize int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest"), []byte("4:1:manifest\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < chunks; i++ {
		name := strings.Repeat(string(rune('a'+i)), 32) + ".darc"
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, chunkSize), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// town builds a town root with a live database and returns its path.
func town(t *testing.T, db string, liveChunkSize int) string {
	t.Helper()
	root := t.TempDir()
	writeStore(t, filepath.Join(root, ".dolt-data", db, ".dolt", "noms"), 1, liveChunkSize)
	return root
}

func backupPath(root, db string) string {
	return filepath.Join(root, ".dolt-backup", db)
}

func statusFor(t *testing.T, root, db string) BackupStatus {
	t.Helper()
	all := InspectBackups(root, BackupStaleAfter)
	for _, st := range all {
		if st.Name == db {
			return st
		}
	}
	t.Fatalf("no backup status for %q (got %d)", db, len(all))
	return BackupStatus{}
}

func hasProblem(st BackupStatus, substr string) bool {
	for _, p := range st.Problems {
		if strings.Contains(p, substr) {
			return true
		}
	}
	return false
}

// The regression: an empty backup directory whose mtime the patrol keeps
// touching. The old check saw a fresh mtime and reported GREEN.
func TestInspectBackups_EmptyDirWithFreshMtimeIsNotHealthy(t *testing.T) {
	root := town(t, "beads", 4096)
	if err := os.MkdirAll(backupPath(root, "beads"), 0o755); err != nil {
		t.Fatal(err)
	}

	st := statusFor(t, root, "beads")
	if st.Healthy() {
		t.Fatalf("empty backup dir reported healthy: %+v", st)
	}
	if !hasProblem(st, "no dolt store") {
		t.Errorf("expected empty-store problem, got %v", st.Problems)
	}
	if st.Age > time.Minute {
		t.Errorf("directory mtime is fresh, so Age should be small; got %v", st.Age)
	}
}

func TestInspectBackups_HealthyBackup(t *testing.T) {
	root := town(t, "beads", 4096)
	writeStore(t, filepath.Join(backupPath(root, "beads"), "beads-backup"), 2, 4096)

	st := statusFor(t, root, "beads")
	if !st.Healthy() {
		t.Fatalf("expected healthy backup, got problems: %v", st.Problems)
	}
	if st.ChunkFiles != 2 {
		t.Errorf("ChunkFiles = %d, want 2", st.ChunkFiles)
	}
	if !st.HasManifest {
		t.Error("HasManifest = false, want true")
	}
}

// Older towns synced straight into <db> instead of <db>/<db>-backup.
func TestInspectBackups_LegacyLayout(t *testing.T) {
	root := town(t, "dolt", 4096)
	writeStore(t, backupPath(root, "dolt"), 2, 4096)

	if st := statusFor(t, root, "dolt"); !st.Healthy() {
		t.Fatalf("legacy layout reported unhealthy: %v", st.Problems)
	}
}

func TestInspectBackups_ManifestWithoutChunks(t *testing.T) {
	root := town(t, "beads", 4096)
	writeStore(t, filepath.Join(backupPath(root, "beads"), "beads-backup"), 0, 0)

	st := statusFor(t, root, "beads")
	if !hasProblem(st, "no chunk files") {
		t.Errorf("expected no-chunk-files problem, got %v", st.Problems)
	}
}

func TestInspectBackups_TruncatedBackupFailsSizeFloor(t *testing.T) {
	root := town(t, "beads", 1_000_000)
	writeStore(t, filepath.Join(backupPath(root, "beads"), "beads-backup"), 1, 1_000) // 0.1% of live

	st := statusFor(t, root, "beads")
	if !hasProblem(st, "truncated") {
		t.Errorf("expected size-floor problem, got %v", st.Problems)
	}
}

// A backup of a database with no local live copy has no size to compare against.
func TestInspectBackups_NoLiveDatabaseSkipsSizeFloor(t *testing.T) {
	root := t.TempDir()
	writeStore(t, filepath.Join(backupPath(root, "archived"), "archived-backup"), 1, 512)

	st := statusFor(t, root, "archived")
	if !st.Healthy() {
		t.Fatalf("expected healthy, got %v", st.Problems)
	}
	if st.LiveSizeBytes != 0 {
		t.Errorf("LiveSizeBytes = %d, want 0", st.LiveSizeBytes)
	}
}

func TestInspectBackups_StaleBackup(t *testing.T) {
	root := town(t, "beads", 4096)
	store := filepath.Join(backupPath(root, "beads"), "beads-backup")
	writeStore(t, store, 1, 4096)

	old := time.Now().Add(-5 * time.Hour)
	for _, p := range []string{store, filepath.Join(store, "manifest"), backupPath(root, "beads")} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	st := statusFor(t, root, "beads")
	if !hasProblem(st, "has not synced") {
		t.Errorf("expected staleness problem, got %v", st.Problems)
	}
}

// dolt log --oneline colorizes its output even through a pipe, so markers on
// disk look like "\x1b[33m<hash>". The hash must come back clean.
func TestReadHashMarker_StripsANSI(t *testing.T) {
	root := town(t, "beads", 4096)
	writeStore(t, filepath.Join(backupPath(root, "beads"), "beads-backup"), 1, 4096)
	marker := filepath.Join(backupPath(root, "beads"), backupHashMarker)
	if err := os.WriteFile(marker, []byte("\x1b[33ms7s97bc5pa2tnluk86p8ae5t9msolmmo\x1b[0m\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := statusFor(t, root, "beads")
	if want := "s7s97bc5pa2tnluk86p8ae5t9msolmmo"; st.HeadHash != want {
		t.Errorf("HeadHash = %q, want %q", st.HeadHash, want)
	}
}

func TestInspectBackups_NoBackupDirReturnsNil(t *testing.T) {
	if got := InspectBackups(t.TempDir(), BackupStaleAfter); got != nil {
		t.Errorf("expected nil for missing .dolt-backup, got %v", got)
	}
}

func TestIsChunkFile(t *testing.T) {
	cases := map[string]bool{
		"ilj73lbsqpd8lkdj55o11mqg0ad6c641.darc": true,
		"eilq5nicq2ag8evhvnp468b0dnmgjrr1":      true, // bare table file
		"manifest":                              false,
		"LOCK":                                  false,
		"short.darc":                            true,
		"NOTAHASHNOTAHASHNOTAHASHNOTAHASH":      false, // 32 chars, not base32
	}
	for name, want := range cases {
		if got := isChunkFile(name); got != want {
			t.Errorf("isChunkFile(%q) = %v, want %v", name, got, want)
		}
	}
}
