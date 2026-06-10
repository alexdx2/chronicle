package admin

// C4 view-model API — three levels of abstraction.
//
// C1 /api/c4/c1?domain=X  — system context: services, externals, infra
// C2 /api/c4/c2?domain=X  — container diagram: services, topics, lifted edges
// C3 /api/c4/c3?domain=X&service=<key|name> — component diagram: one service
//
// The view-model logic lives in the public graph/viewmodel package (shared
// with the MCP server and chronicle-pro). These handlers only parse params,
// call viewmodel.BuildXX and write JSON.

import (
	"errors"
	"net/http"

	"github.com/alexdx2/chronicle-core/graph/viewmodel"
)

// Type aliases — the response types moved to graph/viewmodel.
type (
	c1Response = viewmodel.C1
	c1Stats    = viewmodel.C1Stats
	c1External = viewmodel.External
	c1Infra    = viewmodel.Infra
	c1Edge     = viewmodel.C1Edge

	c2Response     = viewmodel.C2
	c2Service      = viewmodel.C2Service
	c2ServiceStats = viewmodel.C2ServiceStats
	c2Topic        = viewmodel.Topic
	c2Edge         = viewmodel.C2Edge

	c3Response     = viewmodel.C3
	c3Module       = viewmodel.Module
	c3Endpoint     = viewmodel.Endpoint
	c3Component    = viewmodel.Component
	c3InternalEdge = viewmodel.InternalEdge
	c3BoundaryOut  = viewmodel.BoundaryOut
	c3BoundaryIn   = viewmodel.BoundaryIn
	c3Boundary     = viewmodel.Boundary
)

func (s *Server) handleC1(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	if domain == "" {
		domain = s.domainFromManifest()
	}

	resp, err := viewmodel.BuildC1Manifest(s.getStore(), domain, s.manifestPath)
	if err != nil {
		httpError(w, err, 500)
		return
	}
	httpJSON(w, resp)
}

func (s *Server) handleC2(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	if domain == "" {
		domain = s.domainFromManifest()
	}

	resp, err := viewmodel.BuildC2Manifest(s.getStore(), domain, s.manifestPath)
	if err != nil {
		httpError(w, err, 500)
		return
	}
	httpJSON(w, resp)
}

func (s *Server) handleC3(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	if domain == "" {
		domain = s.domainFromManifest()
	}
	svcParam := r.URL.Query().Get("service")
	if svcParam == "" {
		httpError(w, errors.New("service parameter required"), 400)
		return
	}

	resp, err := viewmodel.BuildC3(s.getStore(), domain, svcParam)
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
