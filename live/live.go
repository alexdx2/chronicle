// Package live is the conversation between an agent and an open Chronicle
// dashboard: the agent's diagrams reach the screen as events, the screen
// reports what the human looks at, and the agent reads it back. Events live
// in each repo's store (store.LiveEvent); this package gives them shape,
// validates what the browser sends, computes the dashboard state for the
// MCP tool and tails new diagrams for the admin servers. It never knows
// where the admin listens: URLs belong to the tool wrappers.
package live

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/alexdx2/chronicle-core/graph/viewmodel"
	"github.com/alexdx2/chronicle-core/store"
)

const (
	KindDiagram = "diagram"
	KindScreen  = "screen"

	SourceMCP        = "mcp"
	SourceFederation = "federation"
	SourceDashboard  = "dashboard"

	// OpenWindow: a dashboard client is alive while its last screen report
	// is younger than this (reports come on every change and every 60 s).
	OpenWindow = 90 * time.Second
)

// Counts sizes a diagram for the dashboard card.
type Counts struct {
	Nodes  int `json:"nodes"`
	Groups int `json:"groups"`
	Edges  int `json:"edges"`
	Hidden int `json:"hidden"`
}

// Diagram is the payload of a diagram event: everything the dashboard needs
// to announce and open the agent's view.
type Diagram struct {
	Title  string             `json:"title"`
	Level  string             `json:"level"`
	Target string             `json:"target,omitempty"`
	Domain string             `json:"domain"`
	URL    string             `json:"url"`
	Spec   viewmodel.ViewSpec `json:"spec"`
	Counts Counts             `json:"counts"`
	Marks  int                `json:"marks"`
}

// Screen is what one dashboard tab reports about itself.
type Screen struct {
	ClientID    string              `json:"client_id"`
	Tab         string              `json:"tab"`
	Title       string              `json:"title,omitempty"`
	Spec        *viewmodel.ViewSpec `json:"spec,omitempty"`
	Selected    *Selection          `json:"selected,omitempty"`
	Breadcrumbs []string            `json:"breadcrumbs,omitempty"`
	Visible     bool                `json:"visible"`
	Closed      bool                `json:"closed,omitempty"`
}

// Selection is the element the human selected: a node key, or the
// component edge keys of an arrow.
type Selection struct {
	Kind string   `json:"kind"`
	Key  string   `json:"key,omitempty"`
	Keys []string `json:"keys,omitempty"`
}

// Source is one store the channel reads or writes; Repo names it in a
// federation and stays empty for a single repo.
type Source struct {
	Repo  string
	Store *store.Store
}

// PublishDiagram records a built diagram as a live event.
func PublishDiagram(st *store.Store, source string, d Diagram) (store.LiveEvent, error) {
	payload, err := json.Marshal(d)
	if err != nil {
		return store.LiveEvent{}, fmt.Errorf("live: diagram: %w", err)
	}
	return st.InsertLiveEvent(KindDiagram, source, payload)
}

// ReportScreen records a (parsed) screen report.
func ReportScreen(st *store.Store, s Screen) (store.LiveEvent, error) {
	payload, err := json.Marshal(s)
	if err != nil {
		return store.LiveEvent{}, fmt.Errorf("live: screen: %w", err)
	}
	return st.InsertLiveEvent(KindScreen, SourceDashboard, payload)
}
