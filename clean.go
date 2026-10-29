package main

import (
	"fmt"
	"html"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/microcosm-cc/bluemonday"
)

var policy = bluemonday.UGCPolicy()

// cleanBody turns the raw draft-body inner HTML into sanitised, self-contained HTML.
func cleanBody(inner, articleURL string) (string, error) {
	base, err := url.Parse(articleURL)
	if err != nil {
		return "", err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader("<div id=\"root\">" + inner + "</div>"))
	if err != nil {
		return "", err
	}

	doc.Find("figure.draft-chart").Each(func(_ int, f *goquery.Selection) {
		caption := text(f.Find("figcaption"))
		repl := fmt.Sprintf(`<p><em>%s</em> (<a href="%s">interactive chart on the site</a>)</p>`,
			html.EscapeString(caption), html.EscapeString(articleURL))
		f.ReplaceWithHtml(repl)
	})

	root := doc.Find("#root")
	root.Find("[href]").Each(func(_ int, s *goquery.Selection) { absolutise(s, "href", base) })
	root.Find("[src]").Each(func(_ int, s *goquery.Selection) { absolutise(s, "src", base) })
	root.Find("[srcset]").Each(func(_ int, s *goquery.Selection) {
		v, _ := s.Attr("srcset")
		parts := strings.Split(v, ",")
		for i, p := range parts {
			f := strings.Fields(p)
			if len(f) == 0 {
				continue
			}
			f[0] = resolve(base, f[0])
			parts[i] = strings.Join(f, " ")
		}
		s.SetAttr("srcset", strings.Join(parts, ", "))
	})

	out, err := root.Html()
	if err != nil {
		return "", err
	}
	out = policy.Sanitize(out)
	out += fmt.Sprintf(`<p>Originally published at <a href="%s">foodtruckbench.com</a></p>`, html.EscapeString(articleURL))
	return out, nil
}

func absolutise(s interface {
	Attr(string) (string, bool)
	SetAttr(string, string) *goquery.Selection
}, attr string, base *url.URL) {
	if v, ok := s.Attr(attr); ok {
		s.SetAttr(attr, resolve(base, v))
	}
}

func resolve(base *url.URL, ref string) string {
	u, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return ref
	}
	return base.ResolveReference(u).String()
}
