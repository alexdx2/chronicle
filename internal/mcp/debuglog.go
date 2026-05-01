package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

var (
	activeDebugLogger *DebugLogger
	debugLoggerMu     sync.RWMutex
)

// SetDebugLogger sets the package-level debug logger (called from CLI).
func SetDebugLogger(dl *DebugLogger) {
	debugLoggerMu.Lock()
	defer debugLoggerMu.Unlock()
	activeDebugLogger = dl
}

// GetDebugLogger returns the active debug logger, or nil if debug mode is off.
func GetDebugLogger() *DebugLogger {
	debugLoggerMu.RLock()
	defer debugLoggerMu.RUnlock()
	return activeDebugLogger
}

// DebugLogger writes structured JSONL and freeform markdown debug logs per session.
type DebugLogger struct {
	mu        sync.Mutex
	jsonlFile *os.File
	mdFile    *os.File
	seq       int
	recentErr []recentErrEntry // ring buffer for retry detection
}

type recentErrEntry struct {
	seq  int
	tool string
}

// DebugEntry is a single line in the JSONL debug log.
type DebugEntry struct {
	Timestamp     string         `json:"ts"`
	Seq           int            `json:"seq"`
	Type          string         `json:"type"`
	Tool          string         `json:"tool,omitempty"`
	Params        map[string]any `json:"params,omitempty"`
	ResultSummary string         `json:"result_summary,omitempty"`
	DurationMs    int            `json:"duration_ms,omitempty"`
	Error         string         `json:"error,omitempty"`
	IsError       bool           `json:"is_error,omitempty"`
	OriginalSeq   int            `json:"original_seq,omitempty"`
	RevisionID    int64          `json:"revision_id,omitempty"`
	ToolCount     int            `json:"tool_count,omitempty"`

	// session_start fields
	ChronicleVersion string `json:"chronicle_version,omitempty"`
	OS               string `json:"os,omitempty"`
	GoVersion        string `json:"go_version,omitempty"`
	GitSHA           string `json:"git_sha,omitempty"`
}

func NewDebugLogger(debugDir, chronicleVersion string) (*DebugLogger, error) {
	if err := os.MkdirAll(debugDir, 0755); err != nil {
		return nil, fmt.Errorf("create debug dir: %w", err)
	}

	// Generate session ID: timestamp_4hex
	now := time.Now().UTC()
	b := make([]byte, 2)
	rand.Read(b)
	sessionID := now.Format("2006-01-02T15-04-05") + "_" + hex.EncodeToString(b)

	jsonlPath := filepath.Join(debugDir, sessionID+".jsonl")
	mdPath := filepath.Join(debugDir, sessionID+".claude.md")

	jsonlFile, err := os.Create(jsonlPath)
	if err != nil {
		return nil, fmt.Errorf("create jsonl: %w", err)
	}

	mdFile, err := os.Create(mdPath)
	if err != nil {
		jsonlFile.Close()
		return nil, fmt.Errorf("create md: %w", err)
	}

	dl := &DebugLogger{
		jsonlFile: jsonlFile,
		mdFile:    mdFile,
	}

	// Write session_start
	gitSHA := ""
	if out, err := exec.Command("git", "rev-parse", "HEAD").Output(); err == nil {
		gitSHA = strings.TrimSpace(string(out))
	}

	dl.writeEntry(DebugEntry{
		Type:             "session_start",
		ChronicleVersion: chronicleVersion,
		OS:               runtime.GOOS,
		GoVersion:        runtime.Version(),
		GitSHA:           gitSHA,
	})

	// Write markdown heading
	fmt.Fprintf(mdFile, "# Debug Session %s\n\n", now.Format("2006-01-02 15:04"))

	return dl, nil
}

func (dl *DebugLogger) writeEntry(e DebugEntry) {
	dl.mu.Lock()
	defer dl.mu.Unlock()

	e.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	e.Seq = dl.seq
	dl.seq++

	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	dl.jsonlFile.Write(data)
	dl.jsonlFile.Write([]byte("\n"))
}

