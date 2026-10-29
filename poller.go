package main

import (
	"context"
	"log/slog"
	"time"
)

type Poller struct {
	cfg   Config
	store *Store
	sc    *Scraper
	now   func() time.Time
	delay time.Duration
}

func NewPoller(cfg Config, store *Store, sc *Scraper) *Poller {
	return &Poller{cfg: cfg, store: store, sc: sc, now: time.Now, delay: articleDelay}
}

// Run polls immediately, then every PollInterval until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) {
	p.Poll(ctx)
	t := time.NewTicker(p.cfg.PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.Poll(ctx)
		}
	}
}

func (p *Poller) Poll(ctx context.Context) {
	start := p.now()
	body, err := p.sc.get(ctx, p.cfg.SourceURL)
	if err != nil {
		slog.Error("listing fetch failed", "err", err)
		return
	}
	cards, err := p.sc.parseListing(body, slog.Warn)
	if err != nil || len(cards) == 0 {
		slog.Error("listing has no valid cards, store untouched", "err", err)
		return
	}

	existing := p.store.Snapshot()
	bySlug := make(map[string]int, len(existing))
	for i, e := range existing {
		bySlug[e.Slug] = i
	}
	next := append([]Entry(nil), existing...)

	var nNew, nRefreshed, nFailed int
	changed := false
	fetched := false
	for _, c := range cards {
		i, known := bySlug[c.Slug]
		var e Entry
		if known {
			e = next[i]
		} else {
			e = Entry{Slug: c.Slug, URL: c.URL}
		}
		before := e

		e.Title, e.Summary, e.Author, e.Tag = c.Title, c.Excerpt, c.Author, c.Tag

		// Be a good neighbour: a known article is only refetched when its
		// listing card changed, which may miss silent edits.
		cardChanged := known && (e.Title != before.Title || e.Summary != before.Summary ||
			e.Author != before.Author || e.Tag != before.Tag)
		if !known || cardChanged {
			if fetched {
				select {
				case <-ctx.Done():
					return
				case <-time.After(p.delay):
				}
			}
			fetched = true
			a, err := p.fetchArticle(ctx, c.URL)
			if err != nil {
				slog.Warn("article fetch failed", "slug", c.Slug, "err", err)
				nFailed++
				if !known {
					continue // nothing to store until the article succeeds
				}
			} else {
				e.Modified = a.Modified
				// Published is frozen once set, except an approximate date may be
				// upgraded by a precise one.
				if e.Published.IsZero() {
					e.Published, e.DateApprox = a.Published, a.Approx
				} else if e.DateApprox && !a.Approx {
					e.Published, e.DateApprox = a.Published, false
				}
				e.FetchedAt = p.now().UTC()
				if known {
					nRefreshed++
				} else {
					nNew++
				}
			}
		}

		if !known {
			bySlug[c.Slug] = len(next)
			next = append(next, e)
			changed = true
		} else if e != before {
			next[i] = e
			changed = true
		}
	}

	if changed {
		if err := p.store.Replace(next); err != nil {
			slog.Error("saving store failed", "err", err)
			return
		}
	}
	p.store.MarkPolled(p.now())
	slog.Info("poll complete", "cards", len(cards), "new", nNew, "refreshed", nRefreshed,
		"failed", nFailed, "dur", p.now().Sub(start).Round(time.Millisecond))
}

func (p *Poller) fetchArticle(ctx context.Context, u string) (Article, error) {
	b, err := p.sc.get(ctx, u)
	if err != nil {
		return Article{}, err
	}
	return parseArticle(b)
}
