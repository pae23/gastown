package polecat

import "strings"

const (
	WorkstateVerdictWorking       = "WORKING"
	WorkstateVerdictSafeToNuke    = "SAFE_TO_NUKE"
	WorkstateVerdictPendingMR     = "PENDING_MR"
	WorkstateVerdictNeedsRecovery = "NEEDS_RECOVERY"
	WorkstateVerdictNeedsMQSubmit = "NEEDS_MQ_SUBMIT"
)

// WorkstateInput contains the lifecycle, git, and merge-queue facts needed to
// classify a polecat consistently across list, recovery, witness, and capacity.
type WorkstateInput struct {
	State                          State
	HookBead                       string
	CleanupStatus                  CleanupStatus
	IgnoreCleanupStatus            bool
	PartialSpawnWithoutDurableHook bool
	PushFailed                     bool
	MRFailed                       bool
	Branch                         string
	GitDirty                       bool
	GitDirtyReason                 string
	StashCount                     int
	UnpushedCommits                int
	GitCheckFailed                 bool
	GitCheckFailedReason           string
	ActiveWorkBlocker              string
	ActiveWorkCountsTowardCapacity bool
	ActiveMR                       string
	ActiveMRBlocker                string
	MQCheckRequired                bool
	HasSubmittableWork             bool
	MQNotRequired                  bool
	AssignedBeadTerminal           bool
	// WorkMerged means the branch's patches are already present on its target
	// branch. It is proof that the work was submitted and merged, and it outranks
	// MRSubmitted: the MR bead is a wisp the reaper collects once the work lands,
	// so a merged polecat has no MR bead to find.
	WorkMerged     bool
	MRSubmitted    bool
	MQLookupFailed bool
}

// WorkstateDisposition is the canonical polecat lifecycle decision. It is pure
// policy: callers gather facts, this classifier decides how every subsystem
// should present and count the polecat.
type WorkstateDisposition struct {
	Verdict              string   `json:"verdict"`
	Reason               string   `json:"reason,omitempty"`
	Reusable             bool     `json:"reusable"`
	SafeToNuke           bool     `json:"safe_to_nuke"`
	NeedsRecovery        bool     `json:"needs_recovery"`
	NeedsMQSubmit        bool     `json:"needs_mq_submit"`
	MQStatus             string   `json:"mq_status,omitempty"`
	CountsTowardCapacity bool     `json:"counts_toward_capacity"`
	ReuseStatus          string   `json:"reuse_status,omitempty"`
	Blockers             []string `json:"blockers,omitempty"`
}

// DecideWorkstate returns the canonical disposition for a polecat. It is the
// single decision function behind every destructive and advisory caller
// (list, check-recovery, nuke, reuse, capacity), so a refusal it reports to one
// caller is a refusal for all of them.
//
// Invariant: a disposition that is not safe to nuke always names at least one
// blocker. A refusal nobody can attribute to a predicate is unactionable, and
// callers must never be able to read silence as consent.
func DecideWorkstate(in WorkstateInput) WorkstateDisposition {
	d := decideWorkstate(in)
	if !d.SafeToNuke && len(d.Blockers) == 0 {
		reason := d.Reason
		if reason == "" {
			reason = "unspecified"
		}
		state := string(in.State)
		if state == "" {
			state = "unknown"
		}
		d.Blockers = []string{"polecat_state=" + state + " reason=" + reason}
	}
	return d
}

