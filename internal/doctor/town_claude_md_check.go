package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/templates"
)

// TownCLAUDEmdCheck verifies the town-root CLAUDE.md is up to date with
// the version embedded in the binary. This is the highest-value migration
// check — behavioral norms for agents come from CLAUDE.md.
//
// The town-root CLAUDE.md (~/gt/CLAUDE.md) is loaded by Claude Code for
// all agents running from within the town git tree (Mayor, Deacon).
// It must contain operational norms (Dolt awareness, communication hygiene,
// nudge-first) that guide agent behavior.
type TownCLAUDEmdCheck struct {
	FixableCheck
	missingSections []templates.TownRootRequiredSection
	staleMarkers    []templates.TownRootStaleMarker
	fileMissing     bool
}

// NewTownCLAUDEmdCheck creates a new town-root CLAUDE.md version check.
func NewTownCLAUDEmdCheck() *TownCLAUDEmdCheck {
	return &TownCLAUDEmdCheck{
		FixableCheck: FixableCheck{
			BaseCheck: BaseCheck{
				CheckName:        "town-claude-md",
				CheckDescription: "Verify town-root CLAUDE.md is up to date with embedded version",
				CheckCategory:    CategoryConfig,
			},
		},
	}
}

// Run checks the town-root CLAUDE.md for completeness.
func (c *TownCLAUDEmdCheck) Run(ctx *CheckContext) *CheckResult {
	c.missingSections = nil
	c.staleMarkers = nil
	c.fileMissing = false

	claudePath := filepath.Join(ctx.TownRoot, "CLAUDE.md")

	// Check if file exists
	data, err := os.ReadFile(claudePath)
	if err != nil {
		if os.IsNotExist(err) {
			c.fileMissing = true
			return &CheckResult{
				Name:    c.Name(),
				Status:  StatusError,
				Message: "Town-root CLAUDE.md is missing",
				FixHint: "Run 'gt doctor --fix' to create it from embedded template",
			}
		}
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusError,
			Message: fmt.Sprintf("Cannot read town-root CLAUDE.md: %v", err),
		}
	}

	content := string(data)

	// Check for required sections
	required := templates.TownRootRequiredSections()
	var missing []templates.TownRootRequiredSection
	var details []string

	for _, section := range required {
		if !strings.Contains(content, section.Heading) {
			missing = append(missing, section)
			details = append(details, fmt.Sprintf("Missing: %s (%s)", section.Name, section.Heading))
		}
	}

	// Check for stale guidance from older template revisions. Presence-only
	// checks pass on outdated content, and agents follow whatever is written
	// (hq-oxyjcj: stale Dolt diagnostics guidance derailed an outage RCA).
	var stale []templates.TownRootStaleMarker
	for _, marker := range templates.TownRootStaleMarkers() {
		if strings.Contains(content, marker.Marker) {
			stale = append(stale, marker)
			details = append(details, fmt.Sprintf("Stale: %s (contains %q)", marker.Name, marker.Marker))
		}
	}

	if len(missing) == 0 && len(stale) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "Town-root CLAUDE.md has all required sections",
		}
	}

	c.missingSections = missing
	c.staleMarkers = stale

	var problems []string
	if len(missing) > 0 {
		problems = append(problems, fmt.Sprintf("missing %d section(s)", len(missing)))
	}
	if len(stale) > 0 {
		problems = append(problems, fmt.Sprintf("%d stale section(s)", len(stale)))
	}

	return &CheckResult{
		Name:    c.Name(),
		Status:  StatusWarning,
		Message: fmt.Sprintf("Town-root CLAUDE.md %s", strings.Join(problems, ", ")),
		Details: details,
		FixHint: "Run 'gt doctor --fix' to update from embedded template",
	}
}

