package cmd

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/telemetry"
	"github.com/steveyegge/gastown/internal/tmux"
)

var paneLogSession string

const paneLogPollInterval = 200 * time.Millisecond

// paneUIRe matches lines that are pure Claude Code UI chrome with no content value.
var paneUIRe = regexp.MustCompile(
	// Box-drawing lines and blank lines (light + heavy + double + extensions)
	`^[\s─━═⎯│║╭╮╰╯├└┘┐┤┬┴┼╔╗╚╝╠╣╦╩╬╟╞╡╢╤╧╪╫]+$` +
		// Block elements only (borders, progress fill)
		`|^[\s▀▄█▌▐░▒▓▬▭]+$` +
		// Empty prompt glyph
		`|^❯\s*$` +
		// Status bar / footer elements
		`|bypass permissions` +
		`|esc to interrupt` +
		`|ctrl\+r to search history` +
		`|ctrl\+c to cancel` +
		// Spinner-only lines (single glyph + optional spaces, incl. braille)
		`|^[✶✳✢✽✻·⏵⏺⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏\s]+$` +
		// Claude Code status bar: "current: X.Y.Z · latest: X.Y.Z"
		`|current:.*latest:` +
		// Token count lines: "26112 tokens" at end of status bar
		`|\d+ tokens\s*$` +
		// Spinner with timing like "✢ Channeling… (1m 15s · ↑ 1.6k tokens)"
		`|^[✶✳✢✽✻·]\s+\w.*·.*tokens` +
		// Welcome splash box lines (│ ... │ Tips ... │)
		`|^\s*│\s*(Welcome|Tips for|Run /init|Recent activity|No recent|Sonnet|claude-|~/gt/|✻\s*$|\||▟|▐|▝|▘|╰)`,
)

// isUILine reports whether a line is pure terminal chrome with no content value.
func isUILine(line string) bool {
	return paneUIRe.MatchString(strings.TrimSpace(line))
}

var paneLogCmd = &cobra.Command{
	Use:    "pane-log",
	Short:  "Poll pane output to VictoriaLogs via capture-pane",
	Hidden: true,
	RunE:   runPaneLog,
}

func init() {
	paneLogCmd.Flags().StringVar(&paneLogSession, "session", "", "tmux session name (tag for logs)")
	rootCmd.AddCommand(paneLogCmd)
}

func runPaneLog(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	t := tmux.NewTmux()

	var lastCount int
	initialized := false

	ticker := time.NewTicker(paneLogPollInterval)
	defer ticker.Stop()

	for range ticker.C {
		all, err := t.CapturePaneAll(paneLogSession)
		if err != nil {
			// Session may not exist yet or may have died; keep trying.
			continue
		}

		lines := strings.Split(all, "\n")

		if !initialized {
			// Baseline: skip existing content, only emit future lines.
			lastCount = len(lines)
			initialized = true
			continue
		}

		if len(lines) <= lastCount {
			continue
		}

		newLines := lines[lastCount:]
		lastCount = len(lines)

		var filtered []string
		for _, l := range newLines {
			if !isUILine(l) {
				filtered = append(filtered, l)
			}
		}

		content := strings.TrimSpace(strings.Join(filtered, "\n"))
		if content != "" {
			telemetry.RecordPaneOutput(ctx, paneLogSession, content)
		}
	}
	return nil
}
