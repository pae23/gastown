package agentlog

import (
	"testing"
)

func TestClaudeProjectDirFor(t *testing.T) {
	// The project hash replaces '/' with '-', so the leading slash becomes '-'.
	// e.g., /Users/pa/gt/deacon → $HOME/.claude/projects/-Users-pa-gt-deacon
	dir, err := claudeProjectDirFor("/Users/pa/gt/deacon")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := dir; len(got) == 0 {
		t.Fatal("expected non-empty dir")
	}
	// Should end with -Users-pa-gt-deacon
	const wantSuffix = "-Users-pa-gt-deacon"
	if len(dir) < len(wantSuffix) || dir[len(dir)-len(wantSuffix):] != wantSuffix {
		t.Errorf("dir %q does not end with %q", dir, wantSuffix)
	}
}

func TestParseClaudeCodeLine_Text(t *testing.T) {
	line := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hello world"}]},"timestamp":"2026-02-23T10:00:00Z"}`
	events := parseClaudeCodeLine(line, "hq-mayor", "claudecode")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.EventType != "text" {
		t.Errorf("EventType = %q, want %q", ev.EventType, "text")
	}
	if ev.Role != "assistant" {
		t.Errorf("Role = %q, want %q", ev.Role, "assistant")
	}
	if ev.Content != "Hello world" {
		t.Errorf("Content = %q, want %q", ev.Content, "Hello world")
	}
	if ev.SessionID != "hq-mayor" {
		t.Errorf("SessionID = %q, want %q", ev.SessionID, "hq-mayor")
	}
	if ev.AgentType != "claudecode" {
		t.Errorf("AgentType = %q, want %q", ev.AgentType, "claudecode")
	}
}

func TestParseClaudeCodeLine_ToolUse(t *testing.T) {
	line := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}`
	events := parseClaudeCodeLine(line, "s1", "claudecode")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.EventType != "tool_use" {
		t.Errorf("EventType = %q, want %q", ev.EventType, "tool_use")
	}
	if ev.Content == "" {
		t.Error("Content should not be empty for tool_use")
	}
	// Content should contain the tool name
	if len(ev.Content) < 4 || ev.Content[:4] != "Bash" {
		t.Errorf("Content %q should start with tool name 'Bash'", ev.Content)
	}
}

func TestParseClaudeCodeLine_SkipsUnknownTypes(t *testing.T) {
	line := `{"type":"summary","content":"some summary"}`
	events := parseClaudeCodeLine(line, "s1", "claudecode")
	if len(events) != 0 {
		t.Errorf("expected 0 events for summary type, got %d", len(events))
	}
}

func TestParseClaudeCodeLine_InvalidJSON(t *testing.T) {
	events := parseClaudeCodeLine("not json", "s1", "claudecode")
	if len(events) != 0 {
		t.Errorf("expected 0 events for invalid JSON, got %d", len(events))
	}
}

func TestNewAdapter(t *testing.T) {
	tests := []struct {
		name      string
		agentType string
		wantNil   bool
		wantType  string
	}{
		{"claudecode", "claudecode", false, "claudecode"},
		{"empty defaults to claudecode", "", false, "claudecode"},
		{"opencode", "opencode", false, "opencode"},
		{"unknown", "kiro", true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewAdapter(tt.agentType)
			if tt.wantNil {
				if a != nil {
					t.Errorf("expected nil adapter for %q", tt.agentType)
				}
				return
			}
			if a == nil {
				t.Fatalf("expected non-nil adapter for %q", tt.agentType)
			}
			if a.AgentType() != tt.wantType {
				t.Errorf("AgentType() = %q, want %q", a.AgentType(), tt.wantType)
			}
		})
	}
}
