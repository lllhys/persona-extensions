package main

import "encoding/json"

type issue struct {
	ID     string `json:"id"`
	Key    string `json:"key"`
	Self   string `json:"self"`
	Fields struct {
		Summary     string                         `json:"summary"`
		Description json.RawMessage                `json:"description"`
		Status      struct{ ID, Name string }      `json:"status"`
		Assignee    *user                          `json:"assignee"`
		Project     struct{ ID, Key, Name string } `json:"project"`
		Priority    struct{ ID, Name string }      `json:"priority"`
		IssueType   struct{ ID, Name string }      `json:"issuetype"`
		Updated     string                         `json:"updated"`
		Comment     struct {
			Comments []comment `json:"comments"`
			Total    int       `json:"total"`
		} `json:"comment"`
	} `json:"fields"`
}

type user struct {
	AccountID   string `json:"accountId"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

type comment struct {
	ID      string          `json:"id"`
	Body    json.RawMessage `json:"body"`
	Author  user            `json:"author"`
	Created string          `json:"created"`
}

// searchPage is one answer of /search/jql, which pages with nextPageToken and says no total; it is absent on the last
// page.
type searchPage struct {
	Issues        []issue `json:"issues"`
	NextPageToken string  `json:"nextPageToken"`
	IsLast        bool    `json:"isLast"`
}

// next is the token of the page after this one, "" when this is the last.
func (p searchPage) next() string {
	if p.IsLast {
		return ""
	}
	return p.NextPageToken
}

// maxPageToken bounds a page token read back from page state or a tool call.
const maxPageToken = 2048

type issueType struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Subtask bool   `json:"subtask"`
}
