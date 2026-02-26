// Package beads provides merge request and gate utilities.
package beads

import (
	"strings"
)

// FindMRForBranch searches for an open merge-request bead for the given branch.
// Returns the MR bead if found, nil if not found.
// This enables idempotent `gt done` - if an MR already exists, we skip creation.
func (b *Beads) FindMRForBranch(branch string) (*Issue, error) {
	branchPrefix := "branch: " + branch + "\n"

	// Check issues table first (non-ephemeral MR beads)
	issues, err := b.List(ListOptions{
		Status: "open",
		Label:  "gt:merge-request",
	})
	if err != nil {
		return nil, err
	}
	for _, issue := range issues {
		if strings.HasPrefix(issue.Description, branchPrefix) {
			return issue, nil
		}
	}

	// Fallback: check wisps table. MR beads created as ephemeral (b9900940)
	// live in the wisps table, invisible to bd list --status=open.
	return b.findMRInWisps(branchPrefix)
}

// FindMRForBranchAny searches for a merge-request bead for the given branch
// across all statuses (open and closed). Used by recovery checks to determine
// if work was ever submitted to the merge queue. See #1035.
func (b *Beads) FindMRForBranchAny(branch string) (*Issue, error) {
	branchPrefix := "branch: " + branch + "\n"

	issues, err := b.List(ListOptions{
		Status: "all",
		Label:  "gt:merge-request",
	})
	if err != nil {
		return nil, err
	}
	for _, issue := range issues {
		if strings.HasPrefix(issue.Description, branchPrefix) {
			return issue, nil
		}
	}

	return nil, nil
}

// findMRInWisps searches the wisps table for an open merge-request bead
// matching branchPrefix. Uses bd list --status=all which includes both
// issues (Dolt) and wisps (SQLite) tables with full descriptions.
// Scoped to gt:merge-request label to keep the result set small.
func (b *Beads) findMRInWisps(branchPrefix string) (*Issue, error) {
	issues, err := b.List(ListOptions{
		Status: "all",
		Label:  "gt:merge-request",
	})
	if err != nil {
		return nil, err
	}
	for _, issue := range issues {
		if issue.Status == "closed" {
			continue
		}
		if strings.HasPrefix(issue.Description, branchPrefix) {
			return issue, nil
		}
	}

	return nil, nil
}

