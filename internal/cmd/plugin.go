package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/plugin"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
)

// Plugin command flags
var (
	pluginListJSON     bool
	pluginShowJSON     bool
	pluginRunForce     bool
	pluginRunDryRun    bool
	pluginHistoryJSON  bool
	pluginHistoryLimit int
	pluginSyncSource   string
	pluginSyncClean    bool
	pluginSyncDryRun   bool
	pluginSyncRef      string
	pluginSyncWorktree bool
	pluginSyncNoFetch  bool
	pluginAuditJSON    bool
	pluginAuditRef     string
	pluginAuditNoFetch bool
	pluginRecordPlugin string
	pluginRecordResult string
	pluginRecordTitle  string
	pluginRecordBody   string
	pluginRecordRig    string
	pluginRecordLabels []string
)

var pluginCmd = &cobra.Command{
	Use:     "plugin",
	GroupID: GroupConfig,
	Short:   "Plugin management",
	Long: `Manage plugins that run during Deacon patrol cycles.

Plugins are periodic automation tasks defined by plugin.md files with TOML frontmatter.

PLUGIN LOCATIONS:
  ~/gt/plugins/           Town-level plugins (universal, apply everywhere)
  <rig>/plugins/          Rig-level plugins (project-specific)

GATE TYPES:
  cooldown    Run if enough time has passed (e.g., 1h)
  cron        Run on a schedule (e.g., "0 9 * * *")
  condition   Run if a check command returns exit 0
  event       Run on events (e.g., startup)
  manual      Never auto-run, trigger explicitly

Examples:
  gt plugin list                    # List all discovered plugins
  gt plugin show <name>             # Show plugin details
  gt plugin list --json             # JSON output`,
	RunE: requireSubcommand,
}

var pluginListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all discovered plugins",
	Long: `List all plugins from town and rig plugin directories.

Plugins are discovered from:
  - ~/gt/plugins/ (town-level)
  - <rig>/plugins/ for each registered rig

When a plugin exists at both levels, the rig-level version takes precedence.

Examples:
  gt plugin list              # Human-readable output
  gt plugin list --json       # JSON output for scripting`,
	RunE: runPluginList,
}

var pluginShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show plugin details",
	Long: `Show detailed information about a plugin.

Displays the plugin's configuration, gate settings, and instructions.

Examples:
  gt plugin show rebuild-gt
  gt plugin show rebuild-gt --json`,
	Args: cobra.ExactArgs(1),
	RunE: runPluginShow,
}

var pluginRunCmd = &cobra.Command{
	Use:   "run <name>",
	Short: "Manually trigger plugin execution",
	Long: `Manually trigger a plugin to run.

By default, checks if the gate would allow execution and informs you
if it wouldn't. Use --force to bypass gate checks.

Examples:
  gt plugin run rebuild-gt              # Run if gate allows
  gt plugin run rebuild-gt --force      # Bypass gate check
  gt plugin run rebuild-gt --dry-run    # Show what would happen`,
	Args: cobra.ExactArgs(1),
	RunE: runPluginRun,
}

var pluginSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Deploy plugins from origin/main to runtime directories",
	Long: `Deploy plugins from the gastown repository to runtime plugin directories.

Plugins are deployed from a git ref (origin/main by default), NOT from a working
tree. Runtime plugins are a deployment of merged code: sourcing them from whatever
checkout you happen to be standing in lets a stale worktree silently re-deploy old
plugins over new ones, and lets merged fixes never reach ~/gt/plugins at all.

Syncs to town-level plugins (~/gt/plugins/) so all rigs see the latest plugins.

Examples:
  gt plugin sync                           # Deploy origin/main to the town
  gt plugin sync --ref v1.2.0              # Deploy a specific ref
  gt plugin sync --no-fetch                # Use the local origin/main as-is
  gt plugin sync --worktree                # Deploy the working tree (dev loop)
  gt plugin sync --source ./plugins        # Explicit source directory
  gt plugin sync --clean                   # Remove plugins not in source
  gt plugin sync --dry-run                 # Show what would happen`,
	SilenceUsage: true,
	RunE:         runPluginSync,
}

var pluginAuditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Audit runtime plugins for staleness against origin/main",
	Long: `Compare every plugin in the town runtime directory against a git ref.

Reports plugins whose deployed content differs from the ref (stale), plugins on
the ref that were never deployed (missing), and plugins in the runtime with no
counterpart on the ref (orphaned).

Exits non-zero when any plugin is stale or missing, so patrols and CI can gate
on it.

Examples:
  gt plugin audit                # Audit ~/gt/plugins against origin/main
  gt plugin audit --json         # Machine-readable report
  gt plugin audit --ref v1.2.0   # Audit against a specific ref`,
	SilenceUsage: true,
	RunE:         runPluginAudit,
}

var pluginHistoryCmd = &cobra.Command{
	Use:   "history <name>",
	Short: "Show plugin execution history",
	Long: `Show recent execution history for a plugin.

Queries ephemeral beads (wisps) that record plugin runs.

Examples:
  gt plugin history rebuild-gt
  gt plugin history rebuild-gt --json
  gt plugin history rebuild-gt --limit 20`,
	Args: cobra.ExactArgs(1),
	RunE: runPluginHistory,
}

var pluginRecordRunCmd = &cobra.Command{
	Use:   "record-run",
	Short: "Record a plugin run receipt",
	Long: `Record a plugin run receipt through the canonical plugin recorder.

The recorder creates an ephemeral type:plugin-run bead, closes it immediately,
and leaves the receipt available to plugin history/cooldown queries that use
closed beads. This keeps plugin scripts from leaking open run-log beads.`,
	RunE: runPluginRecordRun,
}

func init() {
	// List subcommand flags
	pluginListCmd.Flags().BoolVar(&pluginListJSON, "json", false, "Output as JSON")

	// Show subcommand flags
	pluginShowCmd.Flags().BoolVar(&pluginShowJSON, "json", false, "Output as JSON")

	// Run subcommand flags
	pluginRunCmd.Flags().BoolVar(&pluginRunForce, "force", false, "Bypass gate check")
	pluginRunCmd.Flags().BoolVar(&pluginRunDryRun, "dry-run", false, "Show what would happen without executing")

	// History subcommand flags
	pluginHistoryCmd.Flags().BoolVar(&pluginHistoryJSON, "json", false, "Output as JSON")
	pluginHistoryCmd.Flags().IntVar(&pluginHistoryLimit, "limit", 10, "Maximum number of runs to show")

	// Record-run subcommand flags
	pluginRecordRunCmd.Flags().StringVar(&pluginRecordPlugin, "plugin", "", "Plugin name")
	pluginRecordRunCmd.Flags().StringVar(&pluginRecordResult, "result", "", "Run result label value")
	pluginRecordRunCmd.Flags().StringVar(&pluginRecordTitle, "title", "", "Receipt title")
	pluginRecordRunCmd.Flags().StringVar(&pluginRecordBody, "description", "", "Receipt description")
	pluginRecordRunCmd.Flags().StringVar(&pluginRecordRig, "rig", "", "Rig label value")
	pluginRecordRunCmd.Flags().StringArrayVarP(&pluginRecordLabels, "label", "l", nil, "Additional label for the receipt")

	// Sync subcommand flags
	pluginSyncCmd.Flags().StringVar(&pluginSyncSource, "source", "", "Explicit source plugins directory (overrides --ref)")
	pluginSyncCmd.Flags().StringVar(&pluginSyncRef, "ref", plugin.DefaultRef, "Git ref to deploy from")
	pluginSyncCmd.Flags().BoolVar(&pluginSyncWorktree, "worktree", false, "Deploy from the working tree instead of a git ref (dev loop)")
	pluginSyncCmd.Flags().BoolVar(&pluginSyncNoFetch, "no-fetch", false, "Don't fetch the ref before deploying")
	pluginSyncCmd.Flags().BoolVar(&pluginSyncClean, "clean", false, "Remove plugins from target that don't exist in source")
	pluginSyncCmd.Flags().BoolVar(&pluginSyncDryRun, "dry-run", false, "Show what would happen without syncing")

	// Audit subcommand flags
	pluginAuditCmd.Flags().BoolVar(&pluginAuditJSON, "json", false, "Output as JSON")
	pluginAuditCmd.Flags().StringVar(&pluginAuditRef, "ref", plugin.DefaultRef, "Git ref to audit against")
	pluginAuditCmd.Flags().BoolVar(&pluginAuditNoFetch, "no-fetch", false, "Don't fetch the ref before auditing")

	// Add subcommands
	pluginCmd.AddCommand(pluginListCmd)
	pluginCmd.AddCommand(pluginShowCmd)
	pluginCmd.AddCommand(pluginRunCmd)
	pluginCmd.AddCommand(pluginHistoryCmd)
	pluginCmd.AddCommand(pluginRecordRunCmd)
	pluginCmd.AddCommand(pluginSyncCmd)
	pluginCmd.AddCommand(pluginAuditCmd)

	rootCmd.AddCommand(pluginCmd)
}

