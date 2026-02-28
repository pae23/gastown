package doctor

import (
	"testing"

	"github.com/steveyegge/gastown/internal/tmux"
)

func TestNewCrossSocketZombieCheck(t *testing.T) {
	check := NewCrossSocketZombieCheck()

	if check.Name() != "cross-socket-zombies" {
		t.Errorf("expected name 'cross-socket-zombies', got %q", check.Name())
	}

	if !check.CanFix() {
		t.Error("expected CanFix to return true")
	}

	if check.Description() != "Detect agent sessions on wrong tmux socket" {
		t.Errorf("unexpected description: %q", check.Description())
	}
}

func TestCrossSocketZombieCheck_NoTownSocket(t *testing.T) {
	// When no town socket is configured (single-socket mode), check should pass
	old := tmux.GetDefaultSocket()
	tmux.SetDefaultSocket("")
	defer tmux.SetDefaultSocket(old)

	check := NewCrossSocketZombieCheck()
	result := check.Run(&CheckContext{TownRoot: t.TempDir()})

	if result.Status != StatusOK {
		t.Errorf("expected StatusOK when no town socket, got %v: %s", result.Status, result.Message)
	}
}

func TestCrossSocketZombieCheck_SameAsDefault(t *testing.T) {
	// When town socket IS "default", check should pass (no cross-socket scenario)
	old := tmux.GetDefaultSocket()
	tmux.SetDefaultSocket("default")
	defer tmux.SetDefaultSocket(old)

	check := NewCrossSocketZombieCheck()
	result := check.Run(&CheckContext{TownRoot: t.TempDir()})

	if result.Status != StatusOK {
		t.Errorf("expected StatusOK when town socket is default, got %v: %s", result.Status, result.Message)
	}
}
