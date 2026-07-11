package templates

import (
	_ "embed"
	"strings"

	"github.com/steveyegge/gastown/internal/cli"
)

//go:embed townroot/claude.md
var townRootCLAUDEmdRaw string

// TownRootCLAUDEmdVersion is the version of the embedded town-root CLAUDE.md.
// Increment this when updating the template content with new sections.
const TownRootCLAUDEmdVersion = 2

// TownRootCLAUDEmd returns the canonical town-root CLAUDE.md content
// with the CLI command name substituted.
func TownRootCLAUDEmd() string {
	return strings.ReplaceAll(townRootCLAUDEmdRaw, "{{cmd}}", cli.Name())
}

// TownRootRequiredSection describes a section that must be present in the town-root CLAUDE.md.
type TownRootRequiredSection struct {
	Name    string // Human-readable name for reporting
	Heading string // The H2 or H3 heading to look for
}

// TownRootRequiredSections returns the key sections that must be present
// in the town-root CLAUDE.md for proper agent behavior.
func TownRootRequiredSections() []TownRootRequiredSection {
	return []TownRootRequiredSection{
		{
			Name:    "Dolt awareness",
			Heading: "## Dolt Server",
		},
		{
			Name:    "Communication hygiene",
			Heading: "### Communication hygiene",
		},
	}
}

// TownRootStaleMarker describes content from an older template revision whose
// presence marks a town-root CLAUDE.md section as dangerously out of date.
type TownRootStaleMarker struct {
	Name    string // Human-readable name for reporting
	Marker  string // Substring whose presence means the section is stale
	Heading string // Heading prefix of the canonical H2 section that replaces it
}

// TownRootStaleMarkers returns known-bad content patterns from older template
// versions. Sections containing these must be replaced with the canonical
// section, not merely supplemented: agents follow whatever guidance is present.
// (hq-oxyjcj: a stale town CLAUDE.md told agents to `kill -QUIT` Dolt — fatal
// on Dolt 1.86.5 — and pointed diagnostics at legacy .dolt-data/dolt.{pid,log}
// paths, deadending the 2026-07-10 outage investigation.)
func TownRootStaleMarkers() []TownRootStaleMarker {
	return []TownRootStaleMarker{
		{
			Name:    "kill -QUIT diagnostics (terminates Dolt >= 1.86.5)",
			Marker:  "kill -QUIT $(cat",
			Heading: "## Dolt Server",
		},
		{
			Name:    "legacy .dolt-data pid/log paths",
			Marker:  ".dolt-data/dolt.pid",
			Heading: "## Dolt Server",
		},
	}
}
