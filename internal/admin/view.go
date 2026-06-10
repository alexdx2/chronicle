package admin

// Unified view-algebra API.
//
//	GET  /api/view?spec=<base64url-json>  — spec encoded in the URL (shareable)
//	POST /api/view                        — spec as JSON body
//
// Both evaluate viewmodel.BuildView. The legacy /api/c4/* endpoints stay
// untouched as preset aliases.

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/alexdx2/chronicle-core/graph/viewmodel"
)

func (s *Server) handleView(w http.ResponseWriter, r *http.Request) {
	var spec viewmodel.ViewSpec

	switch r.Method {
	case http.MethodGet:
		raw := r.URL.Query().Get("spec")
		if raw == "" {
			httpError(w, errors.New("spec query parameter required (base64url-encoded JSON ViewSpec)"), 400)
			return
		}
		data, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(raw, "="))
		if err != nil {
			httpError(w, errors.New("spec is not valid base64url"), 400)
			return
		}
		if err := json.Unmarshal(data, &spec); err != nil {
			httpError(w, err, 400)
			return
		}
	case http.MethodPost:
		if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
			httpError(w, err, 400)
			return
		}
	default:
		httpError(w, errors.New("method not allowed"), http.StatusMethodNotAllowed)
		return
	}

	if spec.Scope.Domain == "" {
		spec.Scope.Domain = s.domainFromManifest()
	}

	resp, err := viewmodel.BuildView(s.getStore(), spec)
	if err != nil {
		var nf *viewmodel.NotFoundError
		if errors.As(err, &nf) {
			httpError(w, err, 404)
			return
		}
		httpError(w, err, 500)
		return
	}
	httpJSON(w, resp)
}
