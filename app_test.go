package main

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func testScraper() *Scraper {
	return NewScraper(Config{Origin: "https://foodtruckbench.com", PublicURL: "https://x.test"})
}

func noWarn(string, ...any) {}

func TestConfig(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if _, err := loadConfig(env(nil)); err == nil {
		t.Error("missing PUBLIC_URL should fail")
	}
	if _, err := loadConfig(env(map[string]string{"PUBLIC_URL": "https://a.test", "POLL_INTERVAL": "banana"})); err == nil {
		t.Error("bad duration should fail")
	}
	if _, err := loadConfig(env(map[string]string{"PUBLIC_URL": "https://a.test", "POLL_INTERVAL": "10s"})); err == nil {
		t.Error("short duration should fail")
	}
	c, err := loadConfig(env(map[string]string{"PUBLIC_URL": "https://a.test/"}))
	if err != nil || c.PublicURL != "https://a.test" || c.PollInterval != 3*time.Hour || c.Origin != "https://foodtruckbench.com" {
		t.Errorf("defaults wrong: %+v %v", c, err)
	}
}

func TestParseListing(t *testing.T) {
	cards, err := testScraper().parseListing(readFixture(t, "listing.html"), noWarn)
	if err != nil || len(cards) < 10 {
		t.Fatalf("cards=%d err=%v", len(cards), err)
	}
	c := cards[0]
	if c.Slug == "" || c.Title == "" || c.Author == "" || c.Tag == "" || c.Excerpt == "" || !strings.HasPrefix(c.URL, "https://foodtruckbench.com/blog/") {
		t.Errorf("incomplete card: %+v", c)
	}
}

func TestParseListingSkipsBadCards(t *testing.T) {
	html := `<div class="blog-cards">
	<a class="blog-card" href="/blog/ok"><h2 class="blog-card-title">Ok</h2></a>
	<a class="blog-card" href="/blog/"><h2 class="blog-card-title">No slug</h2></a>
	<a class="blog-card" href="/blog/notitle"></a>
	<a class="blog-card" href="https://evil.test/blog/x"><h2 class="blog-card-title">Off</h2></a>
	<a class="blog-card" href="/other/x"><h2 class="blog-card-title">Elsewhere</h2></a></div>`
	cards, _ := testScraper().parseListing([]byte(html), noWarn)
	if len(cards) != 1 || cards[0].Slug != "ok" {
		t.Errorf("got %+v", cards)
	}
}

func TestParseArticleFixtures(t *testing.T) {
	for _, f := range []string{"article-grok-4-7.html", "article-minimax-m3-1-flash.html"} {
		a, err := parseArticle(readFixture(t, f))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if a.Published.IsZero() || a.Modified.IsZero() {
			t.Errorf("%s: dates missing", f)
		}
	}
}

// The live claude-opus-5 page has no BlogPosting JSON-LD, only a month-level header date.
func TestParseArticleHeaderDateFallback(t *testing.T) {
	a, err := parseArticle(readFixture(t, "article-claude-opus-5.html"))
	if err != nil || !a.Approx || a.Published.Day() != 1 || a.Published.Month() != time.July || !a.Published.Equal(a.Modified) {
		t.Errorf("%+v %v", a, err)
	}
	if a, _ := parseArticle(readFixture(t, "article-grok-4-7.html")); a.Approx {
		t.Error("JSON-LD article must not be approximate")
	}
	if d, err := parseHeaderDate("23 October 2026"); err != nil || d.Day() != 23 {
		t.Errorf("%v %v", d, err)
	}
}

func TestParseArticleOlderTemplate(t *testing.T) {
	h := `<article class="blog-article"><header class="blog-header"><span class="blog-date">February 2026</span></header>
	<section class="blog-section"><h2>One</h2><p>first</p></section><section class="blog-section"><p>second</p></section>
	<footer><p>related junk</p></footer></article>`
	a, err := parseArticle([]byte(h))
	if err != nil || !a.Approx || a.Published.Month() != time.February {
		t.Fatalf("%+v %v", a, err)
	}
}

