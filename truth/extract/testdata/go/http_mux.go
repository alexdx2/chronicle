package main

import "net/http"

func routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items", list)
	mux.HandleFunc("POST /items/{id}", create)
	mux.Handle("DELETE /items/{id}/{rest...}", del)
	mux.HandleFunc("GET /{$}", index)
	mux.HandleFunc("GET example.com/hosted", hosted)
	mux.HandleFunc("/no-method", nm)
	// mux.HandleFunc("GET /commented", c)
	other.HandleFunc("GET /not-a-mux", x)
	http.HandleFunc("PUT /default-mux", dm)
	return mux
}

func note() { println("mux.HandleFunc(\"GET /in-string\", h)") }
