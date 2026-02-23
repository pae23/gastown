//go:build !windows

package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// activatePaneLogging launches gt pane-log as a detached daemon that polls
// capture-pane every 200ms and emits new lines to VictoriaLogs.
// The process is started in a new session (Setsid) so it survives the parent.
// Any previously running pane-log for the same session is terminated first
// to prevent accumulation of duplicate processes across restarts.
func activatePaneLogging(sessionID string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving executable: %w", err)
	}

	// Terminate any existing pane-log process for this session.
	pidFile := paneLogPIDFile(sessionID)
	if data, err := os.ReadFile(pidFile); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			if proc, err := os.FindProcess(pid); err == nil {
				_ = proc.Signal(syscall.SIGTERM) // best-effort
			}
		}
		_ = os.Remove(pidFile)
	}

	cmd := exec.Command(exe, "pane-log", "--session", sessionID)
	cmd.Env = append(os.Environ(),
		"GT_OTEL_LOGS_URL="+os.Getenv("GT_OTEL_LOGS_URL"),
		"GT_OTEL_METRICS_URL="+os.Getenv("GT_OTEL_METRICS_URL"),
	)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return err
	}

	// Record PID so the next call can terminate this process.
	_ = os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0600)
	return nil
}

// paneLogPIDFile returns the path to the PID file for a given session's pane-log process.
func paneLogPIDFile(sessionID string) string {
	safe := strings.NewReplacer("/", "_", " ", "_", ":", "_").Replace(sessionID)
	return filepath.Join(os.TempDir(), "gt-panelog-"+safe+".pid")
}
