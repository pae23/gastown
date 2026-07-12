package polecat

import (
	"strings"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/git"
)

// MRFinder is the merge-queue lookup subset of *beads.Beads needed to gather
// workstate evidence. It keeps the evidence path unit-testable without a real
// bd binary.
type MRFinder interface {
	FindMRForBranchAny(branch string) (*beads.Issue, error)
}

// ApplyMQEvidence gathers the merge-queue facts for a polecat branch: whether
// the work is already merged, and failing that, whether an MR bead exists.
//
// Merged is checked FIRST and short-circuits the MR lookup, because MR beads are
// ephemeral wisps that the reaper collects once the work lands. Asking "is there
// an MR bead for this branch?" therefore answers a question about the GC clock,
// not about the work: every polecat that merges successfully eventually loses its
// wisp and looks like it never submitted. A merged patch is proof of submission
// and outranks the missing wisp.
//
// The absence of an MR bead is only meaningful for work that is NOT merged —
// there it still means "pushed but never enqueued", and callers keep treating it
// as NEEDS_MQ_SUBMIT (the crashed-between-push-and-submit guard).
//
// All three evidence gatherers (check-recovery, slot reuse, witness patrol) call
// this so their verdicts cannot drift apart.
func ApplyMQEvidence(in *WorkstateInput, finder MRFinder, worktreePath string, targetRefs []string) {
	if in == nil || !in.MQCheckRequired || !in.HasSubmittableWork || in.MQNotRequired {
		return
	}
	if WorkMergedToTarget(worktreePath, targetRefs) {
		in.WorkMerged = true
		return
	}
	if in.AssignedBeadTerminal {
		// Bead closed, but the work is NOT on the target branch. A closed bead has
		// never been proof of submission (a bead can be closed by hand), so leave
		// the disposition to the unmerged path rather than inventing evidence.
		return
	}
	if finder == nil {
		in.MQLookupFailed = true
		return
	}
	mr, err := finder.FindMRForBranchAny(in.Branch)
	if err != nil {
		in.MQLookupFailed = true
		return
	}
	in.MRSubmitted = mr != nil
}

// ApplyMQEvidenceToSlotReuse is ApplyMQEvidence for the SlotReuseInput-shaped
// callers (witness patrol). It exists so reuse and recovery read the same
// evidence rather than each deciding what a missing MR bead means.
func ApplyMQEvidenceToSlotReuse(in *SlotReuseInput, finder MRFinder, worktreePath string, targetRefs []string) {
	if in == nil {
		return
	}
	ws := WorkstateInput{
		Branch:               in.Branch,
		MQCheckRequired:      in.MQCheckRequired,
		HasSubmittableWork:   in.HasSubmittableWork,
		MQNotRequired:        in.MQNotRequired,
		AssignedBeadTerminal: in.AssignedBeadTerminal,
	}
	ApplyMQEvidence(&ws, finder, worktreePath, targetRefs)
	in.WorkMerged = ws.WorkMerged
	in.MRSubmitted = ws.MRSubmitted
	in.MQLookupFailed = in.MQLookupFailed || ws.MQLookupFailed
}

// WorkMergedToTarget reports whether the worktree's HEAD is already merged into
// one of its target branches as they exist on the push remote. Matching is by
// patch-id, so refinery rebases (which rewrite the SHA) still count as merged.
//
// It fails closed: any error resolving or fetching a target yields false, which
// only ever routes the caller back to the ordinary MR-bead check.
func WorkMergedToTarget(worktreePath string, targetRefs []string) bool {
	if strings.TrimSpace(worktreePath) == "" {
		return false
	}
	g := git.NewGit(worktreePath)
	for _, target := range mergeTargetCandidates(g, targetRefs) {
		if status, err := g.PushRemoteTargetStatus("origin", target); err == nil && status.Preserved {
			return true
		}
	}
	return false
}

// mergeTargetCandidates returns the branch names to test for a merged patch.
// Explicit targets (MR bead target, formula/convoy base_branch) come first; the
// remote default branch is the fallback for a polecat whose MR wisp is gone and
// whose attachment carried no base_branch.
func mergeTargetCandidates(g *git.Git, targetRefs []string) []string {
	var candidates []string
	for _, ref := range targetRefs {
		// Strip any remote qualifier: we resolve the branch on the push remote,
		// so "origin/main" and "main" name the same target here.
		ref = strings.TrimSpace(ref)
		if idx := strings.LastIndex(ref, "/"); idx >= 0 && !strings.HasPrefix(ref, "refs/") {
			ref = ref[idx+1:]
		}
		candidates = append(candidates, ref)
	}
	if def := strings.TrimSpace(g.RemoteDefaultBranch()); def != "" {
		candidates = append(candidates, def)
	}
	candidates = append(candidates, "main")
	return uniqueNonEmpty(candidates)
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]bool, len(values))
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