// getPluginScanner creates a scanner with town root and all rig names.
func getPluginScanner() (*plugin.Scanner, string, error) {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return nil, "", fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	// Load rigs config to get rig names
	rigsConfigPath := constants.MayorRigsPath(townRoot)
	rigsConfig, err := config.LoadRigsConfig(rigsConfigPath)
	if err != nil {
		rigsConfig = &config.RigsConfig{Rigs: make(map[string]config.RigEntry)}
	}

	// Extract rig names
	rigNames := make([]string, 0, len(rigsConfig.Rigs))
	for name := range rigsConfig.Rigs {
		rigNames = append(rigNames, name)
	}
	sort.Strings(rigNames)

	scanner := plugin.NewScanner(townRoot, rigNames)
	return scanner, townRoot, nil
}

func runPluginList(cmd *cobra.Command, args []string) error {
	scanner, townRoot, err := getPluginScanner()
	if err != nil {
		return err
	}

	plugins, err := scanner.DiscoverAll()
	if err != nil {
		return fmt.Errorf("discovering plugins: %w", err)
	}

	// Sort plugins by name
	sort.Slice(plugins, func(i, j int) bool {
		return plugins[i].Name < plugins[j].Name
	})

	if pluginListJSON {
		return outputPluginListJSON(plugins)
	}

	return outputPluginListText(plugins, townRoot)
}

func outputPluginListJSON(plugins []*plugin.Plugin) error {
	summaries := make([]plugin.PluginSummary, len(plugins))
	for i, p := range plugins {
		summaries[i] = p.Summary()
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(summaries)
}

func outputPluginListText(plugins []*plugin.Plugin, townRoot string) error {
	if len(plugins) == 0 {
		fmt.Printf("%s No plugins discovered\n", style.Dim.Render("○"))
		fmt.Printf("\n  Plugin directories:\n")
		fmt.Printf("    %s/plugins/\n", townRoot)
		fmt.Printf("\n  Create a plugin by adding a directory with plugin.md\n")
		return nil
	}

	fmt.Printf("%s Discovered %d plugin(s)\n\n", style.Success.Render("●"), len(plugins))

	// Group by location
	townPlugins := make([]*plugin.Plugin, 0)
	rigPlugins := make(map[string][]*plugin.Plugin)

	for _, p := range plugins {
		if p.Location == plugin.LocationTown {
			townPlugins = append(townPlugins, p)
		} else {
			rigPlugins[p.RigName] = append(rigPlugins[p.RigName], p)
		}
	}

	// Print town-level plugins
	if len(townPlugins) > 0 {
		fmt.Printf("  %s\n", style.Bold.Render("Town-level plugins:"))
		for _, p := range townPlugins {
			printPluginSummary(p)
		}
		fmt.Println()
	}

	// Print rig-level plugins by rig
	rigNames := make([]string, 0, len(rigPlugins))
	for name := range rigPlugins {
		rigNames = append(rigNames, name)
	}
	sort.Strings(rigNames)

	for _, rigName := range rigNames {
		fmt.Printf("  %s\n", style.Bold.Render(fmt.Sprintf("Rig %s:", rigName)))
		for _, p := range rigPlugins[rigName] {
			printPluginSummary(p)
		}
		fmt.Println()
	}

	return nil
}

func printPluginSummary(p *plugin.Plugin) {
	gateType := "manual"
	if p.Gate != nil && p.Gate.Type != "" {
		gateType = string(p.Gate.Type)
	}

	desc := p.Description
	if len(desc) > 50 {
		desc = desc[:47] + "..."
	}

	typeTag := gateType
	if p.IsExecWrapper() {
		typeTag = "exec-wrapper"
	}

	fmt.Printf("    %s %s\n", style.Bold.Render(p.Name), style.Dim.Render(fmt.Sprintf("[%s]", typeTag)))
	if desc != "" {
		fmt.Printf("      %s\n", style.Dim.Render(desc))
	}
}

func runPluginShow(cmd *cobra.Command, args []string) error {
	name := args[0]

	scanner, _, err := getPluginScanner()
	if err != nil {
		return err
	}

	p, err := scanner.GetPlugin(name)
	if err != nil {
		return err
	}

	if pluginShowJSON {
		return outputPluginShowJSON(p)
	}

	return outputPluginShowText(p)
}

func outputPluginShowJSON(p *plugin.Plugin) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(p)
}

