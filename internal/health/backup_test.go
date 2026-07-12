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

// journalTown builds a town root whose live database holds journalSize bytes of
// uncompressed chunk journal and collectedSize bytes of collected chunks in
// oldgen (0 for a store that has never been collected). This is what a freshly
// created or write-heavy database looks like on disk.
func journalTown(t *testing.T, db string, journalSize, collectedSize int) string {
	t.Helper()
	root := t.TempDir()
	noms := filepath.Join(root, ".dolt-data", db, ".dolt", "noms")
	if err := os.MkdirAll(filepath.Join(noms, "oldgen"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(noms, "manifest"), []byte("4:1:manifest\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(noms, doltJournalFile), make([]byte, journalSize), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(noms, journalIndexFile), make([]byte, 32*1024), 0o644); err != nil {
		t.Fatal(err)
	}
	if collectedSize > 0 {
		writeStore(t, filepath.Join(noms, "oldgen"), 1, collectedSize)
	}
	return root
}

func hasDiagnostic(st BackupStatus, substr string) bool {
	for _, d := range st.Diagnostics {
		if strings.Contains(d, substr) {
			return true
		}
	}
	return false
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

// The negative path the Deacon could not isolate on production (gt-1j3e): the
// patrol's remote directory exists but holds nothing, while the hash marker says
// a backup was taken and the directory mtime is fresh. Every liveness signal is
// green; only the content check can catch it.
func TestInspectBackups_EmptyStoreWithFreshHashMarkerIsNotHealthy(t *testing.T) {
	root := town(t, "beads", 4096)
	if err := os.MkdirAll(filepath.Join(backupPath(root, "beads"), "beads-backup"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(backupPath(root, "beads"), backupHashMarker)
	if err := os.WriteFile(marker, []byte("s7s97bc5pa2tnluk86p8ae5t9msolmmo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := statusFor(t, root, "beads")
	if st.Healthy() {
		t.Fatalf("empty store with a fresh hash marker reported healthy: %+v", st)
	}
	if !hasProblem(st, "no dolt store") {
		t.Errorf("expected empty-store problem, got %v", st.Problems)
	}
	if st.HeadHash == "" {
		t.Error("HeadHash is empty; the marker should still be read and reported")
	}
	if st.Age > time.Minute {
		t.Errorf("marker and directory are fresh, so no staleness should be claimed; Age = %v", st.Age)
	}
}

// The parent-dir fallback must not stand in for the patrol's own store. Twelve
// production databases have both a <db>-backup store and a legacy store in the
// backup root; when the first is wiped, preferring "whichever holds a manifest"
// verified the backup against the stale legacy copy and reported GREEN.
func TestInspectBackups_EmptyStoreIsNotMaskedByLegacyStore(t *testing.T) {
	root := town(t, "beads", 4096)

	// A legacy store in the backup root, and the patrol's own target sitting empty.
	writeStore(t, backupPath(root, "beads"), 2, 4096)
	if err := os.MkdirAll(filepath.Join(backupPath(root, "beads"), "beads-backup"), 0o755); err != nil {
		t.Fatal(err)
	}

	st := statusFor(t, root, "beads")
	if st.Healthy() {
		t.Fatalf("wiped backup masked by the legacy store: %+v", st)
	}
	if !hasProblem(st, "no dolt store") {
		t.Errorf("expected empty-store problem, got %v", st.Problems)
	}
	if st.Layout != LayoutCurrent {
		t.Errorf("Layout = %q, want %q: <db>-backup exists, so that is the store", st.Layout, LayoutCurrent)
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

// Older towns synced straight into <db> instead of <db>/<db>-backup. The store
// is real, so it verifies — but the fallback is reported, never silent.
func TestInspectBackups_LegacyLayout(t *testing.T) {
	root := town(t, "dolt", 4096)
	writeStore(t, backupPath(root, "dolt"), 2, 4096)

	st := statusFor(t, root, "dolt")
	if !st.Healthy() {
		t.Fatalf("legacy layout reported unhealthy: %v", st.Problems)
	}
	if st.Layout != LayoutLegacy {
		t.Errorf("Layout = %q, want %q", st.Layout, LayoutLegacy)
	}
	if len(st.Diagnostics) == 0 {
		t.Error("legacy fallback must be reported as a diagnostic, not applied silently")
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

// The live chunk journal is uncompressed and a backup holds only compressed
// chunks, so a journal-dominated database dwarfs its own complete backup. The
// size floor must not read that as truncation (gt-l8tz: the `gt` database's
// 53KB archive against a 922KB journal reported RED at 6%).
func TestInspectBackups_JournalDominatedLiveStoreSkipsSizeFloor(t *testing.T) {
	root := journalTown(t, "gt", 922_482, 0)
	writeStore(t, filepath.Join(backupPath(root, "gt"), "gt-backup"), 1, 53_195)

	st := statusFor(t, root, "gt")
	if !st.Healthy() {
		t.Fatalf("expected healthy, got %v", st.Problems)
	}
	if st.LiveCollectedBytes != 0 {
		t.Errorf("LiveCollectedBytes = %d, want 0 (nothing collected into oldgen)", st.LiveCollectedBytes)
	}
	if !hasDiagnostic(st, "size floor not applicable") {
		t.Errorf("expected a diagnostic explaining the skipped floor, got %v", st.Diagnostics)
	}
}

// Skipping the floor must not blind the check: once a database has collected
// chunks, a truncated backup is still caught — measured against the collected
// bytes, not the journal-inflated total.
func TestInspectBackups_TruncatedBackupCaughtDespiteLargeJournal(t *testing.T) {
	root := journalTown(t, "gt", 900_000, 500_000)
	writeStore(t, filepath.Join(backupPath(root, "gt"), "gt-backup"), 1, 1_000) // 0.2% of collected

	st := statusFor(t, root, "gt")
	if !hasProblem(st, "truncated") {
		t.Errorf("expected size-floor problem, got %v", st.Problems)
	}
}

// A backup sized against collected bytes alone is healthy even when the journal
// makes it look tiny next to the live total.
func TestInspectBackups_SizeFloorMeasuredAgainstCollectedBytes(t *testing.T) {
	root := journalTown(t, "gt", 900_000, 100_000)
	writeStore(t, filepath.Join(backupPath(root, "gt"), "gt-backup"), 1, 90_000) // 9% of live total, 90% of collected

	st := statusFor(t, root, "gt")
	if !st.Healthy() {
		t.Fatalf("expected healthy, got %v", st.Problems)
	}
}

// An empty backup is still empty, whatever the live store's journal is doing.
func TestInspectBackups_JournalDominatedLiveStoreStillFlagsEmptyBackup(t *testing.T) {
	root := journalTown(t, "gt", 922_482, 0)
	if err := os.MkdirAll(filepath.Join(backupPath(root, "gt"), "gt-backup"), 0o755); err != nil {
		t.Fatal(err)
	}

	st := statusFor(t, root, "gt")
	if !hasProblem(st, "backup is empty") {
		t.Errorf("expected empty-backup problem, got %v", st.Problems)
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
