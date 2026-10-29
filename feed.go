package main

import (
	"encoding/xml"
	"time"
)

type atomFeed struct {
	XMLName  xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	Title    string      `xml:"title"`
	Subtitle string      `xml:"subtitle"`
	ID       string      `xml:"id"`
	Updated  string      `xml:"updated"`
	Links    []atomLink  `xml:"link"`
	Entries  []atomEntry `xml:"entry"`
}

type atomLink struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
}

type atomEntry struct {
	ID        string     `xml:"id"`
	Title     string     `xml:"title"`
	Link      atomLink   `xml:"link"`
	Published string     `xml:"published"`
	Updated   string     `xml:"updated"`
	Author    atomAuthor `xml:"author"`
	Category  *atomCat   `xml:"category,omitempty"`
	Summary   atomText   `xml:"summary"`
}

type atomAuthor struct {
	Name string `xml:"name"`
}

type atomCat struct {
	Term string `xml:"term,attr"`
}

type atomText struct {
	Type string `xml:"type,attr"`
	Body string `xml:",chardata"`
}

func renderFeed(cfg Config, entries []Entry) ([]byte, error) {
	self := cfg.PublicURL + "/atom.xml"
	f := atomFeed{
		Title:    feedTitle,
		Subtitle: feedSubtitle,
		ID:       self,
		Links: []atomLink{
			{Rel: "alternate", Href: cfg.Origin + "/blog"},
			{Rel: "self", Href: self},
		},
	}
	var newest time.Time
	for _, e := range entries {
		if e.Modified.After(newest) {
			newest = e.Modified
		}
		ae := atomEntry{
			ID:        e.URL,
			Title:     e.Title,
			Link:      atomLink{Rel: "alternate", Href: e.URL},
			Published: e.Published.UTC().Format(time.RFC3339),
			Updated:   e.Modified.UTC().Format(time.RFC3339),
			Author:    atomAuthor{Name: e.Author},
			Summary:   atomText{Type: "text", Body: e.Summary},
		}
		if e.Tag != "" {
			ae.Category = &atomCat{Term: e.Tag}
		}
		f.Entries = append(f.Entries, ae)
	}
	f.Updated = newest.UTC().Format(time.RFC3339)
	b, err := xml.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), b...), nil
}
