package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

var tapGuardGitStashCmd = &cobra.Command{
	Use:   "git-stash",
	Short: "Block index-based git stash operations in multi-worktree repos",
	Long: `Block index-based git stash operations via Claude Code PreToolUse hooks.

Git keeps the stash stack in the COMMON .git directory, so every worktree of a
repo shares ONE stack. In a rig with ten polecat worktrees, "git stash pop"
pops whatever landed on top of the stack — which may be another agent's work.
That silently moves their files into your tree and deletes them from theirs.

This guard blocks the operations that address the shared stack by position:

  git stash pop / apply stash@{0} / drop / clear

and requires a label when creating an entry, so a stash can always be traced
back to the agent that made it:

  git stash push -m "<agent>/<issue> <what>"

Read-only and index-free operations are always allowed:

  git stash list / show / create / store, git stash apply <sha>

The guard is a no-op when the repo has a single worktree — a solo repo has no
sharing hazard.

The guard reads the tool input from stdin (Claude Code hook protocol)
and exits with code 2 to block.

Exit codes:
  0 - Operation allowed
  2 - Operation BLOCKED`,
	RunE: runTapGuardGitStash,
}

func init() {
	tapGuardCmd.AddCommand(tapGuardGitStashCmd)
}

// stashSHAPattern matches an explicit commit object (abbreviated or full).
// A SHA names one stash commit directly and is immune to the stack shifting
// under a concurrent agent, so "git stash apply <sha>" stays allowed.
var stashSHAPattern = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

func runTapGuardGitStash(cmd *cobra.Command, args []string) error {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil // fail open
	}

	command := extractCommand(input)
	if command == "" {
		return nil
	}

	sub, subArgs, ok := parseGitStashCommand(command)
	if !ok {
		return nil
	}

	// A single-worktree repo has no shared-stack hazard: the stash stack
	// belongs to whoever is standing in it.
	if !sharesStashStack(extractHookCwd(input)) {
		return nil
	}

	reason, fix := stashViolation(sub, subArgs)
	if reason == "" {
		return nil
	}

	printStashBlock(command, reason, fix)
	return NewSilentExit(2)
}

// parseGitStashCommand finds a "git stash" invocation in the command line and
// returns its subcommand plus that subcommand's arguments. A bare "git stash"
// means "git stash push", which is how git itself reads it.
func parseGitStashCommand(command string) (sub string, subArgs []string, ok bool) {
	fields := strings.Fields(command)
	for i, f := range fields {
		if f != "stash" {
			continue
		}
		// Walk back over git's global flags ("git -C <dir> stash") to confirm
		// this "stash" is a git subcommand and not, say, a directory named stash.
		if !precededByGit(fields[:i]) {
			continue
		}
		rest := fields[i+1:]
		for j, a := range rest {
			if strings.HasPrefix(a, "-") {
				continue
			}
			return a, rest[j+1:], true
		}
		return "push", nil, true // bare "git stash" == "git stash push"
	}
	return "", nil, false
}

// precededByGit reports whether the tokens before a "stash" token end in a git
// invocation, allowing for global flags in between ("git -C /path stash").
func precededByGit(before []string) bool {
	for i := len(before) - 1; i >= 0; i-- {
		tok := before[i]
		if tok == "git" {
			return true
		}
		if strings.HasPrefix(tok, "-") {
			continue // global flag
		}
		if i > 0 && strings.HasPrefix(before[i-1], "-") {
			continue // value of a global flag, e.g. the path in "-C /path"
		}
		return false
	}
	return false
}

