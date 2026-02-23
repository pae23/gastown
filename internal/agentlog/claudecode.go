package agentlog

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// claudeProjectsDir is the path under $HOME where Claude Code stores projects.
	claudeProjectsDir = ".claude/projects"

	// watchPollInterval is how often we poll for new JSONL content or files.
	watchPollInterval = 500 * time.Millisecond

	// watchFileTimeout is how long we wait for a JSONL file to appear after startup.
	watchFileTimeout = 20 * time.Second

	// maxContentLen is the maximum bytes of content captured per event.
	maxContentLen = 4096
)

// ClaudeCodeAdapter watches Claude Code JSONL conversation files.
//
// Claude Code writes conversation files at:
//
//	~/.claude/projects/<hash>/<session-uuid>.jsonl
//
// where <hash> is derived from the working directory by replacing "/" with "-"
// (e.g. /Users/pa/gt/deacon → -Users-pa-gt-deacon).
//
// The adapter finds the most recently modified JSONL file in the project
// directory (polling until one appears), then tails it for new events.
type ClaudeCodeAdapter struct{}

func (a *ClaudeCodeAdapter) AgentType() string { return "claudecode" }

// Watch starts tailing the Claude Code JSONL log for sessionID.
// workDir is the agent's CWD and is used to locate the project hash directory.
func (a *ClaudeCodeAdapter) Watch(ctx context.Context, sessionID, workDir string) (<-chan AgentEvent, error) {
	projectDir, err := claudeProjectDirFor(workDir)
	if err != nil {
		return nil, fmt.Errorf("resolving project dir: %w", err)
	}

	ch := make(chan AgentEvent, 64)
	go func() {
		defer close(ch)

		jsonlPath, err := waitForNewestJSONL(ctx, projectDir)
		if err != nil {
			return // context cancelled or timeout — non-fatal, just exit
		}

		tailJSONL(ctx, jsonlPath, sessionID, a.AgentType(), ch)
	}()
	return ch, nil
}

// claudeProjectDirFor returns the Claude Code project directory for workDir.
// Formula: $HOME/.claude/projects/<hash> where hash = workDir with '/' → '-'.
func claudeProjectDirFor(workDir string) (string, error) {
	abs, err := filepath.Abs(workDir)
	if err != nil {
		return "", fmt.Errorf("resolving absolute path: %w", err)
	}
	hash := strings.ReplaceAll(abs, "/", "-")
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home dir: %w", err)
	}
	return filepath.Join(home, claudeProjectsDir, hash), nil
}

// waitForNewestJSONL polls projectDir until a .jsonl file appears, returning
// the path of the most recently modified one. Respects ctx cancellation.
func waitForNewestJSONL(ctx context.Context, projectDir string) (string, error) {
	deadline := time.Now().Add(watchFileTimeout)
	for {
		path, ok := newestJSONLIn(projectDir)
		if ok {
			return path, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timeout: no JSONL file appeared in %s within %s", projectDir, watchFileTimeout)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(watchPollInterval):
		}
	}
}

// newestJSONLIn returns the most recently modified .jsonl file in dir.
func newestJSONLIn(dir string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	var bestPath string
	var bestTime time.Time
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if bestPath == "" || info.ModTime().After(bestTime) {
			bestPath = filepath.Join(dir, e.Name())
			bestTime = info.ModTime()
		}
	}
	return bestPath, bestPath != ""
}

// tailJSONL reads all existing lines then polls for new ones, emitting AgentEvents.
func tailJSONL(ctx context.Context, path, sessionID, agentType string, ch chan<- AgentEvent) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	reader := bufio.NewReaderSize(f, 256*1024)
	var partial strings.Builder

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			partial.WriteString(line)
		}
		if err == nil || (err == io.EOF && strings.HasSuffix(partial.String(), "\n")) {
			// We have a complete line (either normal read or EOF-terminated final line)
			fullLine := strings.TrimRight(partial.String(), "\r\n")
			partial.Reset()
			if fullLine != "" {
				for _, ev := range parseClaudeCodeLine(fullLine, sessionID, agentType) {
					select {
					case ch <- ev:
					case <-ctx.Done():
						return
					}
				}
			}
		}
		if err == io.EOF {
			// No more data now — poll for more
			select {
			case <-ctx.Done():
				return
			case <-time.After(watchPollInterval):
			}
		} else if err != nil {
			return // unexpected error
		}
	}
}

// ── Claude Code JSONL structures ──────────────────────────────────────────────

// ccEntry is a top-level line in a Claude Code JSONL file.
type ccEntry struct {
	Type      string     `json:"type"`
	Message   *ccMessage `json:"message,omitempty"`
	Timestamp string     `json:"timestamp,omitempty"`
}

// ccMessage is the message field of a ccEntry.
type ccMessage struct {
	Role    string      `json:"role"`
	Content []ccContent `json:"content"`
}

// ccContent is one content block inside a ccMessage.
type ccContent struct {
	Type string `json:"type"`

	// text
	Text string `json:"text,omitempty"`

	// thinking (extended thinking models only)
	Thinking string `json:"thinking,omitempty"`

	// tool_use
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result (content is a string in the simple case)
	Content string `json:"content,omitempty"`
}

// parseClaudeCodeLine parses one JSONL line and returns 0 or more AgentEvents.
func parseClaudeCodeLine(line, sessionID, agentType string) []AgentEvent {
	var entry ccEntry
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		return nil
	}
	// Only emit events for real conversation turns.
	if entry.Type != "assistant" && entry.Type != "user" {
		return nil
	}
	if entry.Message == nil {
		return nil
	}

	ts := time.Now()
	if entry.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339, entry.Timestamp); err == nil {
			ts = t
		}
	}

	var events []AgentEvent
	for _, c := range entry.Message.Content {
		var eventType, content string
		switch c.Type {
		case "text":
			eventType = "text"
			content = c.Text
		case "thinking":
			eventType = "thinking"
			content = c.Thinking
		case "tool_use":
			eventType = "tool_use"
			// Log tool name + truncated JSON input as a summary.
			inputStr := string(c.Input)
			if len(inputStr) > 256 {
				inputStr = inputStr[:256] + "…"
			}
			content = c.Name + ": " + inputStr
		case "tool_result":
			eventType = "tool_result"
			content = c.Content
		default:
			continue
		}
		if content == "" {
			continue
		}
		if len(content) > maxContentLen {
			content = content[:maxContentLen] + "…"
		}
		events = append(events, AgentEvent{
			AgentType: agentType,
			SessionID: sessionID,
			EventType: eventType,
			Role:      entry.Message.Role,
			Content:   content,
			Timestamp: ts,
		})
	}
	return events
}
