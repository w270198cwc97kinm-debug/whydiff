// whydiff — make agent PRs reviewable.
//
// Reconstructs a coding-agent session (intent → decisions → actions → verification)
// and correlates it with the git diff, so a human can review *why* the changes
// were made — not just what changed. Local-first; no network, no API keys.
//
// Usage:
//
//	whydiff                         # newest session for this repo + auto diff base
//	whydiff list                    # list sessions for this repo
//	whydiff --session <path>        # specific transcript
//	whydiff --base main             # explicit diff base (ref) | "worktree"
//	whydiff --md out.md             # write markdown (paste into your PR)
//	whydiff --html out.html         # write self-contained HTML report
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"time"
)

const version = "0.1.0"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "list" {
		runList()
		return
	}

	fs := flag.NewFlagSet("whydiff", flag.ExitOnError)
	sessionPath := fs.String("session", "", "transcript .jsonl path (default: newest for this repo)")
	base := fs.String("base", "", `diff base ref (default: auto; "worktree" = uncommitted changes vs HEAD)`)
	mdOut := fs.String("md", "", "write markdown report to file (- for stdout)")
	htmlOut := fs.String("html", "", "write self-contained HTML report to file")
	repo := fs.String("repo", ".", "repository path")
	showVersion := fs.Bool("version", false, "print version")
	fs.Parse(os.Args[1:])

	if *showVersion {
		fmt.Println("whydiff", version)
		return
	}

	root := RepoRoot(*repo)
	if root == "" {
		fatal("not a git repository: %s", *repo)
	}

	// pick session
	path := *sessionPath
	if path == "" {
		sessions, err := ListSessions(root)
		if err != nil || len(sessions) == 0 {
			fatal("no Claude Code sessions found for %s (try --session <path>)", root)
		}
		path = sessions[0].Path
	}

	sess, err := ParseSession(path)
	if err != nil {
		fatal("parse session: %v", err)
	}
	if len(sess.Events) == 0 {
		fatal("session %s has no usable events", path)
	}

	exchanges, files, stats := BuildTimeline(sess, root)

	diff, resolvedBase, err := GitDiff(root, *base)
	if err != nil {
		fatal("%v", err)
	}
	// correlate diff <-> session
	storyByPath := map[string]*FileStory{}
	for _, f := range files {
		storyByPath[f.Path] = f
	}
	for i := range diff {
		if st, ok := storyByPath[diff[i].Path]; ok {
			diff[i].InSession = true
			st.InDiff = true
		}
	}
	// review priority: session-authored files first, then the rest, each alphabetical
	sort.SliceStable(diff, func(i, j int) bool {
		if diff[i].InSession != diff[j].InSession {
			return diff[i].InSession
		}
		return diff[i].Path < diff[j].Path
	})

	risks := AssessRisks(exchanges, files, diff, stats)

	report := &Report{
		Session: sess, RepoRoot: root, DiffBase: resolvedBase,
		Exchanges: exchanges, Files: files, Diff: diff, Risks: risks,
		Stats: stats, GeneratedAt: time.Now(),
	}

	wrote := false
	if *mdOut != "" {
		md := RenderMarkdown(report)
		if *mdOut == "-" {
			fmt.Print(md)
		} else if err := os.WriteFile(*mdOut, []byte(md), 0o644); err != nil {
			fatal("write %s: %v", *mdOut, err)
		} else {
			fmt.Fprintf(os.Stderr, "wrote %s\n", *mdOut)
		}
		wrote = true
	}
	if *htmlOut != "" {
		if err := os.WriteFile(*htmlOut, []byte(RenderHTML(report)), 0o644); err != nil {
			fatal("write %s: %v", *htmlOut, err)
		}
		fmt.Fprintf(os.Stderr, "wrote %s\n", *htmlOut)
		wrote = true
	}
	if !wrote || *mdOut == "" && *htmlOut == "" {
		fmt.Print(RenderTerminal(report))
	}
}

func runList() {
	cwd, _ := os.Getwd()
	root := RepoRoot(cwd)
	if root == "" {
		fatal("not a git repository: %s", cwd)
	}
	sessions, err := ListSessions(root)
	if err != nil {
		fatal("list sessions: %v", err)
	}
	if len(sessions) == 0 {
		fmt.Println("no sessions found for", root)
		return
	}
	for _, s := range sessions {
		title := s.Title
		if title == "" {
			title = "(untitled)"
		}
		fmt.Printf("%s  %6dKB  %s\n    %s\n", s.Modified.Local().Format("2006-01-02 15:04"), s.SizeKB, title, s.Path)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "whydiff: "+format+"\n", args...)
	os.Exit(1)
}
