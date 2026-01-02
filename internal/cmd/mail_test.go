package cmd

import "testing"

func TestMatchWorkerPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		caller  string
		want    bool
	}{
		// Exact matches
		{
			name:    "exact match",
			pattern: "gastown/polecats/capable",
			caller:  "gastown/polecats/capable",
			want:    true,
		},
		{
			name:    "exact match with different name",
			pattern: "gastown/polecats/toast",
			caller:  "gastown/polecats/capable",
			want:    false,
		},

		// Wildcard at end
		{
			name:    "wildcard matches polecat",
			pattern: "gastown/polecats/*",
			caller:  "gastown/polecats/capable",
			want:    true,
		},
		{
			name:    "wildcard matches different polecat",
			pattern: "gastown/polecats/*",
			caller:  "gastown/polecats/toast",
			want:    true,
		},
		{
			name:    "wildcard doesn't match wrong rig",
			pattern: "gastown/polecats/*",
			caller:  "beads/polecats/capable",
			want:    false,
		},
		{
			name:    "wildcard doesn't match nested path",
			pattern: "gastown/polecats/*",
			caller:  "gastown/polecats/sub/capable",
			want:    false,
		},

		// Crew patterns
		{
			name:    "crew wildcard matches",
			pattern: "gastown/crew/*",
			caller:  "gastown/crew/max",
			want:    true,
		},
		{
			name:    "crew wildcard doesn't match polecats",
			pattern: "gastown/crew/*",
			caller:  "gastown/polecats/capable",
			want:    false,
		},

		// Different rigs
		{
			name:    "different rig wildcard",
			pattern: "beads/polecats/*",
			caller:  "beads/polecats/capable",
			want:    true,
		},

		// Edge cases
		{
			name:    "empty pattern",
			pattern: "",
			caller:  "gastown/polecats/capable",
			want:    false,
		},
		{
			name:    "empty caller",
			pattern: "gastown/polecats/*",
			caller:  "",
			want:    false,
		},
		{
			name:    "pattern is just wildcard",
			pattern: "*",
			caller:  "anything",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchWorkerPattern(tt.pattern, tt.caller)
			if got != tt.want {
				t.Errorf("matchWorkerPattern(%q, %q) = %v, want %v",
					tt.pattern, tt.caller, got, tt.want)
			}
		})
	}
}

func TestIsEligibleWorker(t *testing.T) {
	tests := []struct {
		name     string
		caller   string
		patterns []string
		want     bool
	}{
		{
			name:     "matches first pattern",
			caller:   "gastown/polecats/capable",
			patterns: []string{"gastown/polecats/*", "gastown/crew/*"},
			want:     true,
		},
		{
			name:     "matches second pattern",
			caller:   "gastown/crew/max",
			patterns: []string{"gastown/polecats/*", "gastown/crew/*"},
			want:     true,
		},
		{
			name:     "matches none",
			caller:   "beads/polecats/capable",
			patterns: []string{"gastown/polecats/*", "gastown/crew/*"},
			want:     false,
		},
		{
			name:     "empty patterns list",
			caller:   "gastown/polecats/capable",
			patterns: []string{},
			want:     false,
		},
		{
			name:     "nil patterns",
			caller:   "gastown/polecats/capable",
			patterns: nil,
			want:     false,
		},
		{
			name:     "exact match in list",
			caller:   "mayor/",
			patterns: []string{"mayor/", "gastown/witness"},
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isEligibleWorker(tt.caller, tt.patterns)
			if got != tt.want {
				t.Errorf("isEligibleWorker(%q, %v) = %v, want %v",
					tt.caller, tt.patterns, got, tt.want)
			}
		})
	}
}
