package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/polecat"
	"github.com/steveyegge/gastown/internal/style"
)

const defaultPoolSize = 4

var (
	polecatPoolInitCount   int
	polecatPoolInitDryRun  bool
	polecatPoolStatusJSON  bool
)

var polecatPoolCmd = &cobra.Command{
	Use:   "pool",
	Short: "Manage persistent polecat pools",
	Long: `Manage persistent polecat pools in rigs.

A pool is a set of pre-created polecats with identities and worktrees,
all starting in IDLE state and ready for dispatch via gt sling.

Pool size is configured in the rig's settings/config.json under namepool.pool_size.`,
	RunE: requireSubcommand,
}

var polecatPoolInitCmd = &cobra.Command{
	Use:   "init <rig>",
	Short: "Initialize a persistent polecat pool for a rig",
	Long: `Create N persistent polecats with identities and worktrees.

Each polecat gets:
  - A name from the rig's name pool
  - A git worktree (on main, ready for work)
  - An identity bead (agent_state=idle)

Pool size is determined by (in priority order):
  1. --count flag
  2. namepool.pool_size in rig settings
  3. Default of 4

Existing polecats are counted toward the target — only missing slots are filled.
Idempotent: running twice with the same count is safe.

Examples:
  gt polecat pool init gastown           # Use configured pool size (or default 4)
  gt polecat pool init gastown --count 6 # Create pool of 6
  gt polecat pool init gastown --dry-run # Show what would be created`,
	Args: cobra.ExactArgs(1),
	RunE: runPolecatPoolInit,
}

var polecatPoolStatusCmd = &cobra.Command{
	Use:   "status <rig>",
	Short: "Show pool status for a rig",
	Long: `Show the current state of the polecat pool for a rig.

Displays each polecat's name, state, and assigned work (if any).

Examples:
  gt polecat pool status gastown
  gt polecat pool status gastown --json`,
	Args: cobra.ExactArgs(1),
	RunE: runPolecatPoolStatus,
}

func init() {
	polecatPoolInitCmd.Flags().IntVar(&polecatPoolInitCount, "count", 0, "Target pool size (overrides config)")
	polecatPoolInitCmd.Flags().BoolVar(&polecatPoolInitDryRun, "dry-run", false, "Show what would be created without creating")

	polecatPoolStatusCmd.Flags().BoolVar(&polecatPoolStatusJSON, "json", false, "Output as JSON")

	polecatPoolCmd.AddCommand(polecatPoolInitCmd)
	polecatPoolCmd.AddCommand(polecatPoolStatusCmd)

	polecatCmd.AddCommand(polecatPoolCmd)
}

func runPolecatPoolInit(cmd *cobra.Command, args []string) error {
	rigName := args[0]

	mgr, r, err := getPolecatManager(rigName)
	if err != nil {
		return err
	}

	// Determine target pool size
	targetSize := polecatPoolInitCount
	if targetSize == 0 {
		targetSize = getConfiguredPoolSize(r.Path)
	}
	if targetSize <= 0 {
		targetSize = defaultPoolSize
	}

	// List existing polecats
	existing, err := mgr.List()
	if err != nil {
		return fmt.Errorf("listing existing polecats: %w", err)
	}

	existingCount := len(existing)
	toCreate := targetSize - existingCount
	if toCreate <= 0 {
		fmt.Printf("Pool already has %d polecats (target: %d). Nothing to create.\n", existingCount, targetSize)
		if existingCount > 0 {
			printPoolSummary(existing)
		}
		return nil
	}

	fmt.Printf("Initializing polecat pool for rig '%s'...\n", rigName)
	fmt.Printf("  Target: %d polecats\n", targetSize)
	fmt.Printf("  Existing: %d\n", existingCount)
	fmt.Printf("  Creating: %d\n", toCreate)
	fmt.Println()

	if polecatPoolInitDryRun {
		fmt.Printf("Dry run — would allocate %d names and create worktrees:\n", toCreate)
		for i := 0; i < toCreate; i++ {
			name, err := mgr.AllocateName()
			if err != nil {
				return fmt.Errorf("allocating name %d: %w", i+1, err)
			}
			fmt.Printf("  %d. %s\n", i+1, name)
			// Release the name since this is dry-run
			mgr.ReleaseName(name)
		}
		return nil
	}

	var created []string
	var failed []string

	for i := 0; i < toCreate; i++ {
		name, err := mgr.AllocateName()
		if err != nil {
			return fmt.Errorf("allocating name for slot %d: %w", existingCount+i+1, err)
		}

		fmt.Printf("  Creating %s...", name)

		// Create the polecat (worktree + agent bead)
		p, err := mgr.Add(name)
		if err != nil {
			fmt.Printf(" %s (%v)\n", style.Error.Render("FAILED"), err)
			failed = append(failed, fmt.Sprintf("%s: %v", name, err))
			continue
		}

		// Transition agent state from "spawning" to "idle" — pool polecats start idle
		if err := mgr.SetAgentStateWithRetry(name, "idle"); err != nil {
			style.PrintWarning("could not set %s to idle: %v", name, err)
		}

		// Sync worktree to main so it's ready for the next assignment.
		// The AddWithOptions created it on a polecat/<name>-<timestamp> branch;
		// we want it sitting on main like a polecat that just finished gt done.
		wtGit := git.NewGit(p.ClonePath)
		defaultBranch := "main"
		if err := wtGit.Checkout(defaultBranch); err != nil {
			style.PrintWarning("could not checkout %s for %s: %v", defaultBranch, name, err)
		}

		fmt.Printf(" %s\n", style.SuccessPrefix)
		created = append(created, name)
	}

	fmt.Println()
	if len(created) > 0 {
		fmt.Printf("%s Created %d persistent polecats (%s)\n",
			style.SuccessPrefix, len(created), strings.Join(created, ", "))
	}
	if len(failed) > 0 {
		fmt.Printf("%s Failed to create %d polecats:\n", style.Error.Render("!"), len(failed))
		for _, f := range failed {
			fmt.Printf("  - %s\n", f)
		}
	}

	totalNow := existingCount + len(created)
	fmt.Printf("Pool ready: %d/%d polecats available for dispatch via gt sling\n", totalNow, targetSize)

	return nil
}

