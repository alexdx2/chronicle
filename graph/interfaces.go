package graph

// GraphQuerier is the primary query interface. OSS provides a single-repo
// implementation (Graph). Enterprise provides a federated implementation.
type GraphQuerier interface {
	QueryDeps(nodeKey string, maxDepth int, filters []string) ([]DepNode, error)
	QueryReverseDeps(nodeKey string, maxDepth int, filters []string) ([]DepNode, error)
	QueryPath(fromKey, toKey string, opts PathOptions) (*PathResult, error)
	QueryImpact(nodeKey string, opts ImpactOptions) (*ImpactResult, error)
	QueryStats(domainKey string) (*Stats, error)
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
}

// AmbiguousRef identifies a candidate node in conflict resolution (enterprise).
type AmbiguousRef struct {
	RepoName   string  `json:"repo_name"`
	NodeKey    string  `json:"node_key"`
	TrustScore float64 `json:"trust_score"`
	Status     string  `json:"status"`
}

// Compile-time check: Graph implements GraphQuerier.
var _ GraphQuerier = (*Graph)(nil)
