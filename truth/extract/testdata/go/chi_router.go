package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/health", health)
	r.Route("/api", func(r chi.Router) {
		r.Get("/items", listItems)
		r.With(auth).Post("/items", createItem)
		r.Route("/items/{id:[0-9]+}", func(r chi.Router) {
			r.Get("/", getItem)
			r.Delete("/", deleteItem)
		})
		r.Group(func(r chi.Router) {
			r.Use(admin)
			r.Put("/settings", putSettings)
		})
		r.Method("PATCH", "/items", patchItems)
	})
	admin := chi.NewRouter()
	admin.Get("/stats", stats)
	r.Mount("/admin", admin)
	r.Mount("/users", userRoutes())
	r.Route("/v2", func(v chi.Router) { v.Get("/ping", ping) })
	r.Get(
		"/multi",
		multi,
	)
	// r.Get("/commented", nope)
	log("r.Get(\"/in-string\", x)")
	r.Handle("/anymethod", h)
	return r
}

func userRoutes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", listUsers)
	r.Get("/{userID}", getUser)
	return r
}

// Sub receives its router; it is mounted in another file.
func Sub(r chi.Router) {
	r.Get("/orphan", orphan)
}

func Detached() chi.Router {
	r := chi.NewRouter()
	r.Get("/detached", d)
	return r
}
