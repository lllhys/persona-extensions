package main

type book struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	UpdatedAt   string `json:"updated_at"`
}

type page struct {
	ID        int64  `json:"id"`
	BookID    int64  `json:"book_id"`
	ChapterID *int64 `json:"chapter_id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	Markdown  string `json:"markdown"`
	HTML      string `json:"html"`
	Draft     bool   `json:"draft"`
	Template  bool   `json:"template"`
	UpdatedAt string `json:"updated_at"`
	URL       string `json:"url"`
	Book      *book  `json:"book"`
}

type searchResult struct {
	ID     int64  `json:"id"`
	BookID int64  `json:"book_id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	URL    string `json:"url"`
	Book   *book  `json:"book"`
}

type listPage[T any] struct {
	Data  []T `json:"data"`
	Total int `json:"total"`
}