// Fix updates the town-root CLAUDE.md with missing sections from the
// embedded template while preserving user customizations.
func (c *TownCLAUDEmdCheck) Fix(ctx *CheckContext) error {
	claudePath := filepath.Join(ctx.TownRoot, "CLAUDE.md")
	canonical := templates.TownRootCLAUDEmd()

	// If file is missing, create it from the canonical template
	if c.fileMissing {
		return os.WriteFile(claudePath, []byte(canonical), 0644)
	}

	if len(c.missingSections) == 0 && len(c.staleMarkers) == 0 {
		return nil
	}

	// Read current content
	data, err := os.ReadFile(claudePath)
	if err != nil {
		return fmt.Errorf("reading CLAUDE.md: %w", err)
	}
	current := string(data)

	// Replace sections containing stale guidance with their canonical
	// counterparts before appending anything missing.
	if len(c.staleMarkers) > 0 {
		current = replaceStaleSections(current, canonical, c.staleMarkers)
	}

	// Parse canonical content into H2 sections
	canonicalSections := parseH2Sections(canonical)

	// For each missing section, find it in the canonical and append
	var toAppend strings.Builder
	for _, missing := range c.missingSections {
		// Find the H2 section that contains this heading
		for _, cs := range canonicalSections {
			if strings.Contains(cs.content, missing.Heading) {
				toAppend.WriteString("\n")
				toAppend.WriteString(cs.content)
				break
			}
		}
	}

	if toAppend.Len() == 0 && len(c.staleMarkers) == 0 {
		return nil
	}

	// Ensure current content ends with a newline before appending
	if !strings.HasSuffix(current, "\n") {
		current += "\n"
	}

	updated := current + toAppend.String()
	return os.WriteFile(claudePath, []byte(updated), 0644)
}

// replaceStaleSections rewrites each H2 section that contains a known-stale
// marker with the canonical section it maps to. Duplicate stale copies of the
// same section (left behind by earlier append-only fixes) collapse into a
// single canonical one. Sections without stale markers pass through untouched.
func replaceStaleSections(current, canonical string, markers []templates.TownRootStaleMarker) string {
	canonicalSections := parseH2Sections(canonical)
	replaced := make(map[string]bool)
	var out strings.Builder

	for _, sec := range parseH2Sections(current) {
		// Never replace the preamble (content before the first H2).
		if sec.heading == "" {
			out.WriteString(sec.content)
			continue
		}
		var replacement *h2Section
		for _, m := range markers {
			if !strings.Contains(sec.content, m.Marker) {
				continue
			}
			for i := range canonicalSections {
				if strings.HasPrefix(canonicalSections[i].heading, m.Heading) {
					replacement = &canonicalSections[i]
					break
				}
			}
			break
		}
		if replacement == nil {
			out.WriteString(sec.content)
			continue
		}
		if replaced[replacement.heading] {
			continue // drop duplicate stale copy of an already-replaced section
		}
		replaced[replacement.heading] = true
		out.WriteString(replacement.content)
	}

	return out.String()
}

// h2Section represents a section of markdown delimited by H2 headings.
type h2Section struct {
	heading string // The H2 heading line (e.g., "## Dolt Server — Operational Awareness")
	content string // Full section content including the heading and all sub-content
}

// parseH2Sections splits markdown content into sections by H2 headings.
// The preamble (content before the first H2) is returned as a section with
// an empty heading.
func parseH2Sections(content string) []h2Section {
	var sections []h2Section
	lines := strings.Split(content, "\n")

	var currentHeading string
	var currentContent strings.Builder
	inSection := false

	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			// Save previous section
			if inSection || currentContent.Len() > 0 {
				sections = append(sections, h2Section{
					heading: currentHeading,
					content: currentContent.String(),
				})
			}
			// Start new section
			currentHeading = line
			currentContent.Reset()
			currentContent.WriteString(line)
			currentContent.WriteString("\n")
			inSection = true
		} else {
			currentContent.WriteString(line)
			currentContent.WriteString("\n")
		}
	}

	// Save final section
	if currentContent.Len() > 0 {
		sections = append(sections, h2Section{
			heading: currentHeading,
			content: currentContent.String(),
		})
	}

	return sections
}
