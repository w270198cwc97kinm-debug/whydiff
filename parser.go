package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// rawEntry mirrors the subset of Claude Code transcript JSONL we consume.
// The format is internal to Claude Code; parse defensively and skip unknowns.
type rawEntry struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	SessionID string          `json:"sessionId"`
	Version   string          `json:"version"`
	GitBranch string          `json:"gitBranch"`
	CWD       string          `json:"cwd"`
	Sidechain bool            `json:"isSidechain"`
	AITitle   string          `json:"aiTitle"`
	Message   json.RawMessage `json:"message"`
}

type rawMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type rawBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   *bool           `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

type rawToolInput struct {
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	Command      string `json:"command"`
	Description  string `json:"description"`
}

const maxSnippet = 400

// ParseSession reads one transcript JSONL into a Session (main chain only).
func ParseSession(path string) (*Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	s := &Session{Path: path}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e rawEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // unknown/corrupt line: skip, never fail the whole report
		}
		if e.AITitle != "" {
			s.Title = e.AITitle // keep last title
		}
		if e.SessionID != "" && s.ID == "" {
			s.ID = e.SessionID
		}
		if e.Version != "" {
			s.Version = e.Version
		}
		if e.GitBranch != "" && s.GitBranch == "" {
			s.GitBranch = e.GitBranch
		}
		if e.CWD != "" && s.CWD == "" {
			s.CWD = e.CWD
		}
		if e.Type != "user" && e.Type != "assistant" {
			continue
		}
		if e.Sidechain {
			s.Sidechain++
			continue
		}
		ts := parseTS(e.Timestamp)
		if !ts.IsZero() {
			if s.Start.IsZero() || ts.Before(s.Start) {
				s.Start = ts
			}
			if ts.After(s.End) {
				s.End = ts
			}
		}
		var msg rawMessage
		if err := json.Unmarshal(e.Message, &msg); err != nil {
			continue
		}
		switch e.Type {
		case "user":
			parseUser(s, msg, ts)
		case "assistant":
			parseAssistant(s, msg, ts)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	return s, nil
}

func parseUser(s *Session, msg rawMessage, ts time.Time) {
	// content is either a plain string (real user prompt)
	// or a block list (tool_result blocks, sometimes with text).
	var str string
	if err := json.Unmarshal(msg.Content, &str); err == nil {
		if t := strings.TrimSpace(str); t != "" && !isNoise(t) {
			s.Events = append(s.Events, Event{Kind: EvUserPrompt, Timestamp: ts, Prompt: t})
		}
		return
	}
	var blocks []rawBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return
	}
	for _, b := range blocks {
		switch b.Type {
		case "tool_result":
			s.Events = append(s.Events, Event{
				Kind:          EvToolResult,
				Timestamp:     ts,
				ForToolID:     b.ToolUseID,
				IsError:       b.IsError != nil && *b.IsError,
				ResultSnippet: snippetFromContent(b.Content),
			})
		case "text":
			if t := strings.TrimSpace(b.Text); t != "" && !isNoise(t) {
				s.Events = append(s.Events, Event{Kind: EvUserPrompt, Timestamp: ts, Prompt: t})
			}
		}
	}
}

func parseAssistant(s *Session, msg rawMessage, ts time.Time) {
	var blocks []rawBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if t := strings.TrimSpace(b.Text); t != "" {
				s.Events = append(s.Events, Event{Kind: EvAssistantText, Timestamp: ts, Text: t})
			}
		case "tool_use":
			var in rawToolInput
			_ = json.Unmarshal(b.Input, &in)
			file := in.FilePath
			if file == "" {
				file = in.NotebookPath
			}
			s.Events = append(s.Events, Event{
				Kind:      EvToolUse,
				Timestamp: ts,
				ToolID:    b.ID,
				ToolName:  b.Name,
				FilePath:  file,
				Command:   in.Command,
				Desc:      in.Description,
			})
		}
	}
}

// snippetFromContent extracts a short text snippet from a tool_result content,
// which may be a plain string or a block list.
func snippetFromContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		return clip(str, maxSnippet)
	}
	var blocks []rawBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var sb strings.Builder
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				if sb.Len() > 0 {
					sb.WriteString(" ")
				}
				sb.WriteString(b.Text)
				if sb.Len() > maxSnippet {
					break
				}
			}
		}
		return clip(sb.String(), maxSnippet)
	}
	return ""
}

// isNoise filters harness-injected user content that is not a human prompt.
func isNoise(t string) bool {
	for _, p := range []string{
		"<system-reminder>", "<task-notification>", "<local-command-caveat>",
		"<command-name>", "<command-message>", "<local-command-stdout>", "Caveat:",
		"Base directory for this skill:", "[Request interrupted",
	} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

func parseTS(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return time.Time{}
		}
	}
	return t
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	// cut on a rune boundary
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// SessionInfo is a lightweight listing of one transcript.
type SessionInfo struct {
	Path     string
	Modified time.Time
	SizeKB   int64
	Title    string
}

// ListSessions returns transcripts for a repo path, newest first.
func ListSessions(repoRoot string) ([]SessionInfo, error) {
	dir, err := projectTranscriptDir(repoRoot)
	if err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []SessionInfo
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, SessionInfo{
			Path:     dir + "/" + e.Name(),
			Modified: fi.ModTime(),
			SizeKB:   fi.Size() / 1024,
			Title:    quickTitle(dir + "/" + e.Name()),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out, nil
}

// projectTranscriptDir maps a repo path to Claude Code's transcript directory:
// every character outside [A-Za-z0-9-] becomes '-', prefixed by '-'.
func projectTranscriptDir(repoRoot string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, r := range repoRoot {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			sb.WriteRune(r)
		} else {
			sb.WriteByte('-')
		}
	}
	return home + "/.claude/projects/" + sb.String(), nil
}

// quickTitle scans a transcript tail-first for its latest ai-title.
func quickTitle(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	title := ""
	for sc.Scan() {
		line := sc.Bytes()
		if !strings.Contains(string(line), `"ai-title"`) {
			continue
		}
		var e rawEntry
		if json.Unmarshal(line, &e) == nil && e.AITitle != "" {
			title = e.AITitle
		}
	}
	return title
}
