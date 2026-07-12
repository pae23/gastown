package beads

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// newTestTown creates a town root with mayor/town.json and a rigs.json listing
// the given rigs. Returns the town root.
func newTestTown(t *testing.T, rigs ...string) string {
	t.Helper()
	townRoot := t.TempDir()

	mayorDir := filepath.Join(townRoot, "mayor")
	if err := os.MkdirAll(mayorDir, 0o755); err != nil {
		t.Fatalf("mkdir mayor: %v", err)
	}
	if err := os.WriteFile(filepath.Join(mayorDir, "town.json"), []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatalf("write town.json: %v", err)
	}

	rigsJSON := `{"version":1,"rigs":{`
	for i, rig := range rigs {
		if i > 0 {
			rigsJSON += ","
		}
		rigsJSON += `"` + rig + `":{"git_url":"","beads":{"prefix":"gt"}}`
	}
	rigsJSON += `}}`
	if err := os.WriteFile(filepath.Join(mayorDir, "rigs.json"), []byte(rigsJSON), 0o644); err != nil {
		t.Fatalf("write rigs.json: %v", err)
	}
	return townRoot
}

func beadsDirAt(t *testing.T, parts ...string) string {
	t.Helper()
	dir := filepath.Join(append(parts, ".beads")...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	return dir
}

func TestCheckAutoInitAllowed(t *testing.T) {
	town := newTestTown(t, "gastown", "beads")

	tests := []struct {
		name     string
		beadsDir string
		wantErr  bool
	}{
		{
			name:     "town's own beads dir",
			beadsDir: beadsDirAt(t, town),
		},
		{
			name:     "registered rig",
			beadsDir: beadsDirAt(t, town, "gastown"),
		},
		{
			name:     "nested path under a registered rig",
			beadsDir: beadsDirAt(t, town, "gastown", "polecats", "capable"),
		},
		{
			name:     "mayor workspace hosts the hq database",
			beadsDir: beadsDirAt(t, town, "mayor", "rig"),
		},
		{
			name:     "unregistered rig is refused",
			beadsDir: beadsDirAt(t, town, "testrig"),
			wantErr:  true,
		},
		{
			name:     "unregistered rig nested path is refused",
			beadsDir: beadsDirAt(t, town, "testrip", "polecats", "phantom"),
			wantErr:  true,
		},
		{
			name:     "town-level agent workspace is not a rig and is refused",
			beadsDir: beadsDirAt(t, town, "witness"),
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkAutoInitAllowed(tt.beadsDir)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("checkAutoInitAllowed(%s) = nil, want refusal", tt.beadsDir)
				}
				if !errors.Is(err, ErrRigNotRegistered) {
					t.Fatalf("error = %v, want ErrRigNotRegistered", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("checkAutoInitAllowed(%s) = %v, want nil", tt.beadsDir, err)
			}
		})
	}
}

// Outside a town there is no registry to consult, so auto-init stays permissive
// (standalone rigs, fixtures).
func TestCheckAutoInitAllowedOutsideTown(t *testing.T) {
	dir := beadsDirAt(t, t.TempDir(), "standalone")
	if err := checkAutoInitAllowed(dir); err != nil {
		t.Fatalf("checkAutoInitAllowed(%s) = %v, want nil outside a town", dir, err)
	}
}

// A missing/corrupt rigs.json must not become a licence to create databases.
func TestCheckAutoInitAllowedUnreadableRegistry(t *testing.T) {
	town := t.TempDir()
	mayorDir := filepath.Join(town, "mayor")
	if err := os.MkdirAll(mayorDir, 0o755); err != nil {
		t.Fatalf("mkdir mayor: %v", err)
	}
	if err := os.WriteFile(filepath.Join(mayorDir, "town.json"), []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatalf("write town.json: %v", err)
	}
	// No rigs.json at all.

	dir := beadsDirAt(t, town, "somerig")
	err := checkAutoInitAllowed(dir)
	if err == nil {
		t.Fatal("checkAutoInitAllowed = nil with no rigs.json, want refusal")
	}
	if !errors.Is(err, ErrRigNotRegistered) {
		t.Fatalf("error = %v, want ErrRigNotRegistered", err)
	}
}

// ensureDatabaseInitialized must refuse a phantom rig rather than shelling out
// to `bd init` — this is the guard that stopped testrig/testrip DBs appearing
// on the production Dolt server (gt-ousq).
func TestEnsureDatabaseInitializedRefusesPhantomRig(t *testing.T) {
	town := newTestTown(t, "gastown")
	phantom := beadsDirAt(t, town, "testrig")

	err := ensureDatabaseInitialized(phantom)
	if err == nil {
		t.Fatal("ensureDatabaseInitialized(phantom rig) = nil, want refusal")
	}
	if !errors.Is(err, ErrRigNotRegistered) {
		t.Fatalf("error = %v, want ErrRigNotRegistered", err)
	}
}

// A redirected beads dir (polecat, crew, refinery) still short-circuits before
// the registry check — its database lives elsewhere and is never created here.
func TestEnsureDatabaseInitializedRedirectShortCircuits(t *testing.T) {
	town := newTestTown(t, "gastown")
	dir := beadsDirAt(t, town, "testrig", "polecats", "capable")
	if err := os.WriteFile(filepath.Join(dir, "redirect"), []byte("../../.beads\n"), 0o644); err != nil {
		t.Fatalf("write redirect: %v", err)
	}

	if err := ensureDatabaseInitialized(dir); err != nil {
		t.Fatalf("ensureDatabaseInitialized(redirected) = %v, want nil", err)
	}
}
