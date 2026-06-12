package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// GitDiff collects changed files for the review range.
// base selection: explicit --base, else merge-base with origin default branch,
// else main/master, else HEAD (working tree changes only).
func GitDiff(repoRoot, base string) ([]DiffFile, string, error) {
	resolved := resolveBase(repoRoot, base)

	// unborn HEAD (fresh repo, no commits): everything is staged/untracked
	if _, err := git(repoRoot, "rev-parse", "--verify", "HEAD"); err != nil {
		var out []DiffFile
		staged, _ := git(repoRoot, "diff", "--cached", "--numstat")
		for _, ln := range strings.Split(staged, "\n") {
			parts := strings.Split(ln, "\t")
			if len(parts) < 3 {
				continue
			}
			add, _ := strconv.Atoi(parts[0])
			del, _ := strconv.Atoi(parts[1])
			out = append(out, DiffFile{Path: parts[len(parts)-1], Status: "A", Added: add, Deleted: del})
		}
		untracked, _ := git(repoRoot, "ls-files", "--others", "--exclude-standard")
		for _, p := range strings.Split(untracked, "\n") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, DiffFile{Path: p, Status: "A"})
			}
		}
		return out, "WORKTREE(no commits)", nil
	}

	var args []string
	if resolved == "WORKTREE" {
		args = []string{"diff", "HEAD", "--numstat", "-M"}
	} else {
		args = []string{"diff", resolved + "...HEAD", "--numstat", "-M"}
	}
	numstat, err := git(repoRoot, args...)
	if err != nil && resolved != "WORKTREE" {
		// triple-dot can fail on shallow/unrelated histories; degrade to two-dot
		numstat, err = git(repoRoot, "diff", resolved, "--numstat", "-M")
	}
	if err != nil {
		return nil, resolved, fmt.Errorf("git diff: %w", err)
	}

	statusArgs := make([]string, len(args))
	copy(statusArgs, args)
	for i := range statusArgs {
		if statusArgs[i] == "--numstat" {
			statusArgs[i] = "--name-status"
		}
	}
	nameStatus, _ := git(repoRoot, statusArgs...)
	status := map[string]string{}
	for _, ln := range strings.Split(nameStatus, "\n") {
		parts := strings.Split(ln, "\t")
		if len(parts) >= 2 {
			st := parts[0]
			path := parts[len(parts)-1] // renames: old\tnew → take new
			status[path] = string(st[0])
		}
	}

	var out []DiffFile
	for _, ln := range strings.Split(numstat, "\n") {
		parts := strings.Split(ln, "\t")
		if len(parts) < 3 {
			continue
		}
		add, _ := strconv.Atoi(parts[0]) // "-" (binary) parses to 0
		del, _ := strconv.Atoi(parts[1])
		path := parts[len(parts)-1]
		st := status[path]
		if st == "" {
			st = "M"
		}
		out = append(out, DiffFile{Path: path, Status: st, Added: add, Deleted: del})
	}

	// include untracked files when reviewing the worktree
	if resolved == "WORKTREE" {
		untracked, _ := git(repoRoot, "ls-files", "--others", "--exclude-standard")
		for _, p := range strings.Split(untracked, "\n") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, DiffFile{Path: p, Status: "A"})
			}
		}
	}
	return out, resolved, nil
}

func resolveBase(repoRoot, base string) string {
	if base != "" {
		if base == "worktree" {
			return "WORKTREE"
		}
		return base
	}
	// origin's default branch
	if head, err := git(repoRoot, "symbolic-ref", "refs/remotes/origin/HEAD"); err == nil {
		ref := strings.TrimSpace(head) // refs/remotes/origin/main
		if ref != "" {
			if mb, err := git(repoRoot, "merge-base", "HEAD", ref); err == nil && strings.TrimSpace(mb) != "" {
				if h, _ := git(repoRoot, "rev-parse", "HEAD"); strings.TrimSpace(mb) != strings.TrimSpace(h) {
					return strings.TrimSpace(ref)
				}
			}
		}
	}
	for _, b := range []string{"main", "master"} {
		if _, err := git(repoRoot, "rev-parse", "--verify", b); err == nil {
			if h, _ := git(repoRoot, "rev-parse", "HEAD"); func() bool {
				mb, err := git(repoRoot, "merge-base", "HEAD", b)
				return err == nil && strings.TrimSpace(mb) != strings.TrimSpace(h)
			}() && strings.TrimSpace(h) != "" {
				return b
			}
		}
	}
	return "WORKTREE"
}

// RepoRoot returns the git toplevel for a directory ("" if not a repo).
func RepoRoot(dir string) string {
	out, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	b, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(b), nil
}
