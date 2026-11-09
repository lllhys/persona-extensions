package main

import "encoding/xml"

type feed struct {
	XMLName xml.Name `xml:"feed"`
	Entries []paper  `xml:"entry"`
}

type paper struct {
	ID        string   `xml:"id"`
	Title     string   `xml:"title"`
	Summary   string   `xml:"summary"`
	Published string   `xml:"published"`
	Updated   string   `xml:"updated"`
	Authors   []author `xml:"author"`
	Links     []link   `xml:"link"`
	Category  struct {
		Term string `xml:"term,attr"`
	} `xml:"category"`
	Comment string `xml:"comment"`
}

type author struct {
	Name string `xml:"name"`
}

type link struct {
	Href  string `xml:"href,attr"`
	Rel   string `xml:"rel,attr"`
	Type  string `xml:"type,attr"`
	Title string `xml:"title,attr"`
}