func TestParseArticleFailures(t *testing.T) {
	body := `<section class="blog-section draft-body"><p>hi</p></section>`
	ld := func(s string) string { return `<script type="application/ld+json">` + s + `</script>` }
	cases := map[string]string{
		"no jsonld":   body,
		"malformed":   ld(`{not json`) + body,
		"bad date":    ld(`{"@type":"BlogPosting","datePublished":"yesterday","dateModified":"x"}`) + body,
		"missing mod": ld(`{"@type":"BlogPosting","datePublished":"2026-01-01T00:00:00Z"}`) + body,
		"wrong type":  ld(`{"@type":"Article","datePublished":"2026-01-01T00:00:00Z","dateModified":"2026-01-01T00:00:00Z"}`) + body,
	}
	for name, h := range cases {
		if _, err := parseArticle([]byte(h)); err == nil {
			t.Errorf("%s: expected failure", name)
		}
	}
	graph := ld(`{"@graph":[{"@type":"WebSite"},{"@type":["BlogPosting"],"datePublished":"2026-01-01T00:00:00Z","dateModified":"2026-01-02T00:00:00Z"}]}`) + body
	if a, err := parseArticle([]byte(graph)); err != nil || a.Modified.Day() != 2 {
		t.Errorf("@graph: %+v %v", a, err)
	}
}

func TestStore(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "d.json")
	if n := len(OpenStore(p).Snapshot()); n != 0 {
		t.Fatal("missing file should be empty")
	}
	s := OpenStore(p)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	es := []Entry{{Slug: "a", Published: t0}, {Slug: "c", Published: t0.Add(time.Hour)}, {Slug: "b", Published: t0}}
	if err := s.Replace(es); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p + ".tmp"); err == nil {
		t.Error("tmp file left behind")
	}
	got := OpenStore(p).Snapshot()
	if len(got) != 3 || got[0].Slug != "c" || got[1].Slug != "a" || got[2].Slug != "b" {
		t.Errorf("round trip/order: %+v", got)
	}
	os.WriteFile(p, []byte("{garbage"), 0o644)
	if n := len(OpenStore(p).Snapshot()); n != 0 {
		t.Error("corrupt file should start empty")
	}
}

// fakeSite serves a listing of the given slugs plus per-slug articles, counting hits.
type fakeSite struct {
	*httptest.Server
	slugs        []string
	listingCode  int
	listingBody  string
	failArticles map[string]bool
	articleHits  atomic.Int32
	modified     string
	noLD         bool
	titleSuffix  string
	published    string
}

func newFakeSite(slugs ...string) *fakeSite {
	f := &fakeSite{slugs: slugs, listingCode: 200, failArticles: map[string]bool{}, modified: "2026-02-01T00:00:00Z", published: "2026-01-01T00:00:00Z"}
	mux := http.NewServeMux()
	mux.HandleFunc("/blog", func(w http.ResponseWriter, r *http.Request) {
		if f.listingCode != 200 {
			http.Error(w, "boom", f.listingCode)
			return
		}
		if f.listingBody != "" {
			w.Write([]byte(f.listingBody))
			return
		}
		var sb strings.Builder
		sb.WriteString(`<div class="blog-cards">`)
		for _, s := range f.slugs {
			sb.WriteString(`<a class="blog-card" href="/blog/` + s + `"><span class="blog-card-tag">T</span><h2 class="blog-card-title">Title ` + s + f.titleSuffix + `</h2><p class="blog-card-excerpt">ex</p><span class="blog-card-author">Au</span></a>`)
		}
		sb.WriteString(`</div>`)
		w.Write([]byte(sb.String()))
	})
	mux.HandleFunc("/blog/", func(w http.ResponseWriter, r *http.Request) {
		f.articleHits.Add(1)
		slug := strings.TrimPrefix(r.URL.Path, "/blog/")
		if f.failArticles[slug] {
			http.Error(w, "boom", 500)
			return
		}
		if f.noLD {
			w.Write([]byte(`<header class="blog-header"><span class="blog-date">July 2026</span></header><section class="blog-section draft-body"><p>body of ` + slug + `</p></section>`))
			return
		}
		w.Write([]byte(`<script type="application/ld+json">{"@type":"BlogPosting","datePublished":"` + f.published + `","dateModified":"` + f.modified + `"}</script>
		<section class="blog-section draft-body"><p>body of ` + slug + `</p></section>`))
	})
	f.Server = httptest.NewServer(mux)
	return f
}

