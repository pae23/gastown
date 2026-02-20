package cmd

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/events"
	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/polecat"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/tmux"
	"github.com/steveyegge/gastown/internal/workspace"
)

// runBatchSling handles slinging multiple beads to a rig.
// Each bead gets its own freshly spawned polecat.
func runBatchSling(beadIDs []string, rigName string, townBeadsDir string) error {
	// Validate all beads exist before spawning any polecats
	for _, beadID := range beadIDs {
		if err := verifyBeadExists(beadID); err != nil {
			return fmt.Errorf("bead '%s' not found", beadID)
		}
	}

	// Cross-rig guard: check all beads match the target rig before spawning (gt-myecw)
	if !slingForce {
		townRoot := filepath.Dir(townBeadsDir)
		for _, beadID := range beadIDs {
			prefix := beads.ExtractPrefix(beadID)
			beadRig := beads.GetRigNameForPrefix(townRoot, prefix)
			if prefix != "" && beadRig != "" && beadRig != rigName {
				others := make([]string, 0, len(beadIDs)-1)
				for _, id := range beadIDs {
					if id != beadID {
						others = append(others, id)
					}
				}
				// Build the full command suggestion safely — avoid appending to
				// beadIDs which may share a backing array with the caller's args.
				allArgs := make([]string, len(beadIDs)+1)
				copy(allArgs, beadIDs)
				allArgs[len(beadIDs)] = rigName
				return fmt.Errorf("bead %s (prefix %q) belongs to rig %q, but target is %q\n\n"+
					"  Options:\n"+
					"    1. Remove the mismatched bead from this batch:\n"+
					"         gt sling %s\n"+
					"    2. Sling the mismatched bead to its own rig:\n"+
					"         gt sling %s %s\n"+
					"    3. Use --force to override the cross-rig guard:\n"+
					"         gt sling %s --force\n",
					beadID, strings.TrimSuffix(prefix, "-"), beadRig, rigName,
					strings.Join(others, " "),
					beadID, beadRig,
					strings.Join(allArgs, " "))
			} else if err := checkCrossRigGuard(beadID, rigName+"/polecats/_", townRoot); err != nil {
				// Fall back to generic guard for edge cases (empty prefix, town-level beads)
				return err
			}
		}
	}

	if slingDryRun {
		fmt.Printf("%s Batch slinging %d beads to rig '%s':\n", style.Bold.Render("🎯"), len(beadIDs), rigName)
		fmt.Printf("  Would cook mol-polecat-work formula once\n")
		for _, beadID := range beadIDs {
			fmt.Printf("  Would spawn polecat and apply mol-polecat-work to: %s\n", beadID)
		}
		return nil
	}

	fmt.Printf("%s Batch slinging %d beads to rig '%s'...\n", style.Bold.Render("🎯"), len(beadIDs), rigName)

	if slingMaxConcurrent > 0 {
		fmt.Printf("  Max concurrent spawns: %d\n", slingMaxConcurrent)
	}

	// Issue #288: Auto-apply mol-polecat-work for batch sling
	// Cook once before the loop for efficiency
	townRoot := filepath.Dir(townBeadsDir)
	formulaName := "mol-polecat-work"
	formulaCooked := false

	// Track results for summary
	type slingResult struct {
		beadID  string
		polecat string
		success bool
		errMsg  string
	}
	results := make([]slingResult, 0, len(beadIDs))
	activeCount := 0 // Track active spawns for --max-concurrent throttling

	// Create a single convoy tracking all beads before spawning polecats.
	// If any bead is already tracked by another convoy, error with details.
	var batchConvoyID string
	trackedByConvoy := make(map[string]bool) // beads successfully tracked in convoy
	if !slingNoConvoy {
		for _, beadID := range beadIDs {
			if existing := isTrackedByConvoy(beadID); existing != "" {
				printConvoyConflict(beadID, existing)
				return fmt.Errorf("cannot create batch convoy: %s is already tracked by convoy %s", beadID, existing)
			}
		}
		convoyID, tracked, err := createBatchConvoy(beadIDs, rigName, slingOwned, slingMerge)
		if err != nil {
			return fmt.Errorf("batch convoy creation failed: %w\n\n  Use --no-convoy to skip convoy tracking and sling beads directly.", err)
		}
		batchConvoyID = convoyID
		for _, id := range tracked {
			trackedByConvoy[id] = true
		}
		fmt.Printf("  %s Created convoy 🚚 %s tracking %d beads\n", style.Bold.Render("→"), convoyID, len(tracked))
	}

	// Spawn a polecat for each bead and sling it
	for i, beadID := range beadIDs {
		// Admission control: throttle spawns when --max-concurrent is set
		if slingMaxConcurrent > 0 && activeCount >= slingMaxConcurrent {
			fmt.Printf("\n%s Max concurrent limit reached (%d), waiting for capacity...\n",
				style.Warning.Render("⏳"), slingMaxConcurrent)
			// Wait for sessions to settle before spawning more
			for wait := 0; wait < 30; wait++ {
				time.Sleep(2 * time.Second)
				if wait >= 2 {
					break
				}
			}
			// Reset counter after cooldown — polecats become self-sufficient
			// quickly, so we use time-based batching rather than precise counting
			activeCount = 0
		}

		fmt.Printf("\n[%d/%d] Slinging %s...\n", i+1, len(beadIDs), beadID)

		// Check bead status
		info, err := getBeadInfo(beadID)
		if err != nil {
			results = append(results, slingResult{beadID: beadID, success: false, errMsg: err.Error()})
			fmt.Printf("  %s Could not get bead info: %v\n", style.Dim.Render("✗"), err)
			continue
		}

		if (info.Status == "pinned" || info.Status == "hooked") && !slingForce {
			results = append(results, slingResult{beadID: beadID, success: false, errMsg: "already " + info.Status})
			fmt.Printf("  %s Already %s (use --force to re-sling)\n", style.Dim.Render("✗"), info.Status)
			continue
		}

		// Guard against slinging deferred beads (gt-1326mw).
		if isDeferredBead(info) && !slingForce {
			results = append(results, slingResult{beadID: beadID, success: false, errMsg: "deferred"})
			fmt.Printf("  %s Skipping deferred bead %s (use --force to override)\n", style.Dim.Render("✗"), beadID)
			continue
		}

		// Guard: burn existing molecules before applying new formula.
		// Runs before polecat spawn to avoid wasted spawn/cleanup on rejected beads.
		if formulaName != "" {
			existingMolecules := collectExistingMolecules(info)
			if len(existingMolecules) > 0 {
				if slingForce {
					fmt.Printf("  %s Burning %d stale molecule(s): %s\n",
						style.Warning.Render("⚠"), len(existingMolecules), strings.Join(existingMolecules, ", "))
					if err := burnExistingMolecules(existingMolecules, beadID, townRoot); err != nil {
						fmt.Printf("  %s Skipping %s: burn failed: %v\n",
							style.Dim.Render("✗"), beadID, err)
						results = append(results, slingResult{beadID: beadID, success: false, errMsg: fmt.Sprintf("burn failed: %v", err)})
						continue
					}
				} else {
					fmt.Printf("  %s Skipping %s: has existing molecule(s) (use --force)\n",
						style.Dim.Render("✗"), beadID)
					results = append(results, slingResult{beadID: beadID, success: false, errMsg: "has existing molecule(s)"})
					continue
				}
			}
		}

		// Spawn a fresh polecat
		spawnOpts := SlingSpawnOptions{
			Force:      slingForce,
			Account:    slingAccount,
			Create:     slingCreate,
			HookBead:   beadID, // Set atomically at spawn time
			Agent:      slingAgent,
			BaseBranch: slingBaseBranch,
		}
		spawnInfo, err := spawnPolecatForSling(rigName, spawnOpts)
		if err != nil {
			results = append(results, slingResult{beadID: beadID, success: false, errMsg: err.Error()})
			fmt.Printf("  %s Failed to spawn polecat: %v\n", style.Dim.Render("✗"), err)
			continue
		}

		targetAgent := spawnInfo.AgentID()
		hookWorkDir := spawnInfo.ClonePath

		// Convoy ID is created before the loop — nothing to do per-bead here.

		// Issue #288: Apply mol-polecat-work via formula-on-bead pattern
		// Cook once (lazy), then instantiate for each bead
		if !formulaCooked {
			workDir := beads.ResolveHookDir(townRoot, beadID, hookWorkDir)
			if err := CookFormula(formulaName, workDir, townRoot); err != nil {
				fmt.Printf("  %s Could not cook formula %s: %v\n", style.Dim.Render("Warning:"), formulaName, err)
				// Fall back to raw hook if formula cook fails
			} else {
				formulaCooked = true
			}
		}

		beadToHook := beadID
		attachedMoleculeID := ""
		if formulaCooked {
			// Auto-inject rig command vars as defaults (user --var flags override)
			rigCmdVars := loadRigCommandVars(townRoot, rigName)
			// Build per-bead vars: rig defaults first, then user vars (higher priority)
			batchVars := append(rigCmdVars, slingVars...)
			if spawnInfo.BaseBranch != "" && spawnInfo.BaseBranch != "main" {
				batchVars = append(batchVars, fmt.Sprintf("base_branch=%s", spawnInfo.BaseBranch))
			}
			result, err := InstantiateFormulaOnBead(formulaName, beadID, info.Title, hookWorkDir, townRoot, true, batchVars)
			if err != nil {
				// Best-effort: in batch mode, a formula instantiation failure should not abort or rollback the
				// spawned polecat. We still hook the raw bead so work can proceed (e.g., missing required vars).
				fmt.Printf("  %s Could not apply formula: %v (hooking raw bead)\n", style.Dim.Render("Warning:"), err)
			} else {
				fmt.Printf("  %s Formula %s applied\n", style.Bold.Render("✓"), formulaName)
				beadToHook = result.BeadToHook
				attachedMoleculeID = result.WispRootID
			}
		}

		// Hook the bead (or wisp compound if formula was applied) with retry
		hookDir := beads.ResolveHookDir(townRoot, beadToHook, hookWorkDir)
		if err := hookBeadWithRetry(beadToHook, targetAgent, hookDir); err != nil {
			results = append(results, slingResult{beadID: beadID, polecat: spawnInfo.PolecatName, success: false, errMsg: "hook failed"})
			fmt.Printf("  %s Failed to hook bead: %v\n", style.Dim.Render("✗"), err)
			// Clean up orphaned polecat to avoid leaving spawned-but-unhookable polecats
			cleanupSpawnedPolecat(spawnInfo, rigName)
			continue
		}

		fmt.Printf("  %s Work attached to %s\n", style.Bold.Render("✓"), spawnInfo.PolecatName)

		// Log sling event
		actor := detectActor()
		_ = events.LogFeed(events.TypeSling, actor, events.SlingPayload(beadToHook, targetAgent))

		// Update agent bead state
		updateAgentHookBead(targetAgent, beadToHook, hookWorkDir, townBeadsDir)

		// Store all attachment fields in a single read-modify-write cycle.
		// This eliminates the race condition where sequential independent updates
		// could overwrite each other under concurrent access.
		//
		// Only stamp ConvoyID on beads that the convoy actually tracks.
		// A bead whose dep add failed should not reference a convoy that
		// has no knowledge of it (review fix: partial dep failure).
		convoyIDForBead := ""
		if trackedByConvoy[beadID] {
			convoyIDForBead = batchConvoyID
		}
		fieldUpdates := beadFieldUpdates{
			Dispatcher:       actor,
			Args:             slingArgs,
			AttachedMolecule: attachedMoleculeID,
			NoMerge:          slingNoMerge,
			ConvoyID:         convoyIDForBead,
			MergeStrategy:    slingMerge,
		}
		// Use beadToHook for the update target (may differ from beadID when formula-on-bead)
		if err := storeFieldsInBead(beadToHook, fieldUpdates); err != nil {
			fmt.Printf("  %s Could not store fields in bead: %v\n", style.Dim.Render("Warning:"), err)
		}

		// Create Dolt branch AFTER all sling writes are complete.
		// CommitWorkingSet flushes working set to HEAD, then CreatePolecatBranch
		// forks from HEAD — ensuring the polecat's branch includes all writes.
		if spawnInfo.DoltBranch != "" {
			if err := spawnInfo.CreateDoltBranch(); err != nil {
				fmt.Printf("  %s Could not create Dolt branch: %v, cleaning up...\n", style.Dim.Render("✗"), err)
				rollbackSlingArtifactsFn(spawnInfo, beadToHook, hookWorkDir)
				results = append(results, slingResult{beadID: beadID, polecat: spawnInfo.PolecatName, success: false})
				continue
			}
		}

		// Start polecat session now that molecule/bead is attached.
		// This ensures polecat sees its work when gt prime runs on session start.
		pane, err := spawnInfo.StartSession()
		if err != nil {
			fmt.Printf("  %s Could not start session: %v, cleaning up partial state...\n", style.Dim.Render("✗"), err)
			rollbackSlingArtifactsFn(spawnInfo, beadToHook, hookWorkDir)
			results = append(results, slingResult{beadID: beadID, polecat: spawnInfo.PolecatName, success: false})
			continue
		} else {
			fmt.Printf("  %s Session started for %s\n", style.Bold.Render("▶"), spawnInfo.PolecatName)
			// Fresh polecats get StartupNudge from SessionManager.Start(),
			// so no need to inject a start prompt here.
			_ = pane
		}

		activeCount++
		results = append(results, slingResult{beadID: beadID, polecat: spawnInfo.PolecatName, success: true})

		// Delay between spawns to prevent Dolt lock contention — sequential
		// spawns without delay cause database lock timeouts when multiple bd
		// operations (agent bead creation, hook setting) overlap.
		if i < len(beadIDs)-1 {
			time.Sleep(2 * time.Second)
		}
	}

	if !slingNoBoot {
		wakeRigAgents(rigName)
	}

	// Print summary
	successCount := 0
	for _, r := range results {
		if r.success {
			successCount++
		}
	}

	fmt.Printf("\n%s Batch sling complete: %d/%d succeeded\n", style.Bold.Render("📊"), successCount, len(beadIDs))
	if successCount < len(beadIDs) {
		for _, r := range results {
			if !r.success {
				fmt.Printf("  %s %s: %s\n", style.Dim.Render("✗"), r.beadID, r.errMsg)
			}
		}
	}

	// If no beads succeeded and we created a convoy, clean it up
	if successCount == 0 && batchConvoyID != "" {
		closeCmd := exec.Command("bd", "close", batchConvoyID, "-r", "all beads failed to sling")
		closeCmd.Dir = townBeadsDir
		if err := closeCmd.Run(); err != nil {
			fmt.Printf("  %s Could not clean up empty convoy %s: %v\n", style.Dim.Render("Warning:"), batchConvoyID, err)
		} else {
			fmt.Printf("  %s Cleaned up empty convoy %s\n", style.Dim.Render("○"), batchConvoyID)
		}
	}

	return nil
}

