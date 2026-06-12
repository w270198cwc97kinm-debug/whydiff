package main

import (
	"fmt"
	"html"
	"sort"
	"strings"
)

// RenderHTML emits a single-file, dependency-free review page.
func RenderHTML(r *Report) string {
	var b strings.Builder
	title := r.Session.Title
	if title == "" {
		title = r.Session.ID
	}
	esc := html.EscapeString

	b.WriteString(`<!DOCTYPE html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>recap — ` + esc(title) + `</title>
<style>
:root{--bg:#0d1117;--panel:#161b22;--border:#30363d;--fg:#e6edf3;--dim:#8b949e;--accent:#58a6ff;--ok:#3fb950;--warn:#d29922;--high:#f85149}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.6 -apple-system,"Segoe UI",Roboto,"Hiragino Sans","Noto Sans JP",sans-serif}
.wrap{max-width:980px;margin:0 auto;padding:24px}
h1{font-size:20px;margin:.2em 0}h2{font-size:16px;border-bottom:1px solid var(--border);padding-bottom:6px;margin-top:28px}
.dim{color:var(--dim)}.mono{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12.5px}
.stats{display:flex;gap:14px;flex-wrap:wrap;margin:10px 0;color:var(--dim);font-size:13px}
.card{background:var(--panel);border:1px solid var(--border);border-radius:8px;padding:12px 14px;margin:10px 0}
table{border-collapse:collapse;width:100%}td,th{border:1px solid var(--border);padding:6px 8px;text-align:left;font-size:13px;vertical-align:top}
.sev-HIGH{color:var(--high);font-weight:700}.sev-WARN{color:var(--warn);font-weight:600}.sev-INFO{color:var(--dim)}
.ok{color:var(--ok)}.bad{color:var(--high)}
details{border:1px solid var(--border);border-radius:8px;margin:8px 0;background:var(--panel)}
summary{cursor:pointer;padding:9px 12px;font-weight:600}details[open]>summary{border-bottom:1px solid var(--border)}
.body{padding:8px 14px}
.act{margin:2px 0 2px 14px;color:var(--dim)}.act .mono{color:var(--fg)}
.decision{margin:10px 0 2px;color:var(--fg)}
.badge{display:inline-block;border:1px solid var(--border);border-radius:99px;padding:0 8px;font-size:11.5px;color:var(--dim);margin-left:6px}
.err{color:var(--high)}
</style></head><body><div class="wrap">`)

	fmt.Fprintf(&b, `<h1>recap <span class="dim">—</span> %s</h1>`, esc(title))
	fmt.Fprintf(&b, `<div class="dim mono">session %s · base %s · %s</div>`,
		esc(r.Session.ID), esc(r.DiffBase), esc(r.GeneratedAt.Local().Format("2006-01-02 15:04")))
	fmt.Fprintf(&b, `<div class="stats"><span>👤 %d prompts</span><span>🧠 %d decisions</span><span>🔧 %d tool calls</span><span>📄 %d files</span><span>🧪 verify %d (fail %d)</span><span>✖ errors %d</span><span>⏱ %.0f min</span><span>🤖 subagent events %d</span></div>`,
		r.Stats.UserPrompts, r.Stats.Decisions, r.Stats.ToolCalls, r.Stats.FilesTouched,
		r.Stats.VerifyRuns, r.Stats.VerifyFailed, r.Stats.ToolErrors, r.Stats.DurationMin, r.Stats.SubagentEvents)

	// risks
	fmt.Fprintf(&b, `<h2>⚠ Risks (%d)</h2>`, len(r.Risks))
	if len(r.Risks) == 0 {
		b.WriteString(`<div class="card ok">None detected</div>`)
	} else {
		rs := append([]Risk(nil), r.Risks...)
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].Severity > rs[j].Severity })
		b.WriteString(`<table><tr><th>Sev</th><th>Rule</th><th>Subject</th><th>Detail</th></tr>`)
		for _, k := range rs {
			fmt.Fprintf(&b, `<tr><td class="sev-%s">%s</td><td>%s</td><td class="mono">%s</td><td>%s</td></tr>`,
				k.Severity, k.Severity, esc(k.Rule), esc(clip(k.Subject, 110)), esc(k.Detail))
		}
		b.WriteString(`</table>`)
	}

	// intent
	b.WriteString(`<h2>Intent (what was asked)</h2><div class="card"><ol>`)
	for _, ex := range r.Exchanges {
		if ex.Prompt == "(session resumed)" {
			continue
		}
		fmt.Fprintf(&b, `<li>%s</li>`, esc(firstLine(ex.Prompt)))
	}
	b.WriteString(`</ol></div>`)

	// files
	fmt.Fprintf(&b, `<h2>Changed files × why (%d in diff)</h2>`, len(r.Diff))
	for _, df := range r.Diff {
		st := findStory(r.Files, df.Path)
		mark, ver := "🤖", ""
		if st == nil {
			mark = "✋"
			ver = `<span class="badge">not from this session</span>`
		} else if st.VerifiedAfter {
			ver = `<span class="badge ok">✓ verified after last edit</span>`
		} else {
			ver = `<span class="badge bad">✗ unverified after last edit</span>`
		}
		fmt.Fprintf(&b, `<details><summary>%s <span class="mono">%s</span> <span class="dim">%s %s</span>%s</summary><div class="body">`,
			mark, esc(df.Path), esc(df.Status), esc(diffStat(df)), ver)
		if st != nil {
			fmt.Fprintf(&b, `<div class="dim">%d edits / %d writes</div>`, st.Edits, st.Writes)
			if len(st.Rationales) > 0 {
				b.WriteString(`<div>Decision trail:</div><ul>`)
				for _, ra := range st.Rationales {
					fmt.Fprintf(&b, `<li>%s</li>`, esc(ra))
				}
				b.WriteString(`</ul>`)
			}
		} else {
			b.WriteString(`<div class="dim">Not written by this session (human edits, command side-effects, or another session)</div>`)
		}
		b.WriteString(`</div></details>`)
	}

	// timeline
	b.WriteString(`<h2>Timeline (decisions → actions)</h2>`)
	for _, ex := range r.Exchanges {
		fmt.Fprintf(&b, `<details><summary>👤 %s <span class="dim mono">%s</span></summary><div class="body">`,
			esc(firstLine(ex.Prompt)), esc(ex.Timestamp.Local().Format("15:04")))
		for _, d := range ex.Decisions {
			if d.Rationale != "" {
				fmt.Fprintf(&b, `<div class="decision">🧠 %s</div>`, esc(firstLine(d.Rationale)))
			}
			for _, a := range d.Actions {
				errMark := ""
				if a.HasResult && a.IsError {
					errMark = ` <span class="err">✖</span>`
				}
				fmt.Fprintf(&b, `<div class="act">%s%s</div>`, esc(actionLine(a)), errMark)
			}
		}
		b.WriteString(`</div></details>`)
	}

	b.WriteString(`<p class="dim" style="margin-top:28px">generated by <span class="mono">recap</span> — agent sessions, reviewable.</p></div></body></html>`)
	return b.String()
}
