package main

import (
	"encoding/json"
	"strings"
)

type bookmark struct {
	Href        string `json:"href"`
	Description string `json:"description"`
	Extended    string `json:"extended"`
	Tags        string `json:"tag"`
	Time        string `json:"time"`
	Shared      string `json:"shared"`
	ToRead      string `json:"toread"`
}

type bookmarkList []bookmark

func (b *bookmarkList) UnmarshalJSON(data []byte) error {
	var direct []bookmark
	if json.Unmarshal(data, &direct) == nil {
		*b = direct
		return nil
	}
	var wrapped struct {
		Posts []bookmark `json:"posts"`
		Data  []bookmark `json:"data"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return err
	}
	if wrapped.Posts != nil {
		*b = wrapped.Posts
	} else {
		*b = wrapped.Data
	}
	return nil
}

type tag struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

type tagList []tag

func (t *tagList) UnmarshalJSON(data []byte) error {
	var direct []tag
	if json.Unmarshal(data, &direct) == nil {
		*t = direct
		return nil
	}
	var byName map[string]int
	if err := json.Unmarshal(data, &byName); err == nil {
		for name, count := range byName {
			*t = append(*t, tag{Tag: name, Count: count})
		}
		return nil
	}
	var wrapped struct {
		Tags []tag `json:"tags"`
	}
	return json.Unmarshal(data, &wrapped)
}

func (b bookmark) TagNames() []string {
	var out []string
	for _, value := range strings.Fields(b.Tags) {
		if len(out) < 20 {
			out = append(out, value)
		}
	}
	return out
}