// LogToolCall logs a tool call with its result or error.
func (dl *DebugLogger) LogToolCall(tool string, params map[string]any, resultSummary string, durationMs int, errMsg string) {
	isErr := errMsg != ""
	entry := DebugEntry{
		Type:          "tool_call",
		Tool:          tool,
		Params:        params,
		ResultSummary: resultSummary,
		DurationMs:    durationMs,
		IsError:       isErr,
	}
	if isErr {
		entry.Error = errMsg
	}

	dl.writeEntry(entry)

	// Retry detection
	dl.mu.Lock()
	currentSeq := dl.seq - 1 // we just incremented in writeEntry

	if isErr {
		dl.recentErr = append(dl.recentErr, recentErrEntry{seq: currentSeq, tool: tool})
		// Keep only last 10 entries
		if len(dl.recentErr) > 10 {
			dl.recentErr = dl.recentErr[len(dl.recentErr)-10:]
		}
	} else {
		// Check if this is a retry of a recent error
		for _, re := range dl.recentErr {
			if re.tool == tool && currentSeq-re.seq <= 3 {
				dl.mu.Unlock()
				dl.writeEntry(DebugEntry{
					Type:        "inferred_retry",
					Tool:        tool,
					OriginalSeq: re.seq,
				})
				return
			}
		}
	}
	dl.mu.Unlock()
}

// LogClaudeMessage appends a freeform message to the .claude.md file.
func (dl *DebugLogger) LogClaudeMessage(message string) {
	dl.mu.Lock()
	defer dl.mu.Unlock()

	fmt.Fprintf(dl.mdFile, "- %s\n", message)
}

// LogAbandoned logs an inferred_abandoned entry for an open revision.
func (dl *DebugLogger) LogAbandoned(revisionID int64) {
	dl.writeEntry(DebugEntry{
		Type:       "inferred_abandoned",
		RevisionID: revisionID,
		ToolCount:  dl.seq,
	})
}

// Close flushes and closes the debug log files.
func (dl *DebugLogger) Close() error {
	dl.mu.Lock()
	defer dl.mu.Unlock()

	var firstErr error
	if dl.jsonlFile != nil {
		if err := dl.jsonlFile.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if dl.mdFile != nil {
		if err := dl.mdFile.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// debugLogTool defines the chronicle_debug_log MCP tool.
func debugLogTool() mcplib.Tool {
	return mcplib.NewTool("chronicle_debug_log",
		mcplib.WithDescription("Log a debug note. Write whatever you're thinking — confusion, assumptions, workarounds, observations. Only available in debug mode."),
		mcplib.WithString("message", mcplib.Required(), mcplib.Description("Freeform debug note")),
	)
}

// debugLogHandler returns the handler for chronicle_debug_log.
func debugLogHandler() server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		dl := GetDebugLogger()
		if dl == nil {
			return mcplib.NewToolResultError("debug mode is not active"), nil
		}
		message := strParam(req.GetArguments(), "message")
		if message == "" {
			return mcplib.NewToolResultError("message is required"), nil
		}
		dl.LogClaudeMessage(message)
		return mcplib.NewToolResultText("logged"), nil
	}
}

const debugInstructions = `

## Debug Mode Active

You are in a debug session. Use chronicle_debug_log to write notes as you work.
Write naturally — no format required. Just share what you're thinking.

Log when you:
- Are unsure which tool to use or what params to pass
- Retry something that failed — say why
- Get a result that surprises you or seems wrong
- Work around a limitation or missing feature
- Notice something that could be better (error messages, missing data, confusing API)
- Make an assumption about how a tool works

Don't overthink it. Quick notes are fine:
"wasn't sure if kind should be model or entity"
"import_all returned success but node count didn't change — confused"
"had to call scan_status just to find out if revision was open"
`
