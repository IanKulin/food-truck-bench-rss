package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const (
	selCard     = "div.blog-cards > a.blog-card"
	selTag      = ".blog-card-tag"
	selTitle    = ".blog-card-title"
	selExcerpt  = ".blog-card-excerpt"
	selAuthor   = ".blog-card-author"
	selBody     = "section.draft-body"
	selSections = "article.blog-article > section.blog-section" // older template: body split across sections
	selLDJSON   = `script[type="application/ld+json"]`
	selHdrDate  = "header.blog-header .blog-date"
)

type Card struct {
	Slug, URL, Title, Excerpt, Author, Tag string
}

type Scraper struct {
	client    *http.Client
	userAgent string
	origin    *url.URL
}

func NewScraper(cfg Config) *Scraper {
	o, _ := url.Parse(cfg.Origin)
	return &Scraper{
		client: &http.Client{
			Timeout: fetchTimeout,
			// Never follow a redirect off the source host (SSRF / internal-network pivot).
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("stopped after 5 redirects")
				}
				if req.URL.Host != o.Host {
					return fmt.Errorf("redirect to foreign host %q refused", req.URL.Host)
				}
				return nil
			},
		},
		userAgent: "foodtruckbench-rss/1.0 (+" + cfg.PublicURL + ")",
		origin:    o,
	}
}

func (s *Scraper) get(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", s.userAgent)
	req.Header.Set("Accept", "text/html")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d", u, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
}

// parseListing extracts cards; cards that are invalid or point off-site are skipped.
func (s *Scraper) parseListing(html []byte, warn func(string, ...any)) ([]Card, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(html)))
	if err != nil {
		return nil, err
	}
	var cards []Card
	doc.Find(selCard).Each(func(_ int, sel *goquery.Selection) {
		href, _ := sel.Attr("href")
		ref, err := url.Parse(href)
		if err != nil {
			warn("skipping card: bad href", "href", href)
			return
		}
		abs := s.origin.ResolveReference(ref)
		if abs.Host != s.origin.Host || !strings.HasPrefix(abs.Path, "/blog/") {
			warn("skipping card: unexpected href", "href", href)
			return
		}
		slug := strings.Trim(strings.TrimPrefix(abs.Path, "/blog/"), "/")
		title := text(sel.Find(selTitle))
		if slug == "" || strings.Contains(slug, "/") || title == "" {
			warn("skipping card: no slug or title", "href", href)
			return
		}
		cards = append(cards, Card{
			Slug:    slug,
			URL:     s.origin.String() + "/blog/" + slug,
			Title:   title,
			Excerpt: text(sel.Find(selExcerpt)),
			Author:  text(sel.Find(selAuthor)),
			Tag:     text(sel.Find(selTag)),
		})
	})
	return cards, nil
}

func text(s *goquery.Selection) string {
	return strings.Join(strings.Fields(s.First().Text()), " ")
}

type Article struct {
	ContentHTML         string
	Published, Modified time.Time
	Approx              bool // dates are the page-header date at month precision
}

func parseArticle(html []byte, articleURL string) (Article, error) {
	var a Article
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(html)))
	if err != nil {
		return a, err
	}
	a.Published, a.Modified, err = extractDates(doc)
	if err != nil {
		// Many articles carry no BlogPosting JSON-LD; fall back to the header date.
		d, herr := parseHeaderDate(text(doc.Find(selHdrDate)))
		if herr != nil {
			return a, fmt.Errorf("%w; %v", err, herr)
		}
		a.Published, a.Modified, a.Approx = d, d, true
	}
	inner, err := articleBodyHTML(doc)
	if err != nil {
		return a, err
	}
	a.ContentHTML, err = cleanBody(inner, articleURL)
	return a, err
}

func extractDates(doc *goquery.Document) (pub, mod time.Time, err error) {
	var found bool
	doc.Find(selLDJSON).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		var v any
		if json.Unmarshal([]byte(s.Text()), &v) != nil {
			return true
		}
		if m := findBlogPosting(v); m != nil {
			p, e1 := time.Parse(time.RFC3339, str(m["datePublished"]))
			d, e2 := time.Parse(time.RFC3339, str(m["dateModified"]))
			if e1 == nil && e2 == nil {
				pub, mod, found = p.UTC(), d.UTC(), true
				return false
			}
		}
		return true
	})
	if !found {
		err = fmt.Errorf("no BlogPosting JSON-LD with valid dates")
	}
	return
}

func findBlogPosting(v any) map[string]any {
	switch t := v.(type) {
	case []any:
		for _, x := range t {
			if m := findBlogPosting(x); m != nil {
				return m
			}
		}
	case map[string]any:
		if hasType(t["@type"], "BlogPosting") {
			return t
		}
		return findBlogPosting(t["@graph"])
	}
	return nil
}

func hasType(v any, want string) bool {
	switch t := v.(type) {
	case string:
		return t == want
	case []any:
		for _, x := range t {
			if s, ok := x.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}

func str(v any) string { s, _ := v.(string); return s }

// parseHeaderDate reads "July 2026" (first of the month) or "23 October 2026", as UTC.
func parseHeaderDate(s string) (time.Time, error) {
	for _, layout := range []string{"2 January 2006", "January 2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable header date %q", s)
}

func articleBodyHTML(doc *goquery.Document) (string, error) {
	if body := doc.Find(selBody).First(); body.Length() > 0 {
		return body.Html()
	}
	var sb strings.Builder
	var err error
	doc.Find(selSections).Each(func(_ int, s *goquery.Selection) {
		h, e := goquery.OuterHtml(s)
		if e != nil {
			err = e
		}
		sb.WriteString(h)
	})
	if err != nil {
		return "", err
	}
	if sb.Len() == 0 {
		return "", fmt.Errorf("article body not found (%q or %q)", selBody, selSections)
	}
	return sb.String(), nil
}