func outputPluginShowText(p *plugin.Plugin) error {
	fmt.Printf("%s %s\n", style.Bold.Render("Plugin:"), p.Name)
	fmt.Printf("%s %s\n", style.Bold.Render("Path:"), p.Path)

	if p.Description != "" {
		fmt.Printf("%s %s\n", style.Bold.Render("Description:"), p.Description)
	}

	// Location
	locStr := string(p.Location)
	if p.RigName != "" {
		locStr = fmt.Sprintf("%s (%s)", p.Location, p.RigName)
	}
	fmt.Printf("%s %s\n", style.Bold.Render("Location:"), locStr)

	fmt.Printf("%s %d\n", style.Bold.Render("Version:"), p.Version)

	// Gate
	fmt.Println()
	fmt.Printf("%s\n", style.Bold.Render("Gate:"))
	if p.Gate != nil {
		fmt.Printf("  Type: %s\n", p.Gate.Type)
		if p.Gate.Duration != "" {
			fmt.Printf("  Duration: %s\n", p.Gate.Duration)
		}
		if p.Gate.Schedule != "" {
			fmt.Printf("  Schedule: %s\n", p.Gate.Schedule)
		}
		if p.Gate.Check != "" {
			fmt.Printf("  Check: %s\n", p.Gate.Check)
		}
		if p.Gate.On != "" {
			fmt.Printf("  On: %s\n", p.Gate.On)
		}
	} else {
		fmt.Printf("  Type: manual (no gate section)\n")
	}

	// Tracking
	if p.Tracking != nil {
		fmt.Println()
		fmt.Printf("%s\n", style.Bold.Render("Tracking:"))
		if len(p.Tracking.Labels) > 0 {
			fmt.Printf("  Labels: %s\n", strings.Join(p.Tracking.Labels, ", "))
		}
		fmt.Printf("  Digest: %v\n", p.Tracking.Digest)
	}

	// Execution
	if p.Execution != nil {
		fmt.Println()
		fmt.Printf("%s\n", style.Bold.Render("Execution:"))
		if p.Execution.Type != "" {
			fmt.Printf("  Type: %s\n", p.Execution.Type)
		}
		if len(p.Execution.Wrapper) > 0 {
			fmt.Printf("  Wrapper: %s\n", strings.Join(p.Execution.Wrapper, " "))
		}
		if p.Execution.Timeout != "" {
			fmt.Printf("  Timeout: %s\n", p.Execution.Timeout)
		}
		fmt.Printf("  Notify on failure: %v\n", p.Execution.NotifyOnFailure)
		if p.Execution.Severity != "" {
			fmt.Printf("  Severity: %s\n", p.Execution.Severity)
		}
	}

	// Instructions preview
	if p.Instructions != "" {
		fmt.Println()
		fmt.Printf("%s\n", style.Bold.Render("Instructions:"))
		lines := strings.Split(p.Instructions, "\n")
		preview := lines
		if len(lines) > 10 {
			preview = lines[:10]
		}
		for _, line := range preview {
			fmt.Printf("  %s\n", line)
		}
		if len(lines) > 10 {
			fmt.Printf("  %s\n", style.Dim.Render(fmt.Sprintf("... (%d more lines)", len(lines)-10)))
		}
	}

	return nil
}

