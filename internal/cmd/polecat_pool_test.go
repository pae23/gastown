package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGetConfiguredPoolSize(t *testing.T) {
	t.Run("returns 0 when no settings file", func(t *testing.T) {
		got := getConfiguredPoolSize("/nonexistent/path")
		if got != 0 {
			t.Errorf("getConfiguredPoolSize() = %d, want 0", got)
		}
	})

	t.Run("returns 0 when no namepool config", func(t *testing.T) {
		tmpDir := t.TempDir()
		settingsDir := filepath.Join(tmpDir, "settings")
		if err := os.MkdirAll(settingsDir, 0755); err != nil {
			t.Fatal(err)
		}
		data := map[string]any{
			"type":    "rig-settings",
			"version": 1,
		}
		writePoolTestJSON(t, filepath.Join(settingsDir, "config.json"), data)

		got := getConfiguredPoolSize(tmpDir)
		if got != 0 {
			t.Errorf("getConfiguredPoolSize() = %d, want 0", got)
		}
	})

	t.Run("returns configured pool_size", func(t *testing.T) {
		tmpDir := t.TempDir()
		settingsDir := filepath.Join(tmpDir, "settings")
		if err := os.MkdirAll(settingsDir, 0755); err != nil {
			t.Fatal(err)
		}
		data := map[string]any{
			"type":    "rig-settings",
			"version": 1,
			"namepool": map[string]any{
				"style":     "mad-max",
				"pool_size": 6,
			},
		}
		writePoolTestJSON(t, filepath.Join(settingsDir, "config.json"), data)

		got := getConfiguredPoolSize(tmpDir)
		if got != 6 {
			t.Errorf("getConfiguredPoolSize() = %d, want 6", got)
		}
	})

	t.Run("returns 0 when pool_size is 0", func(t *testing.T) {
		tmpDir := t.TempDir()
		settingsDir := filepath.Join(tmpDir, "settings")
		if err := os.MkdirAll(settingsDir, 0755); err != nil {
			t.Fatal(err)
		}
		data := map[string]any{
			"type":    "rig-settings",
			"version": 1,
			"namepool": map[string]any{
				"style":     "mad-max",
				"pool_size": 0,
			},
		}
		writePoolTestJSON(t, filepath.Join(settingsDir, "config.json"), data)

		got := getConfiguredPoolSize(tmpDir)
		if got != 0 {
			t.Errorf("getConfiguredPoolSize() = %d, want 0", got)
		}
	})
}

func TestDefaultPoolSize(t *testing.T) {
	if defaultPoolSize != 4 {
		t.Errorf("defaultPoolSize = %d, want 4", defaultPoolSize)
	}
}

func writePoolTestJSON(t *testing.T, path string, data any) {
	t.Helper()
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0644); err != nil {
		t.Fatal(err)
	}
}
