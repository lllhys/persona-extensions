package main

type book struct {
	ID              int64  `json:"id"`
	Title           string `json:"title"`
	Author          string `json:"author"`
	Category        string `json:"category"`
	Source          string `json:"source"`
	NumHighlights   int    `json:"num_highlights"`
	LastHighlightAt string `json:"last_highlight_at"`
	Updated         string `json:"updated"`
	HighlightsURL   string `json:"highlights_url"`
	SourceURL       string `json:"source_url"`
	DocumentNote    string `json:"document_note"`
}

type highlight struct {
	ID            int64  `json:"id"`
	Text          string `json:"text"`
	Note          string `json:"note"`
	Location      int    `json:"location"`
	LocationType  string `json:"location_type"`
	HighlightedAt string `json:"highlighted_at"`
	Updated       string `json:"updated"`
	BookID        int64  `json:"book_id"`
	ReadwiseURL   string `json:"readwise_url"`
	Tags          []tag  `json:"tags"`
}

type tag struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type page[T any] struct {
	Count    int    `json:"count"`
	Next     string `json:"next"`
	Previous string `json:"previous"`
	Results  []T    `json:"results"`
}
