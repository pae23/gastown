package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/agentlog"
	"github.com/steveyegge/gastown/internal/telemetry"
)

var (
	agentLogSession   string
	agentLogWorkDir   string
	agentLogAgentType string
)

var agentLogCmd = &cobra.Command{
	Use:    "agent-log",
	Short:  "Stream agent conversation events to VictoriaLogs (invoked by session lifecycle)",
	Hidden: true,
	RunE:   runAgentLog,
}

func init() {
	agentLogCmd.Flags().StringVar(&agentLogSession, "session", "", "Gas Town tmux session name (used as log tag)")
	agentLogCmd.Flags().StringVar(&agentLogWorkDir, "work-dir", "", "Agent working directory (used to locate conversation log files)")
	agentLogCmd.Flags().StringVar(&agentLogAgentType, "agent", "claudecode", "Agent type (claudecode, opencode)")
	_ = agentLogCmd.MarkFlagRequired("session")
	_ = agentLogCmd.MarkFlagRequired("work-dir")
	rootCmd.AddCommand(agentLogCmd)
}

func runAgentLog(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	provider, err := telemetry.Init(ctx, "gastown", "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: telemetry init failed: %v\n", err)
	}
	if provider != nil {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = provider.Shutdown(shutdownCtx)
		}()
	}

	adapter := agentlog.NewAdapter(agentLogAgentType)
	if adapter == nil {
		return fmt.Errorf("unknown agent type %q; supported: claudecode, opencode", agentLogAgentType)
	}

	ch, err := adapter.Watch(ctx, agentLogSession, agentLogWorkDir)
	if err != nil {
		return fmt.Errorf("starting watcher: %w", err)
	}

	for ev := range ch {
		telemetry.RecordAgentEvent(ctx, ev.SessionID, ev.AgentType, ev.EventType, ev.Role, ev.Content)
	}
	return nil
}
