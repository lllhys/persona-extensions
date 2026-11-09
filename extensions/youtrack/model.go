package main

type project struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ShortName string `json:"shortName"`
	Archived  bool   `json:"archived"`
}

type issue struct {
	ID            string  `json:"id"`
	IDReadable    string  `json:"idReadable"`
	Summary       string  `json:"summary"`
	Description   string  `json:"description"`
	Resolved      *int64  `json:"resolved"`
	Updated       int64   `json:"updated"`
	Project       project `json:"project"`
	Reporter      *user   `json:"reporter"`
	CommentsCount int     `json:"commentsCount"`
}

type user struct {
	ID    string `json:"id"`
	Login string `json:"login"`
	Name  string `json:"name"`
}

type comment struct {
	ID      string `json:"id"`
	Text    string `json:"text"`
	Author  *user  `json:"author"`
	Created int64  `json:"created"`
	Deleted bool   `json:"deleted"`
}
