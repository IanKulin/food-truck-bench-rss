package main

import (
	"fmt"
	"net/http"
	"time"
)

func newServer(cfg Config, store *Store) *http.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /atom.xml", func(w http.ResponseWriter, r *http.Request) {
		entries := store.Snapshot()
		if len(entries) == 0 {
			w.Header().Set("Retry-After", "300")
			http.Error(w, "feed not ready", http.StatusServiceUnavailable)
			return
		}
		b, err := renderFeed(cfg, entries)
		if err != nil {
			http.Error(w, "render error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=900")
		w.Write(b)
	})

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		n := len(store.Snapshot())
		if n == 0 {
			http.Error(w, "no entries yet", http.StatusServiceUnavailable)
			return
		}
		last := "never"
		if t := store.LastPoll(); !t.IsZero() {
			last = t.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(w, "ok entries=%d last_poll=%s\n", n, last)
	})

	return &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}
