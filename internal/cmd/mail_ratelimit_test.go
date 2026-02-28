package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMailRateLimitPolecatAllowsFirst(t *testing.T) {
	t.Setenv("GT_ROLE", "gastown/polecats/capable")
	t.Setenv("CLAUDE_SESSION_ID", "test-session-polecat-first")

	// Ensure clean state
	cleanupRateLimitState(t, "test-session-polecat-first")

	err := checkMailRateLimit()
	if err != nil {
		t.Fatalf("first send should be allowed for polecat, got: %v", err)
	}
}

func TestMailRateLimitPolecatBlocksSecond(t *testing.T) {
	t.Setenv("GT_ROLE", "gastown/polecats/capable")
	t.Setenv("CLAUDE_SESSION_ID", "test-session-polecat-second")

	cleanupRateLimitState(t, "test-session-polecat-second")

	// First send should pass
	if err := checkMailRateLimit(); err != nil {
		t.Fatalf("first send should be allowed: %v", err)
	}
	recordMailSend()

	// Second send should be blocked
	err := checkMailRateLimit()
	if err == nil {
		t.Fatal("second send should be blocked for polecat")
	}
}

func TestMailRateLimitDogHardBlock(t *testing.T) {
	t.Setenv("GT_ROLE", "dog")
	t.Setenv("CLAUDE_SESSION_ID", "test-session-dog")

	cleanupRateLimitState(t, "test-session-dog")

	err := checkMailRateLimit()
	if err == nil {
		t.Fatal("dogs should never be allowed to send mail")
	}
}

func TestMailRateLimitBootHardBlock(t *testing.T) {
	t.Setenv("GT_ROLE", "boot")
	t.Setenv("CLAUDE_SESSION_ID", "test-session-boot")

	cleanupRateLimitState(t, "test-session-boot")

	err := checkMailRateLimit()
	if err == nil {
		t.Fatal("boot should never be allowed to send mail")
	}
}

func TestMailRateLimitMayorUnlimited(t *testing.T) {
	t.Setenv("GT_ROLE", "mayor")
	t.Setenv("CLAUDE_SESSION_ID", "test-session-mayor")

	cleanupRateLimitState(t, "test-session-mayor")

	for i := 0; i < 20; i++ {
		if err := checkMailRateLimit(); err != nil {
			t.Fatalf("mayor should have unlimited sends, blocked at send %d: %v", i+1, err)
		}
		recordMailSend()
	}
}

func TestMailRateLimitWitnessAllowsFive(t *testing.T) {
	t.Setenv("GT_ROLE", "gastown/witness")
	t.Setenv("CLAUDE_SESSION_ID", "test-session-witness")

	cleanupRateLimitState(t, "test-session-witness")

	for i := 0; i < 5; i++ {
		if err := checkMailRateLimit(); err != nil {
			t.Fatalf("witness should allow 5 sends, blocked at send %d: %v", i+1, err)
		}
		recordMailSend()
	}

	// 6th should be blocked
	if err := checkMailRateLimit(); err == nil {
		t.Fatal("witness should be blocked after 5 sends")
	}
}

func TestMailRateLimitDeaconAllowsThree(t *testing.T) {
	t.Setenv("GT_ROLE", "deacon")
	t.Setenv("CLAUDE_SESSION_ID", "test-session-deacon")

	cleanupRateLimitState(t, "test-session-deacon")

	for i := 0; i < 3; i++ {
		if err := checkMailRateLimit(); err != nil {
			t.Fatalf("deacon should allow 3 sends, blocked at send %d: %v", i+1, err)
		}
		recordMailSend()
	}

	// 4th should be blocked
	if err := checkMailRateLimit(); err == nil {
		t.Fatal("deacon should be blocked after 3 sends")
	}
}

func TestMailRateLimitNoRoleMeansHuman(t *testing.T) {
	t.Setenv("GT_ROLE", "")
	t.Setenv("CLAUDE_SESSION_ID", "test-session-human")

	cleanupRateLimitState(t, "test-session-human")

	// No GT_ROLE means human/overseer — should be unlimited
	for i := 0; i < 10; i++ {
		if err := checkMailRateLimit(); err != nil {
			t.Fatalf("human should have unlimited sends, blocked at send %d: %v", i+1, err)
		}
		recordMailSend()
	}
}

func TestMailRateLimitSessionIsolation(t *testing.T) {
	t.Setenv("GT_ROLE", "gastown/polecats/alpha")

	// Session 1: send one mail
	t.Setenv("CLAUDE_SESSION_ID", "test-session-iso-1")
	cleanupRateLimitState(t, "test-session-iso-1")
	if err := checkMailRateLimit(); err != nil {
		t.Fatalf("session 1 first send should work: %v", err)
	}
	recordMailSend()

	// Session 2: should have its own counter
	t.Setenv("CLAUDE_SESSION_ID", "test-session-iso-2")
	cleanupRateLimitState(t, "test-session-iso-2")
	if err := checkMailRateLimit(); err != nil {
		t.Fatalf("session 2 first send should work (independent counter): %v", err)
	}
}

func TestMailRateLimitRefineryAllowsFive(t *testing.T) {
	t.Setenv("GT_ROLE", "gastown/refinery")
	t.Setenv("CLAUDE_SESSION_ID", "test-session-refinery")

	cleanupRateLimitState(t, "test-session-refinery")

	for i := 0; i < 5; i++ {
		if err := checkMailRateLimit(); err != nil {
			t.Fatalf("refinery should allow 5 sends, blocked at send %d: %v", i+1, err)
		}
		recordMailSend()
	}

	if err := checkMailRateLimit(); err == nil {
		t.Fatal("refinery should be blocked after 5 sends")
	}
}

// cleanupRateLimitState removes the rate limit state file for a test session.
func cleanupRateLimitState(t *testing.T, sessionID string) {
	t.Helper()
	path := filepath.Join(rateLimitStateDir(), filepath.Base(sessionID)+".json")
	_ = os.Remove(path)
	t.Cleanup(func() {
		_ = os.Remove(path)
	})
}