// cleanupSpawnedPolecat removes a polecat that was spawned but whose hook failed,
// preventing orphaned polecats from accumulating.
func cleanupSpawnedPolecat(spawnInfo *SpawnedPolecatInfo, rigName string) {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return
	}
	rigsConfigPath := filepath.Join(townRoot, "mayor", "rigs.json")
	rigsConfig, err := config.LoadRigsConfig(rigsConfigPath)
	if err != nil {
		return
	}
	g := git.NewGit(townRoot)
	rigMgr := rig.NewManager(townRoot, rigsConfig, g)
	r, err := rigMgr.GetRig(rigName)
	if err != nil {
		return
	}
	polecatGit := git.NewGit(r.Path)
	t := tmux.NewTmux()
	polecatMgr := polecat.NewManager(r, polecatGit, t)
	if err := polecatMgr.Remove(spawnInfo.PolecatName, true); err != nil {
		fmt.Printf("  %s Could not clean up orphaned polecat %s: %v\n",
			style.Dim.Render("Warning:"), spawnInfo.PolecatName, err)
	} else {
		fmt.Printf("  %s Cleaned up orphaned polecat %s\n",
			style.Dim.Render("○"), spawnInfo.PolecatName)
	}
}

// allBeadIDs returns true if every arg looks like a bead ID (syntactic check).
func allBeadIDs(args []string) bool {
	for _, arg := range args {
		if !looksLikeBeadID(arg) {
			return false
		}
	}
	return len(args) > 0
}

