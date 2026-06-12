package main

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

var verifyPatterns = []struct {
	class string
	re    *regexp.Regexp
}{
	{"test", regexp.MustCompile(`(?i)(^|[ ;&|(])(go test|pytest|python -m pytest|npm (run )?test|yarn test|pnpm (run )?test|jest|vitest|cargo test|make test|rspec|phpunit|mvn test|gradle(w)? test|tox|bun test|deno test|ctest|swift test)\b`)},
	{"typecheck", regexp.MustCompile(`(?i)(^|[ ;&|(])(tsc\b|mypy|pyright|ty check|flow check)`)},
	{"lint", regexp.MustCompile(`(?i)(^|[ ;&|(])(ruff|eslint|biome (check|lint)|golangci-lint|staticcheck|clippy|cargo clippy|rubocop|flake8|pylint|oxlint)`)},
	{"build", regexp.MustCompile(`(?i)(^|[ ;&|(])(go build|go vet|cargo build|cargo check|npm run build|yarn build|pnpm build|make( |$)|mvn (package|compile)|gradle(w)? build|tsc --noEmit)`)},
}

// classifyAction determines what one tool call was.
func classifyAction(e Event) (ActionKind, string) {
	switch e.ToolName {
	case "Edit", "NotebookEdit":
		return ActEdit, ""
	case "Write":
		return ActWrite, ""
	case "Read":
		return ActRead, ""
	case "Glob", "Grep", "WebSearch", "WebFetch", "ToolSearch":
		return ActSearch, ""
	case "AskUserQuestion":
		return ActAsk, ""
	case "Bash":
		for _, p := range verifyPatterns {
			if p.re.MatchString(e.Command) {
				return ActVerify, p.class
			}
		}
		return ActCommand, ""
	default:
		return ActOther, ""
	}
}

// BuildTimeline turns the flat event list into exchanges/decisions and joins results.
func BuildTimeline(s *Session, repoRoot string) ([]Exchange, []*FileStory, Stats) {
	// index tool results by tool id
	results := map[string]Event{}
	for _, e := range s.Events {
		if e.Kind == EvToolResult {
			results[e.ForToolID] = e
		}
	}

	var exchanges []Exchange
	var cur *Exchange
	var curDec *Decision
	stats := Stats{SubagentEvents: s.Sidechain}

	flushDec := func() {
		if curDec != nil && cur != nil {
			cur.Decisions = append(cur.Decisions, *curDec)
		}
		curDec = nil
	}
	flushEx := func() {
		flushDec()
		if cur != nil {
			exchanges = append(exchanges, *cur)
		}
		cur = nil
	}

	for _, e := range s.Events {
		switch e.Kind {
		case EvUserPrompt:
			flushEx()
			cur = &Exchange{Prompt: e.Prompt, Timestamp: e.Timestamp}
			stats.UserPrompts++
		case EvAssistantText:
			if cur == nil { // session resumed mid-way; synthesize an exchange
				cur = &Exchange{Prompt: "(session resumed)", Timestamp: e.Timestamp}
			}
			flushDec()
			curDec = &Decision{Rationale: e.Text, Timestamp: e.Timestamp}
			stats.Decisions++
		case EvToolUse:
			if cur == nil {
				cur = &Exchange{Prompt: "(session resumed)", Timestamp: e.Timestamp}
			}
			if curDec == nil {
				curDec = &Decision{Rationale: "", Timestamp: e.Timestamp}
			}
			kind, vclass := classifyAction(e)
			a := Action{
				Kind:        kind,
				Tool:        e.ToolName,
				File:        relPath(e.FilePath, repoRoot),
				Command:     e.Command,
				Desc:        e.Desc,
				Timestamp:   e.Timestamp,
				VerifyClass: vclass,
			}
			if r, ok := results[e.ToolID]; ok {
				a.HasResult = true
				a.IsError = r.IsError
				a.ResultSnippet = r.ResultSnippet
			}
			curDec.Actions = append(curDec.Actions, a)
			stats.ToolCalls++
			switch kind {
			case ActEdit:
				stats.EditCount++
			case ActWrite:
				stats.WriteCount++
			case ActCommand:
				stats.CommandsRun++
			case ActVerify:
				stats.VerifyRuns++
				if a.IsError {
					stats.VerifyFailed++
				}
			}
			if a.IsError {
				stats.ToolErrors++
			}
		}
	}
	flushEx()

	// file stories
	stories := map[string]*FileStory{}
	lastVerifyOK := time.Time{}
	// first pass: find last successful verify time
	for _, ex := range exchanges {
		for _, d := range ex.Decisions {
			for _, a := range d.Actions {
				if a.Kind == ActVerify && a.HasResult && !a.IsError && a.Timestamp.After(lastVerifyOK) {
					lastVerifyOK = a.Timestamp
				}
			}
		}
	}
	for _, ex := range exchanges {
		for _, d := range ex.Decisions {
			for _, a := range d.Actions {
				if (a.Kind == ActEdit || a.Kind == ActWrite) && a.File != "" {
					st, ok := stories[a.File]
					if !ok {
						st = &FileStory{Path: a.File, FirstTouch: a.Timestamp}
						stories[a.File] = st
					}
					if a.Kind == ActEdit {
						st.Edits++
					} else {
						st.Writes++
					}
					if a.Timestamp.After(st.LastTouch) {
						st.LastTouch = a.Timestamp
					}
					if a.Timestamp.Before(st.FirstTouch) {
						st.FirstTouch = a.Timestamp
					}
					if r := firstLine(d.Rationale); r != "" && !containsStr(st.Rationales, r) {
						st.Rationales = append(st.Rationales, r)
					}
				}
			}
		}
	}
	for _, st := range stories {
		st.VerifiedAfter = !lastVerifyOK.IsZero() && !st.LastTouch.After(lastVerifyOK)
	}
	var fileList []*FileStory
	for _, st := range stories {
		fileList = append(fileList, st)
	}
	sort.Slice(fileList, func(i, j int) bool { return fileList[i].Path < fileList[j].Path })
	stats.FilesTouched = len(fileList)
	if !s.Start.IsZero() && !s.End.IsZero() {
		stats.DurationMin = s.End.Sub(s.Start).Minutes()
	}
	return exchanges, fileList, stats
}

func relPath(p, root string) string {
	if p == "" {
		return ""
	}
	if root != "" && strings.HasPrefix(p, root+"/") {
		return strings.TrimPrefix(p, root+"/")
	}
	return p
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return clip(s, 160)
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
