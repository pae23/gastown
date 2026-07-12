package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/polecat"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/style"
)

// polecatTarget represents a polecat to operate on.
type polecatTarget struct {
	rigName     string
	polecatName string
	mgr         *polecat.Manager
	r           *rig.Rig
}

// resolvePolecatTargets builds a list of polecats from command args.
// If useAll is true, the first arg is treated as a rig name and all polecats in it are returned.
// Otherwise, args are parsed as rig/polecat addresses.
func resolvePolecatTargets(args []string, useAll bool) ([]polecatTarget, error) {
	var targets []polecatTarget

	if useAll {
		// --all flag: first arg is just the rig name
		rigName := args[0]
		// Check if it looks like rig/polecat format
		if _, _, err := parseAddress(rigName); err == nil {
			return nil, fmt.Errorf("with --all, provide just the rig name (e.g., 'gt polecat <cmd> %s --all')", strings.Split(rigName, "/")[0])
		}

		mgr, r, err := getPolecatManager(rigName)
		if err != nil {
			return nil, err
		}

		polecats, err := mgr.List()
		if err != nil {
			return nil, fmt.Errorf("listing polecats: %w", err)
		}

		for _, p := range polecats {
			targets = append(targets, polecatTarget{
				rigName:     rigName,
				polecatName: p.Name,
				mgr:         mgr,
				r:           r,
			})
		}
	} else {
		// Multiple rig/polecat arguments - require explicit rig/polecat format
		for _, arg := range args {
			// Validate format: must contain "/" to avoid misinterpreting rig names as polecat names
			if !strings.Contains(arg, "/") {
				return nil, fmt.Errorf("invalid address '%s': must be in 'rig/polecat' format (e.g., 'gastown/Toast')", arg)
			}

			rigName, polecatName, err := parseAddress(arg)
			if err != nil {
				return nil, fmt.Errorf("invalid address '%s': %w", arg, err)
			}

			mgr, r, err := getPolecatManager(rigName)
			if err != nil {
				return nil, err
			}

			targets = append(targets, polecatTarget{
				rigName:     rigName,
				polecatName: polecatName,
				mgr:         mgr,
				r:           r,
			})
		}
	}

	return targets, nil
}

// SafetyCheckResult holds the result of safety checks for a polecat.
type SafetyCheckResult struct {
	Polecat       string
	Blocked       bool
	Reasons       []string
	Verdict       string
	CleanupStatus polecat.CleanupStatus
	HookBead      string
	HookStale     bool // true if hooked bead is closed
	ActiveMR      string
	OpenMR        string
	GitState      *GitState
}

// checkPolecatSafety performs safety checks before destructive operations.
// It delegates to assessPolecatRecovery — the same decision `gt polecat
// check-recovery` reports — so the destructive command can never permit what the
// advisory command refuses. A polecat that cannot be assessed is blocked: for a
// command that deletes worktrees, branches, and agent state, silence is not consent.
func checkPolecatSafety(target polecatTarget) *SafetyCheckResult {
	result := &SafetyCheckResult{
		Polecat: fmt.Sprintf("%s/%s", target.rigName, target.polecatName),
	}

	assessment, err := assessPolecatRecovery(target.rigName, target.polecatName, target.mgr, target.r)
	if err != nil {
		result.Blocked = true
		result.Reasons = []string{fmt.Sprintf("recovery_check_failed: %v", err)}
		return result
	}

	return safetyCheckFromRecovery(result.Polecat, assessment.Status)
}

// safetyCheckFromRecovery translates a recovery verdict into a nuke safety
// verdict. The translation is total: anything check-recovery does not call
// SAFE_TO_NUKE blocks the nuke, carrying that verdict's own predicates as the
// reasons. Nuke has no checklist of its own to fall open on.
func safetyCheckFromRecovery(polecatAddr string, status RecoveryStatus) *SafetyCheckResult {
	result := &SafetyCheckResult{
		Polecat:       polecatAddr,
		Verdict:       status.Verdict,
		CleanupStatus: status.CleanupStatus,
		HookBead:      status.HookBead,
		HookStale:     status.HookStale,
		ActiveMR:      status.ActiveMR,
		OpenMR:        status.OpenMR,
		GitState:      status.GitState,
		Blocked:       !status.SafeToNuke,
	}

	if result.Blocked {
		result.Reasons = status.Blockers
		if len(result.Reasons) == 0 {
			result.Reasons = []string{fmt.Sprintf("verdict=%s reason=%s", status.Verdict, status.Reason)}
		}
	}
	return result
}

