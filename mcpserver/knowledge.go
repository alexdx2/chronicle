package mcpserver

import (
	"context"
	"sync"
	"time"

	"github.com/alexdx2/chronicle-core/freshness"
	"github.com/alexdx2/chronicle-core/store"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Every query answer is built from knowledge of some age, and an agent that
// cannot see that age treats a year-old graph like today's. So every read-only
// result ends with one extra content block naming the commit the knowledge
// came from. It is a SECOND block: the first block stays byte-identical, so
// every existing parser keeps working.

// KnowledgeLiner supplies the knowledge line appended to query results.
type KnowledgeLiner interface {
	// KnowledgeLine returns "" to append nothing.
	KnowledgeLine(toolName string, firstBlockText string) string
}

var (
	knowledgeMu    sync.RWMutex
	knowledgeLiner KnowledgeLiner
)

// SetKnowledgeLiner installs the process-wide liner (nil disables). Core's
// mcp serve installs NewStoreLiner; pro installs its federated liner.
func SetKnowledgeLiner(l KnowledgeLiner) {
	knowledgeMu.Lock()
	knowledgeLiner = l
	knowledgeMu.Unlock()
}

func currentLiner() KnowledgeLiner {
	knowledgeMu.RLock()
	defer knowledgeMu.RUnlock()
	return knowledgeLiner
}

// QueryTools is the allowlist that receives the block: the read-only tools
// whose answers an agent acts on. Built from freshness.QueryToolNames so the
// list that decides "this is a query" has exactly one definition.
var QueryTools = func() map[string]bool {
	m := make(map[string]bool, len(freshness.QueryToolNames))
	for _, n := range freshness.QueryToolNames {
		m[n] = true
	}
	return m
}()

// appendKnowledge is the one place the block is decided and added — both
// wrappers call it, so logging and non-logging hosts cannot drift apart.
// Nothing is appended for a non-query tool, a failed call, an error result, or
// when no liner is installed.
func appendKnowledge(toolName string, result *mcplib.CallToolResult, callErr error, firstBlockText string) {
	if callErr != nil || result == nil || result.IsError || !QueryTools[toolName] {
		return
	}
	l := currentLiner()
	if l == nil {
		return
	}
	line := l.KnowledgeLine(toolName, firstBlockText)
	if line == "" {
		return
	}
	result.Content = append(result.Content, mcplib.TextContent{Type: "text", Text: line})
}

// WrapWithKnowledge appends the knowledge block to h's result without
// touching the request log. chronicle-pro's federation server has no
// request-log store of its own — it wraps its tools with this so federated
// answers carry the same age marker core's do.
func WrapWithKnowledge(name string, h server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		result, err := h(ctx, req)
		appendKnowledge(name, result, err, firstBlockText(result))
		return result, err
	}
}

func firstBlockText(result *mcplib.CallToolResult) string {
	if result == nil {
		return ""
	}
	for _, c := range result.Content {
		if tc, ok := c.(mcplib.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// storeLiner renders freshness.Compute for one project. A query burst must not
// mean a git shell-out per call, so the line is cached for ttl; freshness is
// measured in commits, not milliseconds.
type storeLiner struct {
	repoDir string
	repo    string
	store   *store.Store
	ttl     time.Duration

	mu         sync.Mutex
	line       string
	computedAt time.Time
	computed   bool
}

// NewStoreLiner caches freshness.Compute(repoDir, repo, "", s) for ttl.
func NewStoreLiner(repoDir, repo string, s *store.Store, ttl time.Duration) KnowledgeLiner {
	return &storeLiner{repoDir: repoDir, repo: repo, store: s, ttl: ttl}
}

func (l *storeLiner) KnowledgeLine(string, string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.computed && time.Since(l.computedAt) < l.ttl {
		return l.line
	}
	line := ""
	if rep, err := freshness.Compute(l.repoDir, l.repo, "", l.store); err == nil {
		line = rep.Line()
	}
	l.line = line
	l.computedAt = time.Now()
	l.computed = true
	return line
}