func runPluginRun(cmd *cobra.Command, args []string) error {
	name := args[0]

	scanner, townRoot, err := getPluginScanner()
	if err != nil {
		return err
	}

	p, err := scanner.GetPlugin(name)
	if err != nil {
		return err
	}

	// Check gate status for cooldown gates
	gateOpen := true
	gateReason := ""
	if p.Gate != nil && p.Gate.Type == plugin.GateCooldown && !pluginRunForce {
		recorder := plugin.NewRecorder(townRoot)
		duration := p.Gate.Duration
		if duration == "" {
			duration = "1h" // default
		}
		count, err := recorder.CountRunsSince(p.Name, duration)
		if err != nil {
			// Log warning but continue
			fmt.Fprintf(os.Stderr, "Warning: checking gate status: %v\n", err)
		} else if count > 0 {
			gateOpen = false
			gateReason = fmt.Sprintf("ran %d time(s) within %s cooldown", count, duration)
		}
	}

	if pluginRunDryRun {
		fmt.Printf("%s Dry run for plugin: %s\n", style.Bold.Render("Plugin:"), p.Name)
		fmt.Printf("%s %s\n", style.Bold.Render("Location:"), p.Path)
		if p.Gate != nil {
			fmt.Printf("%s %s\n", style.Bold.Render("Gate type:"), p.Gate.Type)
		}
		if !gateOpen {
			fmt.Printf("%s %s (use --force to override)\n", style.Warning.Render("Gate closed:"), gateReason)
		} else {
			fmt.Printf("%s Would execute plugin instructions\n", style.Success.Render("Gate open:"))
		}
		return nil
	}

	if !gateOpen && !pluginRunForce {
		fmt.Printf("%s Gate closed: %s\n", style.Warning.Render("⚠"), gateReason)
		fmt.Printf("  Use --force to bypass gate check\n")
		return nil
	}

	// Execute the plugin
	// For manual runs, we print the instructions for the agent/user to execute
	// Automatic execution via dogs is handled by gt-n08ix.2
	fmt.Printf("%s Running plugin: %s\n", style.Success.Render("●"), p.Name)
	if pluginRunForce && !gateOpen {
		fmt.Printf("  %s\n", style.Dim.Render("(gate bypassed with --force)"))
	}
	fmt.Println()
	fmt.Printf("%s\n", style.Bold.Render("Instructions:"))
	fmt.Println(p.Instructions)

	// Record the run
	recorder := plugin.NewRecorder(townRoot)
	beadID, err := recorder.RecordRun(plugin.PluginRunRecord{
		PluginName: p.Name,
		RigName:    p.RigName,
		Result:     plugin.ResultSuccess, // Manual runs are marked success
		Body:       "Manual run via gt plugin run",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to record run: %v\n", err)
	} else {
		fmt.Printf("\n%s Recorded run: %s\n", style.Dim.Render("●"), beadID)
	}

	return nil
}

// resolvePluginSource resolves the plugin tree to deploy from.
//
// Default is a git ref (origin/main), exported to a temp dir. --source and
// --worktree opt into a working tree, which is a dev-loop convenience: a
// working tree can sit behind main, so it is never the default.
//
// The returned cleanup must be called by the caller.
func resolvePluginSource(townRoot, ref string, worktree, noFetch bool) (sourceDir, label string, cleanup func(), err error) {
	noop := func() {}

	if pluginSyncSource != "" {
		abs, err := filepath.Abs(pluginSyncSource)
		if err != nil {
			return "", "", noop, fmt.Errorf("resolving source path: %w", err)
		}
		return abs, style.Dim.Render(abs), noop, nil
	}

	if worktree {
		dir, err := plugin.FindGastownSource(townRoot)
		if err != nil {
			return "", "", noop, err
		}
		return dir, style.Dim.Render(dir + " (working tree)"), noop, nil
	}

	repo, err := plugin.FindGastownRepo(townRoot)
	if err != nil {
		return "", "", noop, fmt.Errorf("%w; use --source or --worktree to deploy from a directory", err)
	}

	src, err := plugin.ExportPluginsFromRef(repo, ref, !noFetch)
	if err != nil {
		return "", "", noop, err
	}
	return src.Dir, style.Dim.Render(src.Describe()), func() { _ = src.Close() }, nil
}

func runPluginSync(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	sourceDir, sourceLabel, cleanup, err := resolvePluginSource(townRoot, pluginSyncRef, pluginSyncWorktree, pluginSyncNoFetch)
	if err != nil {
		return err
	}
	defer cleanup()

	targetDir := filepath.Join(townRoot, "plugins")

	if pluginSyncDryRun {
		report, err := plugin.DetectDrift(sourceDir, targetDir)
		if err != nil {
			return fmt.Errorf("detecting drift: %w", err)
		}

		fmt.Printf("%s Plugin sync dry run\n", style.Bold.Render("Plugin sync:"))
		fmt.Printf("  Source: %s\n", sourceLabel)
		fmt.Printf("  Target: %s\n\n", targetDir)

		if !report.HasDrift() && len(report.Extra) == 0 {
			fmt.Printf("  %s All plugins up to date\n", style.Success.Render("✓"))
			return nil
		}

		for _, d := range report.Drifted {
			fmt.Printf("  %s %s (content differs)\n", style.Warning.Render("~"), d.Name)
		}
		for _, name := range report.Missing {
			fmt.Printf("  %s %s (new, would be copied)\n", style.Success.Render("+"), name)
		}
		if pluginSyncClean {
			for _, name := range report.Extra {
				fmt.Printf("  %s %s (would be removed)\n", style.Error.Render("-"), name)
			}
		}
		return nil
	}

	result, err := plugin.SyncPlugins(sourceDir, targetDir, pluginSyncClean)
	if err != nil {
		return fmt.Errorf("syncing plugins: %w", err)
	}

	if len(result.Copied) == 0 && len(result.Removed) == 0 {
		fmt.Printf("%s Plugins already up to date (%d checked)\n",
			style.Success.Render("✓"), len(result.Skipped))
		return nil
	}

	fmt.Printf("%s Synced plugins from %s\n", style.Success.Render("●"), sourceLabel)
	for _, name := range result.Copied {
		fmt.Printf("  %s %s\n", style.Success.Render("↑"), name)
	}
	for _, name := range result.Removed {
		fmt.Printf("  %s %s\n", style.Error.Render("×"), name)
	}
	if len(result.Skipped) > 0 {
		fmt.Printf("  %s %d plugin(s) already current\n",
			style.Dim.Render("·"), len(result.Skipped))
	}
	for _, e := range result.Errors {
		fmt.Fprintf(os.Stderr, "  %s %s\n", style.Error.Render("!"), e)
	}

	return nil
}

// pluginAuditReport is the machine-readable result of `gt plugin audit`.
type pluginAuditReport struct {
	Runtime  string   `json:"runtime"`
	Ref      string   `json:"ref"`
	Commit   string   `json:"commit"`
	Stale    []string `json:"stale"`    // deployed, but content differs from the ref
	Missing  []string `json:"missing"`  // on the ref, never deployed
	Orphaned []string `json:"orphaned"` // deployed, but absent from the ref
	Clean    bool     `json:"clean"`
}

func runPluginAudit(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	repo, err := plugin.FindGastownRepo(townRoot)
	if err != nil {
		return err
	}

	src, err := plugin.ExportPluginsFromRef(repo, pluginAuditRef, !pluginAuditNoFetch)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()

	targetDir := filepath.Join(townRoot, "plugins")
	drift, err := plugin.DetectDrift(src.Dir, targetDir)
	if err != nil {
		return fmt.Errorf("auditing plugins: %w", err)
	}

	report := pluginAuditReport{
		Runtime:  targetDir,
		Ref:      src.Ref,
		Commit:   src.Commit,
		Stale:    make([]string, 0, len(drift.Drifted)),
		Missing:  drift.Missing,
		Orphaned: drift.Extra,
		Clean:    !drift.HasDrift(),
	}
	for _, d := range drift.Drifted {
		report.Stale = append(report.Stale, d.Name)
	}
	if report.Missing == nil {
		report.Missing = []string{}
	}
	if report.Orphaned == nil {
		report.Orphaned = []string{}
	}
	sort.Strings(report.Stale)
	sort.Strings(report.Missing)
	sort.Strings(report.Orphaned)

	if pluginAuditJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		printPluginAudit(report, src.Describe())
	}

	if !report.Clean {
		return fmt.Errorf("%d plugin(s) out of date with %s; run 'gt plugin sync'",
			len(report.Stale)+len(report.Missing), src.Ref)
	}
	return nil
}

