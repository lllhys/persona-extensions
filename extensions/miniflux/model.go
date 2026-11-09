package main

type entry struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Author      string `json:"author"`
	Content     string `json:"content"`
	PublishedAt string `json:"published_at"`
	Status      string `json:"status"`
	Starred     bool   `json:"starred"`
	ReadingTime int    `json:"reading_time"`
	Feed        struct {
		ID       int64  `json:"id"`
		Title    string `json:"title"`
		SiteURL  string `json:"site_url"`
		Category struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"category"`
	} `json:"feed"`
}

type entryPage struct {
	Total   int     `json:"total"`
	Entries []entry `json:"entries"`
}
