package main

import "time"

// EventKind classifies a parsed transcript event.
type EventKind int

const (
	EvUserPrompt EventKind = iota
	EvAssistantText
	EvToolUse
	EvToolResult
)

// Event is a single normalized item from a session transcript.
type Event struct {
	Kind      EventKind
	Timestamp time.Time
	Sidechain bool

	// EvUserPrompt
	Prompt string

	// EvAssistantText
	Text string

	// EvToolUse
	ToolID   string
	ToolName string
	FilePath string // Edit/Write/NotebookEdit
	Command  string // Bash
	Desc     string // Bash description

	// EvToolResult
	ForToolID     string
	IsError       bool
	ResultSnippet string
}

// Session is one parsed transcript file.
type Session struct {
	Path      string
	ID        string
	Title     string
	Version   string
	GitBranch string
	CWD       string
	Start     time.Time
	End       time.Time
	Events    []Event // main chain only (non-sidechain)
	Sidechain int     // count of sidechain (subagent) events, surfaced not parsed
}

// ActionKind classifies what a tool call did.
type ActionKind int

const (
	ActEdit ActionKind = iota
	ActWrite
	ActRead
	ActCommand
	ActVerify // test / lint / typecheck / build command
	ActSearch
	ActAsk
	ActOther
)

func (k ActionKind) String() string {
	switch k {
	case ActEdit:
		return "edit"
	case ActWrite:
		return "write"
	case ActRead:
		return "read"
	case ActCommand:
		return "command"
	case ActVerify:
		return "verify"
	case ActSearch:
		return "search"
	case ActAsk:
		return "ask"
	default:
		return "other"
	}
}

// Action is one tool call joined with its result.
type Action struct {
	Kind          ActionKind
	Tool          string
	File          string // repo-relative when possible
	Command       string
	Desc          string
	Timestamp     time.Time
	IsError       bool
	HasResult     bool
	ResultSnippet string
	VerifyClass   string // "test" | "lint" | "typecheck" | "build" | ""
}

// Decision is one assistant narration followed by the actions it produced.
type Decision struct {
	Rationale string
	Timestamp time.Time
	Actions   []Action
}

// Exchange is one user prompt and the decisions made under it.
type Exchange struct {
	Prompt    string
	Timestamp time.Time
	Decisions []Decision
}

// FileStory aggregates everything the session did to one file.
type FileStory struct {
	Path          string
	Edits         int
	Writes        int
	FirstTouch    time.Time
	LastTouch     time.Time
	Rationales    []string // decision snippets that touched this file (deduped, ordered)
	VerifiedAfter bool     // a verify action succeeded after the last touch
	InDiff        bool
}

// DiffFile is one entry from git diff.
type DiffFile struct {
	Path      string
	Status    string // M/A/D/R...
	Added     int
	Deleted   int
	InSession bool
}

// Risk severity levels.
type Severity int

const (
	SevInfo Severity = iota
	SevWarn
	SevHigh
)

func (s Severity) String() string {
	switch s {
	case SevHigh:
		return "HIGH"
	case SevWarn:
		return "WARN"
	default:
		return "INFO"
	}
}

// Risk is one finding from the risk rules.
type Risk struct {
	Rule     string
	Severity Severity
	Subject  string // file or command
	Detail   string
}

// Report is the full assembled review model.
type Report struct {
	Session     *Session
	RepoRoot    string
	DiffBase    string
	Exchanges   []Exchange
	Files       []*FileStory
	Diff        []DiffFile
	Risks       []Risk
	Stats       Stats
	GeneratedAt time.Time
}

// Stats are headline numbers for the summary.
type Stats struct {
	UserPrompts    int
	Decisions      int
	ToolCalls      int
	FilesTouched   int
	EditCount      int
	WriteCount     int
	CommandsRun    int
	VerifyRuns     int
	VerifyFailed   int
	ToolErrors     int
	SubagentEvents int
	DurationMin    float64
}
