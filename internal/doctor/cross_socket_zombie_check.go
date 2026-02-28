package doctor

import (
	"fmt"

	"github.com/steveyegge/gastown/internal/events"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/tmux"
)

// CrossSocketZombieCheck detects agent sessions on the default tmux socket
// when a separate town socket exists. After the socket isolation fix (gt-qkekp),
// sessions that were created on the wrong socket remain as zombies consuming
// resources and potentially causing split-brain behavior.
type CrossSocketZombieCheck struct {
	FixableCheck
	zombieSessions []string // Cached during Run for use in Fix
}

// NewCrossSocketZombieCheck creates a new cross-socket zombie check.
func NewCrossSocketZombieCheck() *CrossSocketZombieCheck {
	return &CrossSocketZombieCheck{
		FixableCheck: FixableCheck{
			BaseCheck: BaseCheck{
				CheckName:        "cross-socket-zombies",
				CheckDescription: "Detect agent sessions on wrong tmux socket",
				CheckCategory:    CategoryCleanup,
			},
		},
	}
}

// Run checks for Gas Town agent sessions on the default socket when a town socket exists.
func (c *CrossSocketZombieCheck) Run(ctx *CheckContext) *CheckResult {
	townSocket := tmux.GetDefaultSocket()
	if townSocket == "" || townSocket == "default" {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "No separate town socket configured (single-socket mode)",
		}
	}

	// List sessions on the default socket
	defaultTmux := tmux.NewTmuxWithSocket("default")
	sessions, err := defaultTmux.ListSessions()
	if err != nil {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "No default socket server running",
		}
	}

	if len(sessions) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "No sessions on default socket",
		}
	}

	// Find agent sessions on the default socket that should be on the town socket
	var zombies []string
	var userSessions int

	for _, sess := range sessions {
		if sess == "" {
			continue
		}

		// Check if this session name matches a Gas Town agent pattern
		if session.IsKnownSession(sess) {
			zombies = append(zombies, sess)
		} else {
			userSessions++
		}
	}

	// Cache zombies for Fix
	c.zombieSessions = zombies

	if len(zombies) == 0 {
		msg := "No agent sessions on default socket"
		if userSessions > 0 {
			msg = fmt.Sprintf("Default socket has %d user session(s), no agent zombies", userSessions)
		}
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: msg,
		}
	}

	details := make([]string, 0, len(zombies)+1)
	details = append(details, fmt.Sprintf("Town socket: %s (agent sessions should be here)", townSocket))
	for _, sess := range zombies {
		details = append(details, fmt.Sprintf("  Zombie on default socket: %s", sess))
	}

	return &CheckResult{
		Name:    c.Name(),
		Status:  StatusWarning,
		Message: fmt.Sprintf("Found %d agent session(s) on default socket (should be on %s)", len(zombies), townSocket),
		Details: details,
		FixHint: "Run 'gt doctor --fix' to kill cross-socket zombie sessions",
	}
}

// Fix kills agent sessions on the default socket, preserving user sessions.
func (c *CrossSocketZombieCheck) Fix(ctx *CheckContext) error {
	if len(c.zombieSessions) == 0 {
		return nil
	}

	defaultTmux := tmux.NewTmuxWithSocket("default")
	var lastErr error

	for _, sess := range c.zombieSessions {
		// Log pre-death event for audit trail
		_ = events.LogFeed(events.TypeSessionDeath, sess,
			events.SessionDeathPayload(sess, "unknown", "cross-socket zombie cleanup", "gt doctor"))

		if err := defaultTmux.KillSessionWithProcesses(sess); err != nil {
			lastErr = err
		}
	}

	return lastErr
}
