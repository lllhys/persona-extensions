package main

import (
	"encoding/json"
	"strconv"
	"strings"
)

// raindrop is the subset of the public API's bookmark used by Persona.
// The ID stays an integer through the exact JSON decoder, not a float64.
type raindrop struct {
	ID         int64       `json:"_id"`
	Title      string      `json:"title"`
	Excerpt    string      `json:"excerpt"`
	Note       string      `json:"note"`
	Link       string      `json:"link"`
	Domain     string      `json:"domain"`
	Created    string      `json:"created"`
	LastUpdate string      `json:"lastUpdate"`
	Tags       []string    `json:"tags"`
	Important  bool        `json:"important"`
	Broken     bool        `json:"broken"`
	Collection collection  `json:"collection"`
	Highlights []highlight `json:"highlights"`
}

type highlight struct {
	ID      string `json:"_id"`
	Text    string `json:"text"`
	Note    string `json:"note"`
	Color   string `json:"color"`
	Created string `json:"created"`
}

type collection struct {
	ID     int64  `json:"_id"`
	Title  string `json:"title"`
	Parent *struct {
		ID int64 `json:"$id"`
	} `json:"parent,omitempty"`
}

type page struct {
	Items []raindrop `json:"items"`
	Count int        `json:"count"`
	Page  int        `json:"page"`
}

func bookmarkID(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 {
		return 0, strconv.ErrSyntax
	}
	var id int64
	if err := json.Unmarshal(raw, &id); err == nil {
		return id, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, strconv.ErrSyntax
	}
	return strconv.ParseInt(strings.TrimSpace(text), 10, 64)
}
