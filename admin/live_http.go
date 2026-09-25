package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/alexdx2/chronicle-core/live"
)

// The live channel's HTTP gate, shared by Chronicle's and Chronicle Pro's
// admin servers so both enforce the same checks on untrusted browser input.

// LiveQuery is a validated GET /api/live query.
type LiveQuery struct {
	Limit    int   // 1..200, default 20
	After    int64 // meaningful when HasAfter
	HasAfter bool
	Repo     string // repo=<name>; federation readers need it together with after
}

// ParseLiveQuery validates GET /api/live: GET only (405), kind diagram only
// (400 — screen state is never served), limit 1..200 (default 20), after a
// non-negative id. On failure it has written the error and returns false.
func ParseLiveQuery(w http.ResponseWriter, r *http.Request) (LiveQuery, bool) {
	if r.Method != http.MethodGet {
		writeLiveError(w, http.StatusMethodNotAllowed, "method not allowed")
		return LiveQuery{}, false
	}
	q := r.URL.Query()
	if k := q.Get("kind"); k != "" && k != live.KindDiagram {
		writeLiveError(w, http.StatusBadRequest, "screen state is not served; use chronicle_dashboard_state")
		return LiveQuery{}, false
	}
	lq := LiveQuery{Limit: 20, Repo: q.Get("repo")}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			writeLiveError(w, http.StatusBadRequest, "limit must be 1..200")
			return LiveQuery{}, false
		}
		lq.Limit = n
	}
	if a := q.Get("after"); a != "" {
		after, err := strconv.ParseInt(a, 10, 64)
		if err != nil || after < 0 {
			writeLiveError(w, http.StatusBadRequest, "after must be a non-negative id")
			return LiveQuery{}, false
		}
		lq.After, lq.HasAfter = after, true
	}
	return lq, true
}

// ScreenReport reads POST /api/live/screen: POST only (405), JSON only (415),
// same origin (403), at most live.MaxScreenBytes (413), a valid report (400).
// On failure it has written the error and returns false.
func ScreenReport(w http.ResponseWriter, r *http.Request) (live.Screen, bool) {
	if r.Method != http.MethodPost {
		writeLiveError(w, http.StatusMethodNotAllowed, "method not allowed")
		return live.Screen{}, false
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		writeLiveError(w, http.StatusUnsupportedMediaType, "content-type must be application/json")
		return live.Screen{}, false
	}
	if !SameOrigin(r) {
		writeLiveError(w, http.StatusForbidden, "cross-origin screen reports are refused")
		return live.Screen{}, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, live.MaxScreenBytes)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeLiveError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("screen report larger than %d bytes", live.MaxScreenBytes))
			return live.Screen{}, false
		}
		writeLiveError(w, http.StatusBadRequest, err.Error())
		return live.Screen{}, false
	}
	sc, err := live.ParseScreen(raw)
	if err != nil {
		writeLiveError(w, http.StatusBadRequest, err.Error())
		return live.Screen{}, false
	}
	return sc, true
}

// WritePrivateJSON answers without CORS headers: another origin cannot read
// the live channel.
func WritePrivateJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func writeLiveError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