// resolveRigFromBeadIDs resolves the target rig from bead prefixes.
// All beads must resolve to the same rig. Returns an error with suggested
// actions if any prefix cannot be resolved or if beads span multiple rigs.
func resolveRigFromBeadIDs(beadIDs []string, townRoot string) (string, error) {
	var resolvedRig string
	mismatches := []string{} // "bead-id -> rig" for error reporting

	for _, beadID := range beadIDs {
		prefix := beads.ExtractPrefix(beadID)
		if prefix == "" {
			return "", fmt.Errorf("cannot resolve rig for %s: no valid prefix\n\n"+
				"  Options:\n"+
				"    1. Specify the rig explicitly:\n"+
				"         gt sling %s <rig>\n"+
				"    2. Check the bead ID is correct:\n"+
				"         bd show %s\n",
				beadID, strings.Join(beadIDs, " "), beadID)
		}

		rigName := beads.GetRigNameForPrefix(townRoot, prefix)
		if rigName == "" {
			return "", fmt.Errorf("cannot resolve rig for %s: prefix %q is not mapped to any rig\n\n"+
				"  The prefix may belong to a town-level bead or the routes are not configured.\n\n"+
				"  Options:\n"+
				"    1. Specify the rig explicitly:\n"+
				"         gt sling %s <rig>\n"+
				"    2. Check the bead's route mapping:\n"+
				"         cat .beads/routes.jsonl | grep %s\n"+
				"    3. Create the bead from the target rig directory instead:\n"+
				"         cd <rig> && bd create --title=...\n",
				beadID, prefix, strings.Join(beadIDs, " "), prefix)
		}

		if resolvedRig == "" {
			resolvedRig = rigName
		}
		mismatches = append(mismatches, fmt.Sprintf("    %s (prefix %s) -> %s", beadID, prefix, rigName))

		if rigName != resolvedRig {
			return "", fmt.Errorf("beads resolve to different rigs:\n\n%s\n\n"+
				"  All beads in a batch sling must target the same rig.\n\n"+
				"  Options:\n"+
				"    1. Sling each rig's beads separately:\n"+
				"         gt sling <bead1> <bead2> ...   (beads for %s)\n"+
				"         gt sling <bead3> <bead4> ...   (beads for %s)\n"+
				"    2. Specify the target rig explicitly:\n"+
				"         gt sling %s <rig>\n",
				strings.Join(mismatches, "\n"),
				resolvedRig, rigName,
				strings.Join(beadIDs, " "))
		}
	}

	if resolvedRig == "" {
		return "", fmt.Errorf("could not resolve rig from bead prefixes")
	}

	return resolvedRig, nil
}
