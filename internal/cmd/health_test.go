package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeBackupStore lays down a dolt chunk store: a manifest plus one archive
// chunk file.
func writeBackupStore(t *testing.T, dir string, chunkSize int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest"), []byte("4:1:manifest\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	chunk := strings.Repeat("a", 32) + ".darc"
	if err := os.WriteFile(filepath.Join(dir, chunk), make([]byte, chunkSize), 0o644); err != nil {
		t.Fatal(err)
	}
}

// checkBackupHealth is what `gt health` actually calls, so this is the check that
// decides whether the command goes red. The health package tests prove
// InspectBackups flags an empty store; this proves the command acts on it —
// DoltCorrupt is what a report reader (and the daemon's warnings) keys off.
func TestCheckBackupHealth_EmptyStoreIsReportedCorrupt(t *testing.T) {
	root := t.TempDir()
	writeBackupStore(t, filepath.Join(root, ".dolt-data", "beads", ".dolt", "noms"), 4096)

	// The patrol's remote target exists but holds no dolt store, and the hash
	// marker claims a backup was taken (gt-1j3e).
	backup := filepath.Join(root, ".dolt-backup", "beads")
	if err := os.MkdirAll(filepath.Join(backup, "beads-backup"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, ".last-backup-hash"), []byte("s7s97bc5pa2tnluk86p8ae5t9msolmmo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	bh := checkBackupHealth(root)
	if !bh.DoltCorrupt {
		t.Fatalf("empty backup store not reported corrupt: %+v", bh.DoltBackups)
	}
	if len(bh.DoltBackups) != 1 || bh.DoltBackups[0].Healthy() {
		t.Fatalf("expected one unhealthy database, got %+v", bh.DoltBackups)
	}
}

// The command must go red for a live database that has no backup at all, and
// must count it (gt-drmn). Before this, the denominator was "directories under
// .dolt-backup", so a database created between backup cycles was not red — it was
// absent, and `gt health` printed a fully-verified N/N over an unbacked database.
func TestCheckBackupHealth_LiveDatabaseWithNoBackupIsReportedCorrupt(t *testing.T) {
	root := t.TempDir()
	writeBackupStore(t, filepath.Join(root, ".dolt-data", "beads", ".dolt", "noms"), 4096)
	writeBackupStore(t, filepath.Join(root, ".dolt-backup", "beads", "beads-backup"), 4096)

	// A database goes live; the backup patrol has not run since.
	writeBackupStore(t, filepath.Join(root, ".dolt-data", "newrig", ".dolt", "noms"), 4096)

	bh := checkBackupHealth(root)
	if !bh.DoltCorrupt {
		t.Fatalf("unbacked live database not reported corrupt: %+v", bh.DoltBackups)
	}
	if len(bh.DoltBackups) != 2 {
		t.Fatalf("counted %d database(s), want 2: the unbacked database must be in the denominator (%+v)",
			len(bh.DoltBackups), bh.DoltBackups)
	}
	var found bool
	for _, st := range bh.DoltBackups {
		if st.Name == "newrig" {
			found = true
			if st.Healthy() {
				t.Errorf("unbacked database reported healthy: %+v", st)
			}
		}
	}
	if !found {
		t.Errorf("unbacked database missing from the report: %+v", bh.DoltBackups)
	}
}

func TestCheckBackupHealth_VerifiedStoreIsClean(t *testing.T) {
	root := t.TempDir()
	writeBackupStore(t, filepath.Join(root, ".dolt-data", "beads", ".dolt", "noms"), 4096)
	writeBackupStore(t, filepath.Join(root, ".dolt-backup", "beads", "beads-backup"), 4096)

	bh := checkBackupHealth(root)
	if bh.DoltCorrupt {
		t.Fatalf("healthy backup reported corrupt: %+v", bh.DoltBackups)
	}
	if bh.DoltStale {
		t.Errorf("freshly written backup reported stale (age %ds)", bh.DoltAgeSeconds)
	}
}
