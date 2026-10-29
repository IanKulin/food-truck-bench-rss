package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	cfg, err := loadConfig(getenvOS)
	if err != nil {
		slog.Error("invalid configuration", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store := OpenStore(cfg.DataFile)
	poller := NewPoller(cfg, store, NewScraper(cfg))
	pollDone := make(chan struct{})
	go func() {
		defer close(pollDone)
		poller.Run(ctx)
	}()

	srv := newServer(cfg, store)
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()

	slog.Info("listening", "addr", cfg.ListenAddr, "public_url", cfg.PublicURL, "entries", len(store.Snapshot()))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server failed", "err", err)
		os.Exit(1)
	}
	<-pollDone
}