func decideWorkstate(in WorkstateInput) WorkstateDisposition {
	if in.ActiveMRBlocker != "" && !in.PushFailed && !in.MRFailed && in.State == StateDone {
		return WorkstateDisposition{
			Verdict:     WorkstateVerdictPendingMR,
			Reason:      "active-mr-open",
			ReuseStatus: "idle-pr-open",
			Blockers:    []string{in.ActiveMRBlocker},
		}
	}

	// StateDone is the expected pre-cleanup state: the polecat called `gt done`
	// and is waiting to be nuked. It runs the same predicate checklist as an idle
	// polecat — only a real predicate (hook, dirty git, unsubmitted work, pending
	// MR) may refuse cleanup, never the state alone.
	if in.State != StateIdle && in.State != StateDone {
		verdict := WorkstateVerdictNeedsRecovery
		needsRecovery := true
		if in.State == StateWorking {
			verdict = WorkstateVerdictWorking
			needsRecovery = false
		}
		d := WorkstateDisposition{
			Verdict:              verdict,
			Reason:               "not-idle",
			NeedsRecovery:        needsRecovery,
			CountsTowardCapacity: true,
		}
		if in.ActiveWorkBlocker != "" {
			d.Blockers = append(d.Blockers, in.ActiveWorkBlocker)
		}
		return d
	}

	d := WorkstateDisposition{Verdict: WorkstateVerdictSafeToNuke}
	capacityBlocked := false
	block := func(reason, blocker string, countsTowardCapacity bool) {
		if d.Reason == "" {
			d.Reason = reason
		}
		if blocker != "" {
			d.Blockers = append(d.Blockers, blocker)
		}
		capacityBlocked = capacityBlocked || countsTowardCapacity
	}

	if in.HookBead != "" && !in.PartialSpawnWithoutDurableHook {
		block("hook-still-set", "has work on hook ("+in.HookBead+")", true)
	}
	if in.PushFailed {
		block("push-failed", "push_failed=true", true)
	}
	if in.MRFailed {
		block("mr-failed", "mr_failed=true", true)
	}
	if in.ActiveWorkBlocker != "" {
		block("active-work", in.ActiveWorkBlocker, in.ActiveWorkCountsTowardCapacity)
	}
	if !in.IgnoreCleanupStatus && !in.CleanupStatus.IsSafe() {
		reason := "cleanup-" + string(in.CleanupStatus)
		blocker := "cleanup_status=" + string(in.CleanupStatus)
		if in.CleanupStatus == "" {
			reason = "cleanup-unknown"
			blocker = "cleanup_status=<missing>"
		} else if in.CleanupStatus == CleanupUnknown {
			reason = "cleanup-unknown"
		}
		block(reason, blocker, true)
	}
	if in.GitCheckFailed {
		blocker := in.GitCheckFailedReason
		if blocker == "" {
			blocker = "git_state=unknown"
		}
		block("git-check-failed", blocker, true)
	}
	if in.GitDirty {
		blocker := in.GitDirtyReason
		if blocker == "" {
			blocker = "git_state=has_uncommitted"
		}
		block("git-dirty", blocker, true)
	}
	if in.StashCount > 0 {
		block("git-stash", "git_state=has_stash stash_count="+itoa(in.StashCount), true)
	}
	if in.UnpushedCommits > 0 {
		block("git-unpushed", "git_state=has_unpushed unpushed_commits="+itoa(in.UnpushedCommits), true)
	}
	activeMRBlocks := in.ActiveMRBlocker != ""
	if activeMRBlocks {
		block("active-mr-open", in.ActiveMRBlocker, false)
	}

	if len(d.Blockers) > 0 {
		if activeMRBlocks && len(d.Blockers) == 1 {
			d.Verdict = WorkstateVerdictPendingMR
			d.ReuseStatus = "idle-pr-open"
			return d
		}
		d.Verdict = WorkstateVerdictNeedsRecovery
		d.NeedsRecovery = true
		d.CountsTowardCapacity = capacityBlocked
		d.ReuseStatus = "idle-recovery-needed"
		return d
	}

	if in.MQCheckRequired {
		if in.WorkMerged {
			// Merged work is proof of submission. It cannot need MQ submission, and
			// it cannot be blocked by a failed MQ lookup: the MR wisp it would look
			// for is gone precisely because the work landed.
			d.MQStatus = "merged"
		} else if in.MQLookupFailed {
			d.Verdict = WorkstateVerdictNeedsRecovery
			d.Reason = "mq-lookup-failed"
			d.NeedsRecovery = true
			d.MQStatus = "unknown"
			d.CountsTowardCapacity = true
			d.ReuseStatus = "idle-recovery-needed"
			d.Blockers = append(d.Blockers, "mq_status=unknown")
			return d
		} else if !in.HasSubmittableWork || in.MQNotRequired {
			d.MQStatus = "not_required"
		} else if in.MRSubmitted {
			d.MQStatus = "submitted"
		} else {
			d.Verdict = WorkstateVerdictNeedsMQSubmit
			d.Reason = "mq-not-submitted"
			d.NeedsRecovery = true
			d.NeedsMQSubmit = true
			d.MQStatus = "not_submitted"
			d.CountsTowardCapacity = true
			d.ReuseStatus = "idle-recovery-needed"
			d.Blockers = append(d.Blockers, "mq_status=not_submitted")
			return d
		}
	}

	d.SafeToNuke = true
	if in.State == StateDone {
		// Cleared every predicate, but a done polecat's sandbox belongs to finished
		// work: it may be nuked, not reused in place. It still holds a worktree, so
		// it keeps counting toward capacity until the nuke lands.
		d.Reason = "done-safe-to-nuke"
		d.CountsTowardCapacity = true
		d.ReuseStatus = "idle-done"
		return d
	}

	d.Reusable = true
	d.Reason = "reusable"
	if strings.HasPrefix(in.Branch, "polecat/") {
		d.ReuseStatus = "idle-preserved"
	} else {
		d.ReuseStatus = "idle-clean"
	}
	return d
}

// CanIgnoreStaleCleanupStatus returns true when a dirty persisted
// cleanup_status is older than the direct predicates proving no work is at risk.
// The status remains unsafe globally; callers must opt into this reconciliation
// path only after gathering live git, hook, work, and active-MR facts.
func CanIgnoreStaleCleanupStatus(status CleanupStatus, workTerminal, hookSafe, activeMRSafe, gitSafe bool) bool {
	if !workTerminal || !hookSafe || !activeMRSafe || !gitSafe {
		return false
	}
	switch status {
	case CleanupUncommitted, CleanupStash, CleanupUnpushed:
		return true
	default:
		return false
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
