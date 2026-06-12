package main

import (
	"fmt"
	"regexp"
	"strings"
)

var dangerPatterns = []struct {
	name string
	re   *regexp.Regexp
	sev  Severity
}{
	{"force-push", regexp.MustCompile(`git push[^\n]*(--force\b|-f\b)`), SevHigh},
	{"hard-reset", regexp.MustCompile(`git reset --hard`), SevWarn},
	{"recursive-rm", regexp.MustCompile(`rm -[a-zA-Z]*r[a-zA-Z]*f|rm -[a-zA-Z]*f[a-zA-Z]*r`), SevWarn},
	{"sudo", regexp.MustCompile(`(^|[;&|]\s*)sudo `), SevWarn},
	{"curl-pipe-sh", regexp.MustCompile(`(curl|wget)[^\n|]*\|\s*(ba|z)?sh`), SevHigh},
	{"chmod-777", regexp.MustCompile(`chmod\s+(-R\s+)?777`), SevWarn},
	{"skip-hooks", regexp.MustCompile(`git commit[^\n]*(--no-verify|-n\b)|--no-gpg-sign`), SevWarn},
	{"drop-table", regexp.MustCompile(`(?i)drop\s+(table|database)`), SevHigh},
	{"checkout-discard", regexp.MustCompile(`git checkout\s+--\s`), SevInfo},
}

// AssessRisks runs all deterministic risk rules over the assembled report parts.
func AssessRisks(exchanges []Exchange, files []*FileStory, diff []DiffFile, stats Stats) []Risk {
	var risks []Risk

	// R1/R2 — verification gaps per file (only flag files that are part of the diff,
	// to avoid noise from scratch files)
	anyVerify := stats.VerifyRuns > 0
	for _, f := range files {
		if !f.InDiff {
			continue
		}
		if !anyVerify {
			continue // covered by the session-level R0 below
		}
		if !f.VerifiedAfter {
			risks = append(risks, Risk{
				Rule: "edited-after-verify", Severity: SevWarn, Subject: f.Path,
				Detail: "最後の verify 成功より後に編集されている（検証ギャップ）",
			})
		}
	}
	// R0 — no verification at all in a session that changed files
	if !anyVerify && len(files) > 0 {
		risks = append(risks, Risk{
			Rule: "no-verification", Severity: SevHigh, Subject: fmt.Sprintf("%d files touched", len(files)),
			Detail: "セッション中に test/lint/typecheck/build が一度も実行されていない",
		})
	}
	// failed verify at session end
	if stats.VerifyFailed > 0 {
		risks = append(risks, Risk{
			Rule: "verify-failures", Severity: SevWarn,
			Subject: fmt.Sprintf("%d/%d verify runs failed", stats.VerifyFailed, stats.VerifyRuns),
			Detail:  "失敗した verify 実行がある。最終状態で解消済みかを確認のこと",
		})
	}

	// R3 — dangerous commands
	seen := map[string]bool{}
	for _, ex := range exchanges {
		for _, d := range ex.Decisions {
			for _, a := range d.Actions {
				if a.Command == "" {
					continue
				}
				for _, p := range dangerPatterns {
					if p.re.MatchString(a.Command) {
						key := p.name + "|" + clip(a.Command, 80)
						if seen[key] {
							continue
						}
						seen[key] = true
						risks = append(risks, Risk{
							Rule: "dangerous-command/" + p.name, Severity: p.sev,
							Subject: clip(a.Command, 120),
							Detail:  "破壊的/不可逆になり得るコマンド",
						})
					}
				}
			}
		}
	}

	// R4 — errors that never resolved: same command head errored and never succeeded later
	type cmdState struct {
		lastErr  bool
		errCount int
		example  string
	}
	cmds := map[string]*cmdState{}
	for _, ex := range exchanges {
		for _, d := range ex.Decisions {
			for _, a := range d.Actions {
				if a.Command == "" || !a.HasResult {
					continue
				}
				head := commandHead(a.Command)
				st, ok := cmds[head]
				if !ok {
					st = &cmdState{}
					cmds[head] = st
				}
				st.lastErr = a.IsError
				if a.IsError {
					st.errCount++
					st.example = clip(a.Command, 120)
				}
			}
		}
	}
	for head, st := range cmds {
		if st.lastErr && st.errCount > 0 {
			risks = append(risks, Risk{
				Rule: "unresolved-error", Severity: SevWarn, Subject: head,
				Detail: fmt.Sprintf("このコマンド種の最後の実行がエラーのまま（%d回失敗, 例: %s）", st.errCount, st.example),
			})
		}
	}

	// R5 — diff vs session mismatch
	sessionFiles := map[string]bool{}
	for _, f := range files {
		sessionFiles[f.Path] = true
	}
	var outOfSession []string
	for _, df := range diff {
		if !df.InSession {
			outOfSession = append(outOfSession, df.Path)
		}
	}
	if len(outOfSession) > 0 {
		risks = append(risks, Risk{
			Rule: "out-of-session-changes", Severity: SevInfo,
			Subject: fmt.Sprintf("%d files", len(outOfSession)),
			Detail:  "diff に含まれるがこのセッションの Edit/Write 由来でない変更（人手編集・コマンド副作用・別セッション）: " + clip(strings.Join(outOfSession, ", "), 240),
		})
	}
	var notInDiff []string
	for _, f := range files {
		if !f.InDiff {
			notInDiff = append(notInDiff, f.Path)
		}
	}
	if len(notInDiff) > 0 {
		risks = append(risks, Risk{
			Rule: "session-edits-not-in-diff", Severity: SevInfo,
			Subject: fmt.Sprintf("%d files", len(notInDiff)),
			Detail:  "セッションで編集されたが diff 範囲に現れないファイル（コミット済み/破棄/範囲外）: " + clip(strings.Join(notInDiff, ", "), 240),
		})
	}

	return risks
}

// commandHead normalizes a shell command to its leading executable words.
func commandHead(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if i := strings.IndexAny(cmd, "\n"); i > 0 {
		cmd = cmd[:i]
	}
	fields := strings.Fields(cmd)
	n := 2
	if len(fields) < n {
		n = len(fields)
	}
	return strings.Join(fields[:n], " ")
}
