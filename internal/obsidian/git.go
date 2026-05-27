// Package obsidian provides shared helpers for the obsidian vault git
// repo. Subpackages (lists, property) call Cmd / CommitAndPush to
// pull/add/commit/push their writes.
package obsidian

import (
	"fmt"
	"os/exec"
	"strings"
)

var repo = "/home/jcgregorio/obsidian"

// SetRepo points the helpers at a non-default vault path. Called from
// cmd/myjarvis when OBSIDIAN_REPO is set in the environment.
func SetRepo(path string) { repo = path }

// Repo returns the current vault repo path (mostly for diagnostics/tests).
func Repo() string { return repo }

// Cmd runs `git <args...>` inside the configured vault repo, returning
// any error along with the trimmed combined output.
func Cmd(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return nil
}

// CommitAndPush adds relPath (if non-empty), commits with the given
// message, and pushes. Mirrors the pull-before-write pattern used by
// the lists and property packages.
func CommitAndPush(relPath, commitMsg string) error {
	if relPath != "" {
		if err := Cmd("add", relPath); err != nil {
			return fmt.Errorf("git add: %w", err)
		}
	}
	if err := Cmd("commit", "-m", commitMsg); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}
	if err := Cmd("push"); err != nil {
		return fmt.Errorf("git push: %w", err)
	}
	return nil
}
