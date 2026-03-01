package daemon

import (
	"errors"
	"testing"
)

func TestIsConcurrentWriteError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"unrelated error", errors.New("connection refused"), false},
		{"graph has changed", errors.New("rebase: the commit graph has changed during rebase"), true},
		{"graph changed", errors.New("commit graph changed underneath us"), true},
		{"concurrency", errors.New("concurrency error during operation"), true},
		{"not found in graph", errors.New("commit abc123 not found in the commit graph"), true},
		{"case insensitive", errors.New("COMMIT GRAPH HAS CHANGED"), true},
		{"wrapped error", errors.New("rebase execution failed: commit graph has changed"), true},
		{"integrity error", errors.New("integrity check: table missing"), false},
		{"timeout error", errors.New("context deadline exceeded"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isConcurrentWriteError(tt.err)
			if got != tt.want {
				t.Errorf("isConcurrentWriteError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