func runPolecatPoolStatus(cmd *cobra.Command, args []string) error {
	rigName := args[0]

	mgr, r, err := getPolecatManager(rigName)
	if err != nil {
		return err
	}

	polecats, err := mgr.List()
	if err != nil {
		return fmt.Errorf("listing polecats: %w", err)
	}

	targetSize := getConfiguredPoolSize(r.Path)
	if targetSize <= 0 {
		targetSize = defaultPoolSize
	}

	if polecatPoolStatusJSON {
		return printPoolStatusJSON(polecats, rigName, targetSize)
	}

	fmt.Printf("Pool status for rig '%s' (%d/%d):\n\n", rigName, len(polecats), targetSize)

	if len(polecats) == 0 {
		fmt.Println("  (no polecats — run 'gt polecat pool init' to create)")
		return nil
	}

	printPoolSummary(polecats)
	return nil
}

func printPoolSummary(polecats []*polecat.Polecat) {
	idle, working := 0, 0
	for _, p := range polecats {
		stateIcon := "○"
		stateStr := string(p.State)
		extra := ""

		switch p.State {
		case polecat.StateIdle:
			idle++
			stateIcon = style.Dim.Render("○")
			stateStr = style.Dim.Render("idle")
		case polecat.StateWorking:
			working++
			stateIcon = style.Bold.Render("●")
			stateStr = style.Bold.Render("working")
			if p.Issue != "" {
				extra = fmt.Sprintf(" → %s", p.Issue)
			}
		default:
			stateIcon = "?"
		}

		fmt.Printf("  %s %-12s %s%s\n", stateIcon, p.Name, stateStr, extra)
	}
	fmt.Printf("\n  idle: %d  working: %d  total: %d\n", idle, working, len(polecats))
}

func printPoolStatusJSON(polecats []*polecat.Polecat, rigName string, targetSize int) error {
	type poolEntry struct {
		Name  string `json:"name"`
		State string `json:"state"`
		Issue string `json:"issue,omitempty"`
	}
	type poolStatus struct {
		Rig        string      `json:"rig"`
		TargetSize int         `json:"target_size"`
		Current    int         `json:"current"`
		Polecats   []poolEntry `json:"polecats"`
	}

	entries := make([]poolEntry, 0, len(polecats))
	for _, p := range polecats {
		entries = append(entries, poolEntry{
			Name:  p.Name,
			State: string(p.State),
			Issue: p.Issue,
		})
	}

	status := poolStatus{
		Rig:        rigName,
		TargetSize: targetSize,
		Current:    len(polecats),
		Polecats:   entries,
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(status)
}

// getConfiguredPoolSize reads pool_size from rig settings.
func getConfiguredPoolSize(rigPath string) int {
	settingsPath := filepath.Join(rigPath, "settings", "config.json")
	settings, err := config.LoadRigSettings(settingsPath)
	if err != nil {
		return 0
	}
	if settings.Namepool != nil && settings.Namepool.PoolSize > 0 {
		return settings.Namepool.PoolSize
	}
	return 0
}
