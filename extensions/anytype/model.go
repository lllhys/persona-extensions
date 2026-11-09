package main

type space struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type objectType struct {
	ID      string `json:"id"`
	TypeKey string `json:"type_key"`
	Name    string `json:"name"`
	Layout  string `json:"layout"`
}

type knowledgeObject struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Snippet  string     `json:"snippet"`
	Body     string     `json:"body"`
	Markdown string     `json:"markdown"`
	SpaceID  string     `json:"space_id"`
	Layout   string     `json:"layout"`
	Type     objectType `json:"type"`
	Blocks   []block    `json:"blocks"`
}

type block struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Text *struct {
		Text string `json:"text"`
	} `json:"text"`
}

type page[T any] struct {
	Data       []T `json:"data"`
	Pagination struct {
		Offset  int  `json:"offset"`
		Limit   int  `json:"limit"`
		Total   int  `json:"total"`
		HasMore bool `json:"has_more"`
	} `json:"pagination"`
}