func newTestPoller(t *testing.T, f *fakeSite) (*Poller, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "d.json")
	cfg := Config{PublicURL: "https://x.test", SourceURL: f.URL + "/blog", Origin: f.URL, PollInterval: time.Hour, DataFile: path}
	p := NewPoller(cfg, OpenStore(path), NewScraper(cfg))
	p.delay = 0
	return p, path
}

func TestPollerLifecycle(t *testing.T) {
	f := newFakeSite("a", "b", "c")
	defer f.Close()
	p, path := newTestPoller(t, f)
	ctx := context.Background()
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return now }

	p.Poll(ctx) // 1: populates
	if n := len(p.store.Snapshot()); n != 3 || f.articleHits.Load() != 3 {
		t.Fatalf("entries=%d hits=%d", n, f.articleHits.Load())
	}

	now = now.Add(time.Hour)
	p.Poll(ctx) // 2: within 24h, no article requests
	if f.articleHits.Load() != 3 {
		t.Errorf("unexpected refetch, hits=%d", f.articleHits.Load())
	}

	now = now.Add(100 * time.Hour)
	p.Poll(ctx) // unchanged cards are never refetched, however old
	if f.articleHits.Load() != 3 {
		t.Errorf("unexpected refetch of unchanged cards, hits=%d", f.articleHits.Load())
	}

	// 7: refetch doesn't change Published, but updates Modified
	f.published, f.modified = "2030-01-01T00:00:00Z", "2026-02-15T00:00:00Z"
	f.titleSuffix = " v2"
	p.Poll(ctx) // 3: cards changed, refetched
	if f.articleHits.Load() != 6 {
		t.Errorf("expected refetch, hits=%d", f.articleHits.Load())
	}
	for _, e := range p.store.Snapshot() {
		if e.Published.Year() != 2026 || e.Modified.Day() != 15 {
			t.Errorf("published/modified wrong: %+v", e)
		}
	}

	// 4: bad listings leave the store file byte-identical
	before, _ := os.ReadFile(path)
	for name, set := range map[string]func(){
		"500":     func() { f.listingCode = 500 },
		"empty":   func() { f.listingCode, f.listingBody = 200, " " },
		"garbage": func() { f.listingBody = "<html><body>Just a moment...</body></html>" },
	} {
		set()
		now = now.Add(48 * time.Hour)
		p.Poll(ctx)
		if after, _ := os.ReadFile(path); string(after) != string(before) {
			t.Errorf("%s: store changed", name)
		}
	}
	f.listingCode, f.listingBody = 200, ""

	// 6: vanished entries retained
	f.slugs = []string{"a"}
	now = now.Add(time.Hour)
	p.Poll(ctx)
	if n := len(p.store.Snapshot()); n != 3 {
		t.Errorf("vanished entries dropped: %d", n)
	}
}

func TestPollerApproxDateUpgrade(t *testing.T) {
	f := newFakeSite("a")
	defer f.Close()
	p, _ := newTestPoller(t, f)
	ctx := context.Background()
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return now }
	f.noLD = true
	p.Poll(ctx)
	e := p.store.Snapshot()[0]
	if !e.DateApprox || e.Published.Day() != 1 || e.Published.Month() != time.July {
		t.Fatalf("expected approx date: %+v", e)
	}
	f.noLD = false
	f.titleSuffix = " v2" // card change triggers the refetch
	now = now.Add(30 * time.Hour)
	p.Poll(ctx)
	e = p.store.Snapshot()[0]
	if e.DateApprox || e.Published.Month() != time.January {
		t.Errorf("expected upgrade to precise date: %+v", e)
	}
}

func TestPollerArticleFailure(t *testing.T) {
	f := newFakeSite("a", "b", "c")
	defer f.Close()
	f.failArticles["b"] = true
	p, _ := newTestPoller(t, f)
	ctx := context.Background()
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return now }

	p.Poll(ctx)
	if n := len(p.store.Snapshot()); n != 2 {
		t.Fatalf("one failure should not block others, got %d", n)
	}
	delete(f.failArticles, "b") // retried next poll
	now = now.Add(time.Hour)
	p.Poll(ctx)
	if n := len(p.store.Snapshot()); n != 3 {
		t.Errorf("failed article not retried, got %d", n)
	}

	// refetch failure keeps the old entry
	f.failArticles["a"] = true
	f.titleSuffix = " v2"
	now = now.Add(30 * time.Hour)
	p.Poll(ctx)
	for _, e := range p.store.Snapshot() {
		if e.Slug == "a" && e.Published.IsZero() {
			t.Errorf("entry discarded: %+v", e)
		}
	}
}