// stashViolation returns the reason a stash subcommand is unsafe on a shared
// stack, plus the command to run instead. An empty reason means "allowed".
func stashViolation(sub string, args []string) (reason, fix string) {
	const findSHA = "find your SHA with: git stash list --format='%H %gs'"

	switch sub {
	case "pop":
		return "git stash pop takes the top of a stack shared by every worktree — it can steal another agent's entry",
			"git stash apply <sha> — " + findSHA

	case "drop":
		return "git stash drop deletes by stack position — the entry at that position may be another agent's",
			"leave the entry in place; a labelled stash costs nothing, and it may be another agent's only copy"

	case "clear":
		return "git stash clear destroys the stash stack of EVERY worktree in this repo",
			"leave the stack alone — it is not yours to clear"

	case "apply", "branch":
		// Both take an optional trailing stash ref. "branch" takes the new
		// branch name first, so drop that before looking for the ref.
		refArgs := positionalArgs(args)
		if sub == "branch" && len(refArgs) > 0 {
			refArgs = refArgs[1:]
		}
		// A SHA names one stash commit and is immune to the stack shifting.
		if len(refArgs) > 0 && stashSHAPattern.MatchString(refArgs[0]) {
			return "", ""
		}
		if len(refArgs) > 0 {
			return fmt.Sprintf("%q addresses the shared stack by position, which shifts when another agent stashes", refArgs[0]),
				"git stash " + sub + " <sha> — " + findSHA
		}
		return "bare 'git stash " + sub + "' takes the top of a stack shared by every worktree",
			"git stash " + sub + " <sha> — " + findSHA

	case "push", "save":
		// Creating an entry is safe; creating an *anonymous* entry is not,
		// because nobody can tell later whose it was. "save" also takes its
		// message positionally, where "push" reads positionals as pathspecs.
		if hasStashMessage(args) || (sub == "save" && len(positionalArgs(args)) > 0) {
			return "", ""
		}
		return "an unlabelled stash is indistinguishable from another agent's on the shared stack",
			fmt.Sprintf("git stash push -m %q", stashLabelHint())
	}

	// list, show, create, store: read-only or index-free.
	return "", ""
}

// positionalArgs drops flags, keeping the operands in order.
func positionalArgs(args []string) []string {
	var out []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		out = append(out, a)
	}
	return out
}

// hasStashMessage reports whether a push/save carries -m/--message.
func hasStashMessage(args []string) bool {
	for _, a := range args {
		if a == "-m" || a == "--message" || strings.HasPrefix(a, "--message=") {
			return true
		}
		// Bundled short flags, e.g. -um "msg".
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "m") {
			return true
		}
	}
	return false
}

// stashLabelHint builds a suggested stash label from the agent's identity so
// the blocked command can be retried by copy-paste.
func stashLabelHint() string {
	role := os.Getenv("GT_ROLE")
	if role == "" {
		role = "<agent>"
	}
	return role + " <issue-id> <what you stashed>"
}

// sharesStashStack reports whether the repo at dir has more than one worktree,
// which is exactly when the stash stack is shared. It fails open (false) so a
// git error never blocks the agent.
func sharesStashStack(dir string) bool {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	count := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			count++
		}
	}
	return count > 1
}

// extractHookCwd pulls the working directory Claude Code reports for the tool
// call, so the guard inspects the repo the command would actually run in.
func extractHookCwd(input []byte) string {
	var hookInput struct {
		Cwd string `json:"cwd"`
	}
	if err := json.Unmarshal(input, &hookInput); err != nil {
		return ""
	}
	return hookInput.Cwd
}

func printStashBlock(command, reason, fix string) {
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "╔══════════════════════════════════════════════════════════════════╗")
	fmt.Fprintln(os.Stderr, "║  ❌ SHARED STASH STACK — COMMAND BLOCKED                         ║")
	fmt.Fprintln(os.Stderr, "╚══════════════════════════════════════════════════════════════════╝")
	fmt.Fprintf(os.Stderr, "  Command: %s\n", command)
	fmt.Fprintf(os.Stderr, "  Reason:  %s\n", reason)
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  Every worktree of this repo shares ONE stash stack (git keeps it in")
	fmt.Fprintln(os.Stderr, "  the common .git dir). Other agents are pushing to it right now.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintf(os.Stderr, "  Do this instead:\n    %s\n", fix)
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  Better: skip the stash. To prove a test fails without your fix, park")
	fmt.Fprintln(os.Stderr, "  the fix in a worktree-local patch file — nothing shared is touched:")
	fmt.Fprintln(os.Stderr, "    git diff -- <src> > /tmp/fix.patch && git checkout -- <src>")
	fmt.Fprintln(os.Stderr, "    <run the test — confirm RED> && git apply /tmp/fix.patch")
	fmt.Fprintln(os.Stderr, "")
}
