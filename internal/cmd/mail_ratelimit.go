package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/steveyegge/gastown/internal/runtime"
)

// mailRateLimit defines the per-session mail send limit for each role.
// A limit of 0 means mail send is completely blocked.
// A limit of -1 means unlimited.
var mailRateLimit = map[Role]int{
	RoleMayor:    -1, // Unlimited
	RoleDeacon:   3,  // Escalations only
	RoleBoot:     0,  // Hard block (dogs)
	RoleDog:      0,  // Hard block
	RoleWitness:  5,  // Protocol messages only
	RoleRefinery: 5,  // Protocol messages only
	RolePolecat:  1,  // 0-1 per session
	RoleCrew:     1,  // Same as polecat
	RoleUnknown:  -1, // Overseer/human — unlimited
}

// mailRateLimitState tracks send counts per session using a file.
// The file lives in $TMPDIR/gt-mail-ratelimit/<session-id>.json
type mailRateLimitState struct {
	Count int `json:"count"`
}

var rateLimitDir string
var rateLimitOnce sync.Once

func rateLimitStateDir() string {
	rateLimitOnce.Do(func() {
		tmpDir := os.TempDir()
		rateLimitDir = filepath.Join(tmpDir, "gt-mail-ratelimit")
		_ = os.MkdirAll(rateLimitDir, 0o700)
	})
	return rateLimitDir
}

// checkMailRateLimit checks whether the current role is allowed to send another mail.
// Returns nil if allowed, or an error explaining the limit and suggesting gt nudge.
func checkMailRateLimit() error {
	role, err := getMailSenderRole()
	if err != nil {
		// If we can't determine role, allow the send (fail open for humans)
		return nil
	}

	limit, ok := mailRateLimit[role]
	if !ok {
		// Unknown role in the map — allow (fail open)
		return nil
	}

	// Unlimited
	if limit < 0 {
		return nil
	}

	// Hard block (limit == 0)
	if limit == 0 {
		return fmt.Errorf("role %q is not allowed to send mail\n"+
			"  Use 'gt nudge <target> \"message\"' for routine communication (zero cost, ephemeral)", role)
	}

	// Check session count
	sessionID := getMailSessionID()
	if sessionID == "" {
		// No session ID — can't track, allow the send
		return nil
	}

	state := loadRateLimitState(sessionID)
	if state.Count >= limit {
		return fmt.Errorf("mail send limit reached: %d/%d for role %q this session\n"+
			"  Use 'gt nudge <target> \"message\"' for routine communication (zero cost, ephemeral)\n"+
			"  Use 'gt escalate' for escalations that need to survive session death",
			state.Count, limit, role)
	}

	return nil
}

// recordMailSend increments the send counter for the current session.
func recordMailSend() {
	sessionID := getMailSessionID()
	if sessionID == "" {
		return
	}

	state := loadRateLimitState(sessionID)
	state.Count++
	saveRateLimitState(sessionID, state)
}

// getMailSenderRole returns the Role for the current sender.
func getMailSenderRole() (Role, error) {
	// Check GT_ROLE env var first (authoritative for agent sessions)
	envRole := os.Getenv(EnvGTRole)
	if envRole != "" {
		parsed, _, _ := parseRoleString(envRole)
		return parsed, nil
	}

	// No GT_ROLE means human/overseer — return unknown (unlimited)
	return RoleUnknown, nil
}

// getMailSessionID returns a stable session identifier for rate limit tracking.
func getMailSessionID() string {
	// Use Gas Town's session ID detection
	if sid := runtime.SessionIDFromEnv(); sid != "" {
		return sid
	}
	// Fallback: use PID of parent process (the agent session)
	ppid := os.Getppid()
	if ppid > 1 {
		return "ppid-" + strconv.Itoa(ppid)
	}
	return ""
}

func rateLimitFilePath(sessionID string) string {
	// Sanitize session ID for use as filename
	safe := filepath.Base(sessionID)
	return filepath.Join(rateLimitStateDir(), safe+".json")
}

func loadRateLimitState(sessionID string) mailRateLimitState {
	path := rateLimitFilePath(sessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		return mailRateLimitState{}
	}
	var state mailRateLimitState
	if json.Unmarshal(data, &state) != nil {
		return mailRateLimitState{}
	}
	return state
}

func saveRateLimitState(sessionID string, state mailRateLimitState) {
	path := rateLimitFilePath(sessionID)
	data, _ := json.Marshal(state)
	_ = os.WriteFile(path, data, 0o600)
}