func TestFeedAndServer(t *testing.T) {
	cfg := Config{PublicURL: "https://feed.test", Origin: "https://foodtruckbench.com"}
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "d.json")
	st := OpenStore(path)
	h := newServer(cfg, st).Handler

	for _, p := range []string{"/atom.xml", "/healthz"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != 503 {
			t.Errorf("%s empty: %d", p, rec.Code)
		}
	}

	st.Replace([]Entry{
		{Slug: "old", URL: "https://foodtruckbench.com/blog/old", Title: "Old & <b>", Published: t0, Modified: t0, Tag: "T"},
		{Slug: "new", URL: "https://foodtruckbench.com/blog/new", Title: "New", Published: t0.Add(time.Hour), Modified: t0.Add(2 * time.Hour)},
	})
	get := func() (*httptest.ResponseRecorder, []byte) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/atom.xml", nil))
		return rec, rec.Body.Bytes()
	}
	rec, b1 := get()
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/atom+xml") {
		t.Fatalf("code=%d ct=%s", rec.Code, rec.Header().Get("Content-Type"))
	}
	var parsed struct {
		ID      string `xml:"id"`
		Updated string `xml:"updated"`
		Entries []struct {
			ID    string `xml:"id"`
			Title string `xml:"title"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(b1, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.ID != "https://feed.test/atom.xml" || len(parsed.Entries) != 2 || parsed.Entries[0].ID != "https://foodtruckbench.com/blog/new" {
		t.Errorf("bad feed: %+v", parsed)
	}
	if parsed.Entries[1].Title != "Old & <b>" {
		t.Errorf("escaping round-trip: %+v", parsed.Entries)
	}
	if parsed.Updated != "2026-01-01T02:00:00Z" {
		t.Errorf("updated=%s", parsed.Updated)
	}
	if _, b2 := get(); string(b1) != string(b2) {
		t.Error("feed not stable across renders")
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 {
		t.Errorf("healthz=%d", rec.Code)
	}
}

func TestClientAddr(t *testing.T) {
	trusted, _ := parsePrefixes(defaultTrustedProxies)
	cases := []struct{ remote, xff, want string }{
		{"203.0.113.9:1234", "1.2.3.4", "203.0.113.9"},               // untrusted peer: XFF ignored
		{"172.18.0.2:5555", "198.51.100.7", "198.51.100.7"},          // via proxy
		{"172.18.0.2:5555", "6.6.6.6, 198.51.100.7", "198.51.100.7"}, // spoofed left entry ignored
		{"172.18.0.2:5555", "198.51.100.7, 10.0.0.5", "198.51.100.7"},
		{"172.18.0.2:5555", "", "172.18.0.2"},
		{"172.18.0.2:5555", "garbage", "172.18.0.2"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/atom.xml", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := clientAddr(r, trusted).String(); got != c.want {
			t.Errorf("remote=%s xff=%q: got %s, want %s", c.remote, c.xff, got, c.want)
		}
	}
}

func TestRateLimit(t *testing.T) {
	trusted, _ := parsePrefixes(defaultTrustedProxies)
	l := newLimiter(60)
	now := time.Now()
	l.now = func() time.Time { return now }
	h := rateLimit(l, trusted, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	do := func(xff string) int {
		r := httptest.NewRequest("GET", "/atom.xml", nil)
		r.RemoteAddr = "172.18.0.2:1"
		r.Header.Set("X-Forwarded-For", xff)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for i := 0; i < rateBurst; i++ {
		if c := do("198.51.100.7"); c != 200 {
			t.Fatalf("request %d: %d", i, c)
		}
	}
	if c := do("198.51.100.7"); c != 429 {
		t.Fatalf("over burst: got %d, want 429", c)
	}
	if c := do("198.51.100.8"); c != 200 {
		t.Fatalf("other client should be unaffected, got %d", c)
	}
	now = now.Add(2 * time.Second)
	if c := do("198.51.100.7"); c != 200 {
		t.Fatalf("after refill: got %d", c)
	}
}
