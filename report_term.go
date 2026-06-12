package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

var useColor = os.Getenv("NO_COLOR") == ""

func c(code, s string) string {
	if !useColor {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// RenderTerminal prints a compact review summary for the console.
func RenderTerminal(r *Report) string {
	var b strings.Builder
	title := r.Session.Title
	if title == "" {
		title = r.Session.ID
	}
	fmt.Fprintf(&b, "\n%s %s\n", c("1;36", "whydiff"), c("1", title))
	fmt.Fprintf(&b, "%s session=%s base=%s\n", c("2", "·"), c("2", clip(r.Session.ID, 12)), c("2", r.DiffBase))
	fmt.Fprintf(&b, "%s %d prompts · %d decisions · %d tool calls · %d files · verify %d (fail %d) · errors %d · %.0f min\n\n",
		c("2", "·"), r.Stats.UserPrompts, r.Stats.Decisions, r.Stats.ToolCalls, r.Stats.FilesTouched,
		r.Stats.VerifyRuns, r.Stats.VerifyFailed, r.Stats.ToolErrors, r.Stats.DurationMin)

	// risks
	fmt.Fprintf(&b, "%s\n", c("1", fmt.Sprintf("⚠ Risks (%d)", len(r.Risks))))
	if len(r.Risks) == 0 {
		fmt.Fprintf(&b, "  %s\n", c("32", "none"))
	} else {
		rs := append([]Risk(nil), r.Risks...)
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].Severity > rs[j].Severity })
		for _, k := range rs {
			sev := k.Severity.String()
			switch k.Severity {
			case SevHigh:
				sev = c("1;31", sev)
			case SevWarn:
				sev = c("33", sev)
			default:
				sev = c("2", sev)
			}
			fmt.Fprintf(&b, "  %s %s %s\n      %s\n", sev, c("1", k.Rule), clip(k.Subject, 90), c("2", clip(k.Detail, 160)))
		}
	}
	b.WriteString("\n")

	// intent
	fmt.Fprintf(&b, "%s\n", c("1", "Intent"))
	n := 0
	for _, ex := range r.Exchanges {
		if ex.Prompt == "(session resumed)" {
			continue
		}
		n++
		fmt.Fprintf(&b, "  %d. %s\n", n, clip(firstLine(ex.Prompt), 110))
	}
	b.WriteString("\n")

	// files: all agent-authored, then a capped list of the rest
	fmt.Fprintf(&b, "%s\n", c("1", fmt.Sprintf("Changed files (%d in diff)", len(r.Diff))))
	const maxOther = 5
	otherShown, otherTotal := 0, 0
	for _, df := range r.Diff {
		st := findStory(r.Files, df.Path)
		if st == nil {
			otherTotal++
			if otherShown >= maxOther {
				continue
			}
			otherShown++
			fmt.Fprintf(&b, "  [%s] %s %s\n", c("33", "other"), df.Path, c("2", diffStat(df)))
			continue
		}
		ver := c("31", " ✗unverified")
		if st.VerifiedAfter {
			ver = c("32", " ✓verified")
		}
		fmt.Fprintf(&b, "  [%s] %s %s%s\n", c("36", "agent"), df.Path, c("2", diffStat(df)), ver)
		if len(st.Rationales) > 0 {
			fmt.Fprintf(&b, "        %s %s\n", c("2", "why:"), c("2", clip(st.Rationales[0], 120)))
		}
	}
	if otherTotal > otherShown {
		fmt.Fprintf(&b, "  %s\n", c("2", fmt.Sprintf("… and %d more files not from this session (see --md/--html for all)", otherTotal-otherShown)))
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "%s\n", c("2", "full report: whydiff --md report.md / whydiff --html report.html"))
	return b.String()
}