func rigPrefix(r *rig.Rig) string {
	townRoot := filepath.Dir(r.Path)
	return beads.GetPrefixForRig(townRoot, r.Name)
}

func polecatBeadIDForRig(r *rig.Rig, rigName, polecatName string) string {
	return beads.PolecatBeadIDWithPrefix(rigPrefix(r), rigName, polecatName)
}

// displaySafetyCheckBlocked prints blocked polecats and guidance.
func displaySafetyCheckBlocked(blocked []*SafetyCheckResult) {
	displaySafetyCheckBlockedTo(os.Stderr, blocked)
}

func displaySafetyCheckBlockedTo(w io.Writer, blocked []*SafetyCheckResult) {
	fmt.Fprintf(w, "%s Cannot nuke the following polecats:\n\n", style.Error.Render("Error:"))
	var polecatList []string
	for _, b := range blocked {
		fmt.Fprintf(w, "  %s:\n", style.Bold.Render(b.Polecat))
		for _, r := range b.Reasons {
			fmt.Fprintf(w, "    - %s\n", r)
		}
		polecatList = append(polecatList, b.Polecat)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Safety checks failed. Resolve issues before nuking, or use --force.")
	fmt.Fprintln(w, "Options:")
	fmt.Fprintln(w, "  1. Complete work: gt done (from polecat session)")
	fmt.Fprintln(w, "  2. Push changes: git push (from polecat worktree)")
	fmt.Fprintln(w, "  3. Escalate: gt mail send mayor/ -s \"RECOVERY_NEEDED\" -m \"...\"")
	fmt.Fprintf(w, "  4. Force nuke (LOSES WORK): gt polecat nuke --force %s\n", strings.Join(polecatList, " "))
	fmt.Fprintln(w)
}

func formatSafetyCheckBlockers(blocked []*SafetyCheckResult) string {
	parts := make([]string, 0, len(blocked))
	for _, b := range blocked {
		parts = append(parts, fmt.Sprintf("%s: %s", b.Polecat, strings.Join(b.Reasons, "; ")))
	}
	return strings.Join(parts, " | ")
}

// displayDryRunSafetyCheck shows safety check status for dry-run mode. It prints
// the verdict a real nuke would act on — the same one check-recovery reports —
// and returns true when a normal nuke would refuse.
func displayDryRunSafetyCheck(target polecatTarget) bool {
	fmt.Printf("\n  Safety checks:\n")
	result := checkPolecatSafety(target)

	cleanupStatus := result.CleanupStatus
	switch {
	case cleanupStatus.IsSafe():
		fmt.Printf("    - Cleanup status: %s\n", style.Success.Render(string(cleanupStatus)))
	case cleanupStatus.RequiresRecovery():
		fmt.Printf("    - Cleanup status: %s\n", style.Error.Render(string(cleanupStatus)))
	default:
		statusText := string(cleanupStatus)
		if statusText == "" {
			statusText = "<missing>"
		}
		fmt.Printf("    - Cleanup status: %s\n", style.Warning.Render(statusText))
	}

	switch {
	case result.HookBead == "":
		fmt.Printf("    - Hook: %s\n", style.Success.Render("empty"))
	case result.HookStale:
		fmt.Printf("    - Hook: %s (%s, closed - stale)\n", style.Warning.Render("stale"), result.HookBead)
	default:
		fmt.Printf("    - Hook: %s (%s)\n", style.Error.Render("has work"), result.HookBead)
	}

	if result.ActiveMR != "" {
		fmt.Printf("    - Active MR: %s\n", result.ActiveMR)
	}
	if result.OpenMR != "" {
		fmt.Printf("    - Open MR: %s (%s)\n", style.Error.Render("yes"), result.OpenMR)
	} else {
		fmt.Printf("    - Open MR: %s\n", style.Success.Render("none"))
	}

	if result.Blocked {
		fmt.Printf("    - Verdict: %s\n", style.Error.Render(result.Verdict))
		for _, reason := range result.Reasons {
			fmt.Printf("      - %s\n", reason)
		}
	} else {
		fmt.Printf("    - Verdict: %s\n", style.Success.Render(result.Verdict))
	}

	return result.Blocked
}
