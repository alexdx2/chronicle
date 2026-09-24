package mcpserver

import (
	"time"

	"github.com/alexdx2/chronicle-core/graph/viewmodel"
	"github.com/alexdx2/chronicle-core/live"
	"github.com/alexdx2/chronicle-core/store"
)

// publishLiveDiagram announces a built diagram to open dashboards. It never
// fails the tool: the diagram is the answer, the announcement a courtesy.
func publishLiveDiagram(st *store.Store, source string, d live.Diagram) map[string]any {
	ev, err := live.PublishDiagram(st, source, d)
	if err != nil {
		return map[string]any{"published": false, "error": err.Error()}
	}
	state := live.State([]live.Source{{Store: st}}, time.Now())
	return map[string]any{"published": true, "event_id": ev.ID, "dashboard_open": state.Open}
}

// mergeMarks lays over on top of base: the tool's marks parameter wins over
// view_spec.marks on the same key. Validation runs on the result.
func mergeMarks(base, over map[string]viewmodel.Mark) map[string]viewmodel.Mark {
	if len(base) == 0 && len(over) == 0 {
		return nil
	}
	out := make(map[string]viewmodel.Mark, len(base)+len(over))
	for k, m := range base {
		out[k] = m
	}
	for k, m := range over {
		out[k] = m
	}
	return out
}

func hiddenCount(h []viewmodel.HiddenEntry) int {
	n := 0
	for _, e := range h {
		n += e.Count
	}
	return n
}
