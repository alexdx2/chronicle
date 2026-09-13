package graph

import "github.com/alexdx2/chronicle-core/store"

// GraphQuerier is the primary query interface. OSS provides a single-repo
// implementation (Graph). Enterprise provides a federated implementation.
type GraphQuerier interface {
	QueryDeps(nodeKey string, maxDepth int, filters []string) ([]DepNode, error)
	QueryReverseDeps(nodeKey string, maxDepth int, filters []string) ([]DepNode, error)
	QueryPath(fromKey, toKey string, opts PathOptions) (*PathResult, error)
	QueryImpact(nodeKey string, opts ImpactOptions) (*ImpactResult, error)
	QueryStats(domainKey string) (*Stats, error)

	// Retrieval primitives (single-repo on Graph; cross-repo on FederatedGraph).
	// The agent composes these — no natural-language understanding lives here.
	NodeSearch(q string, f store.NodeFilter, limit int) ([]SearchResult, error)
	Subgraph(rootKey string, opts SubgraphOptions) (*SubgraphResult, error)
	Insights(domainKey string) (*InsightsResult, error)
}

// GraphDiscoverer finds .depbot/ directories and returns openable graph targets.
// OSS provides a single-directory implementation. Enterprise scans children.
type GraphDiscoverer interface {
	Discover(rootDir string) ([]GraphTarget, error)
}

// GraphTarget represents a discovered repo with a .depbot/ directory.
type GraphTarget struct {
	RepoName string `json:"repo_name"`
	Path     string `json:"path"`
	Domain   string `json:"domain,omitempty"`
	// Status is "" when the target holds knowledge, "empty" when its DB has
	// never recorded a scan, and "error" when discovery could not read it at
	// all. Both non-empty statuses stay visible — discovery tells you the
	// directory exists — but no caller may query them: a ghost DB contributes
	// nothing and would only make a federation look larger than the knowledge
	// behind it, and an unreadable one contributes nothing it can vouch for.
	Status string `json:"status,omitempty"`
	// Reason carries why a target is not queryable — the open or query error
	// behind Status "error". A repo that drops out of a federation because its
	// DB was locked mid-scan must say so: "silently missing" and "known to
	// hold nothing" are opposite facts, and only one of them is a reason to
	// stop looking.
	Reason string `json:"reason,omitempty"`
}

// Target statuses. Closed set — discovery sets them, every opener skips
// anything but "".
const (
	// TargetStatusEmpty marks a discovered .depbot/ whose DB has no scan revision.
	TargetStatusEmpty = "empty"
	// TargetStatusError marks one discovery could not read; Reason says why.
	TargetStatusError = "error"
)

// AmbiguousRef identifies a candidate node in conflict resolution (enterprise).
type AmbiguousRef struct {
	RepoName   string  `json:"repo_name"`
	NodeKey    string  `json:"node_key"`
	TrustScore float64 `json:"trust_score"`
	Status     string  `json:"status"`
}

// Compile-time check: Graph implements GraphQuerier.
var _ GraphQuerier = (*Graph)(nil)
