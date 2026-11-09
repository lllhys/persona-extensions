package main

type link struct {
	ID          string   `json:"id"`
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	Summary     string   `json:"summary"`
	Author      string   `json:"author"`
	Tags        []string `json:"tags"`
	WordCount   int      `json:"wordCount"`
	Starred     bool     `json:"starred"`
	Highlighted bool     `json:"highlighted"`
	AddedAt     string   `json:"addedAt"`
	ModifiedAt  string   `json:"modifiedAt"`
	ReadAt      *string  `json:"readAt"`
}

type highlight struct {
	ID        string  `json:"id"`
	LinkID    string  `json:"linkID"`
	Content   string  `json:"content"`
	Note      *string `json:"note"`
	CreatedAt string  `json:"createdAt"`
}

type linkPage struct {
	Data    []link `json:"data"`
	HasMore bool   `json:"hasMore"`
}

type highlightPage struct {
	Data    []highlight `json:"data"`
	HasMore bool        `json:"hasMore"`
}
