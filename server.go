package main

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

// feedCache renders the feed once per store generation instead of once per request.
type feedCache struct {
	mu  sync.Mutex
	gen uint64
	b   []byte
}

func (c *feedCache) get(cfg Config, store *Store) ([]byte, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	gen := store.Generation()
	if c.b != nil && c.gen == gen {
		return c.b, true, nil
	}
	entries := store.Snapshot()
	if len(entries) == 0 {
		return nil, false, nil
	}
	b, err := renderFeed(cfg, entries)
	if err != nil {
		return nil, true, err
	}
	c.gen, c.b = gen, b
	return b, true, nil
}

func newServer(cfg Config, store *Store) *http.Server {
	mux := http.NewServeMux()
	cache := &feedCache{}

	mux.HandleFunc("GET /atom.xml", func(w http.ResponseWriter, r *http.Request) {
		b, ready, err := cache.get(cfg, store)
		if !ready {
			w.Header().Set("Retry-After", "300")
			http.Error(w, "feed not ready", http.StatusServiceUnavailable)
			return
		}
		if err != nil {
			http.Error(w, "render error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
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

	var h http.Handler = mux
	if cfg.RateLimit > 0 {
		h = rateLimit(newLimiter(cfg.RateLimit), cfg.TrustedProxies, mux)
	}

	return &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
}
