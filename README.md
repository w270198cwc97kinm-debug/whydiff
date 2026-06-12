# recap — agent sessions, reviewable

**recap** reconstructs what your coding agent actually did — *intent → decisions → actions → verification* — and correlates it with your git diff, so you can review **why** the changes were made, not just what changed.

When an agent generates a 35-file PR, no human holds an authoritative understanding of why each change exists. Review bots find bugs; **recap reconstructs the reasoning.**

- 🏠 **Local-first** — reads Claude Code session transcripts already on your machine. No network, no API keys, no code leaves your laptop.
- 🧭 **Per-file "why"** — every changed file is linked to the agent's own narration that produced it.
- 🧪 **Verification gaps** — flags files edited *after* the last passing test/lint/build, sessions with **no verification at all**, and verify failures.
- ⚠ **Risk rules** — dangerous commands (force-push, `rm -rf`, `curl | sh`, skipped hooks), unresolved errors, and **out-of-session changes** (diff entries the agent never wrote — human edits or command side-effects).
- 📋 **Three outputs** — terminal summary, **markdown to paste into your PR description**, and a self-contained HTML report.

## Install

```sh
go install github.com/kainuma/recap@latest
```

(or download a binary from Releases)

## Usage

```sh
recap                    # newest session for this repo + auto-detected diff base
recap list               # list sessions recorded for this repo
recap --base main        # explicit diff base ("worktree" = uncommitted changes)
recap --md report.md     # markdown report — paste into your PR
recap --html report.html # self-contained HTML report
recap --session <path>   # specific transcript (.jsonl)
```

### Typical workflow

```sh
# agent finishes a task; before opening the PR:
recap --md RECAP.md && gh pr create --body-file RECAP.md
```

Reviewers see: the original ask, the agent's decision trail per file, what was verified (and what wasn't), and every risk flag — before reading a single diff hunk.

## What the report contains

| Section | What it answers |
|---|---|
| ⚠ Risks | What deserves suspicion first? |
| Intent | What was the agent asked to do? |
| Files × Why | For each changed file: which decisions produced it, how many edits, verified after last touch? |
| Timeline | The full decision → action trail, collapsible per prompt |

Markers: `🤖` agent-authored (from the session) · `✋` in the diff but **not** from this session's edits.

## Supported agents

- **Claude Code** (session transcripts in `~/.claude/projects/...`) — today.
- The transcript format is internal to Claude Code and may change; recap parses defensively and skips what it doesn't understand. Issues/PRs for other agents (Codex CLI, Cursor, OpenCode…) welcome.

## How it works

recap parses the session JSONL (user prompts, assistant narration, tool calls, tool results), joins tool calls to their results, classifies actions (edit / write / command / **verify** = test·lint·typecheck·build), builds a per-file story, diffs your repo, and runs deterministic risk rules. **No LLM calls** — the report is a faithful reconstruction, not a summary that can hallucinate.

## License

MIT
