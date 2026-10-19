package api

import "github.com/go-chi/chi/v5"

func testRoutes() {
	r := chi.NewRouter()
	r.Get("/only-in-test", nil)
}