func printPluginAudit(report pluginAuditReport, source string) {
	fmt.Printf("%s %s\n", style.Bold.Render("Plugin audit:"), source)
	fmt.Printf("  Runtime: %s\n\n", report.Runtime)

	for _, name := range report.Stale {
		fmt.Printf("  %s %s %s\n", style.Error.Render("✗"), name,
			style.Dim.Render("stale — deployed content differs from ref"))
	}
	for _, name := range report.Missing {
		fmt.Printf("  %s %s %s\n", style.Warning.Render("+"), name,
			style.Dim.Render("missing — on ref, never deployed"))
	}
	for _, name := range report.Orphaned {
		fmt.Printf("  %s %s %s\n", style.Dim.Render("○"), name,
			style.Dim.Render("orphaned — deployed, absent from ref"))
	}

	if report.Clean && len(report.Orphaned) == 0 {
		fmt.Printf("  %s All runtime plugins match the ref\n", style.Success.Render("✓"))
		return
	}
	if report.Clean {
		fmt.Printf("\n  %s Deployed plugins match the ref\n", style.Success.Render("✓"))
	}
}

func runPluginHistory(cmd *cobra.Command, args []string) error {
	name := args[0]

	_, townRoot, err := getPluginScanner()
	if err != nil {
		return err
	}

	recorder := plugin.NewRecorder(townRoot)
	runs, err := recorder.GetRunsSince(name, "")
	if err != nil {
		return fmt.Errorf("querying history: %w", err)
	}

	if runs == nil {
		runs = []*plugin.PluginRunBead{}
	}

	// Apply limit
	if pluginHistoryLimit > 0 && len(runs) > pluginHistoryLimit {
		runs = runs[:pluginHistoryLimit]
	}

	if pluginHistoryJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(runs)
	}

	if len(runs) == 0 {
		fmt.Printf("%s No execution history for plugin: %s\n", style.Dim.Render("○"), name)
		return nil
	}

	fmt.Printf("%s Execution history for %s (%d runs)\n\n", style.Success.Render("●"), name, len(runs))

	for _, run := range runs {
		resultStyle := style.Success
		resultIcon := "✓"
		if run.Result == plugin.ResultFailure {
			resultStyle = style.Error
			resultIcon = "✗"
		} else if run.Result == plugin.ResultSkipped {
			resultStyle = style.Dim
			resultIcon = "○"
		}

		fmt.Printf("  %s %s  %s\n",
			resultStyle.Render(resultIcon),
			run.CreatedAt.Format("2006-01-02 15:04"),
			style.Dim.Render(run.ID))
	}

	return nil
}

func runPluginRecordRun(cmd *cobra.Command, args []string) error {
	if pluginRecordPlugin == "" {
		return fmt.Errorf("--plugin is required")
	}
	if pluginRecordResult == "" {
		return fmt.Errorf("--result is required")
	}

	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	recorder := plugin.NewRecorder(townRoot)
	beadID, err := recorder.RecordRun(plugin.PluginRunRecord{
		PluginName:  pluginRecordPlugin,
		RigName:     pluginRecordRig,
		Result:      plugin.RunResult(pluginRecordResult),
		Title:       pluginRecordTitle,
		Body:        pluginRecordBody,
		ExtraLabels: pluginRecordLabels,
	})
	if err != nil {
		return err
	}

	fmt.Fprintln(cmd.OutOrStdout(), beadID)
	return nil
}
