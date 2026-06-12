package main

import (
	"fmt"
	"sort"
	"strings"
)

// RenderMarkdown produces a PR-ready review document.
func RenderMarkdown(r *Report) string {
	var b strings.Builder
	title := r.Session.Title
	if title == "" {
		title = r.Session.ID
	}
	fmt.Fprintf(&b, "# recap — %s\n\n", title)
	fmt.Fprintf(&b, "> Agent session reconstructed for review by `recap` — intent → decisions → actions → verification.\n\n")

	// headline
	fmt.Fprintf(&b, "**session**: `%s`  \n", r.Session.ID)
	if !r.Session.Start.IsZero() {
		endFmt := "15:04"
		if r.Session.End.Local().YearDay() != r.Session.Start.Local().YearDay() {
			endFmt = "2006-01-02 15:04"
		}
		fmt.Fprintf(&b, "**span**: %s – %s (%.0f min)  \n",
			r.Session.Start.Local().Format("2006-01-02 15:04"),
			r.Session.End.Local().Format(endFmt), r.Stats.DurationMin)
	}
	fmt.Fprintf(&b, "**diff base**: `%s`  \n", r.DiffBase)
	fmt.Fprintf(&b, "**stats**: %d prompts / %d decisions / %d tool calls / %d files / verify %d (fail %d) / errors %d / subagent events %d\n\n",
		r.Stats.UserPrompts, r.Stats.Decisions, r.Stats.ToolCalls, r.Stats.FilesTouched,
		r.Stats.VerifyRuns, r.Stats.VerifyFailed, r.Stats.ToolErrors, r.Stats.SubagentEvents)

	// risks first: reviewers triage here
	fmt.Fprintf(&b, "## ⚠ Risks (%d)\n\n", len(r.Risks))
	if len(r.Risks) == 0 {
		b.WriteString("None detected.\n\n")
	} else {
		rs := append([]Risk(nil), r.Risks...)
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].Severity > rs[j].Severity })
		b.WriteString("| Sev | Rule | Subject | Detail |\n|---|---|---|---|\n")
		for _, k := range rs {
			fmt.Fprintf(&b, "| %s | %s | `%s` | %s |\n", k.Severity, k.Rule, mdEscape(clip(k.Subject, 80)), mdEscape(k.Detail))
		}
		b.WriteString("\n")
	}

	// intent
	b.WriteString("## Intent (what was asked)\n\n")
	n := 0
	for _, ex := range r.Exchanges {
		if ex.Prompt == "(session resumed)" {
			continue
		}
		n++
		fmt.Fprintf(&b, "%d. %s\n", n, mdEscape(firstLine(ex.Prompt)))
	}
	b.WriteString("\n")

	// file stories — the core review view (session-authored first, others folded)
	var authored, others []DiffFile
	for _, df := range r.Diff {
		if findStory(r.Files, df.Path) != nil {
			authored = append(authored, df)
		} else {
			others = append(others, df)
		}
	}
	fmt.Fprintf(&b, "## Changed files × why (%d in diff, %d from this session)\n\n", len(r.Diff), len(authored))
	for _, df := range authored {
		st := findStory(r.Files, df.Path)
		fmt.Fprintf(&b, "### 🤖 `%s` (%s %s)\n\n", df.Path, df.Status, diffStat(df))
		verified := "❌ not verified after last edit"
		if st.VerifiedAfter {
			verified = "✅ verified after last edit"
		}
		fmt.Fprintf(&b, "- %d edits / %d writes — %s\n", st.Edits, st.Writes, verified)
		if len(st.Rationales) > 0 {
			b.WriteString("- decision trail:\n")
			for _, ra := range st.Rationales {
				fmt.Fprintf(&b, "  - %s\n", mdEscape(ra))
			}
		}
		b.WriteString("\n")
	}
	if len(others) > 0 {
		fmt.Fprintf(&b, "<details><summary>✋ %d files in the diff were <b>not</b> written by this session (human edits, command side-effects, or another session)</summary>\n\n", len(others))
		for _, df := range others {
			fmt.Fprintf(&b, "- `%s` (%s %s)\n", df.Path, df.Status, diffStat(df))
		}
		b.WriteString("\n</details>\n\n")
	}

	// timeline (collapsed: one line per decision)
	b.WriteString("## Timeline (decisions → actions)\n\n")
	for _, ex := range r.Exchanges {
		fmt.Fprintf(&b, "<details><summary><b>👤 %s</b></summary>\n\n", mdEscape(firstLine(ex.Prompt)))
		for _, d := range ex.Decisions {
			if d.Rationale != "" {
				fmt.Fprintf(&b, "- 🧠 %s\n", mdEscape(firstLine(d.Rationale)))
			}
			for _, a := range d.Actions {
				fmt.Fprintf(&b, "  - %s\n", mdEscape(actionLine(a)))
			}
		}
		b.WriteString("\n</details>\n\n")
	}

	return b.String()
}

func actionLine(a Action) string {
	status := ""
	if a.HasResult && a.IsError {
		status = " ✖"
	}
	switch a.Kind {
	case ActEdit:
		return fmt.Sprintf("✏️ edit %s%s", a.File, status)
	case ActWrite:
		return fmt.Sprintf("📄 write %s%s", a.File, status)
	case ActRead:
		return fmt.Sprintf("👀 read %s", a.File)
	case ActVerify:
		return fmt.Sprintf("🧪 %s: `%s`%s", a.VerifyClass, clip(firstLine(a.Command), 90), status)
	case ActCommand:
		d := a.Desc
		if d == "" {
			d = clip(firstLine(a.Command), 90)
		}
		return fmt.Sprintf("💻 %s%s", d, status)
	case ActSearch:
		return fmt.Sprintf("🔎 %s", a.Tool)
	case ActAsk:
		return "❓ asked the user"
	default:
		return fmt.Sprintf("🔧 %s%s", a.Tool, status)
	}
}

func findStory(files []*FileStory, path string) *FileStory {
	for _, f := range files {
		if f.Path == path {
			return f
		}
	}
	return nil
}

// diffStat renders +N/−M, or "(new)" for untracked files where numstat is unavailable.
func diffStat(df DiffFile) string {
	if df.Status == "A" && df.Added == 0 && df.Deleted == 0 {
		return "(new)"
	}
	return fmt.Sprintf("+%d/−%d", df.Added, df.Deleted)
}

func mdEscape(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	return s
}
