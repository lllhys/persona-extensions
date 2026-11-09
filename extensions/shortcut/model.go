package main

type member struct {
	ID          string `json:"id"`
	MentionName string `json:"mention_name"`
	Name        string `json:"name"`
}

type story struct {
	ID              int64     `json:"id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	AppURL          string    `json:"app_url"`
	Completed       bool      `json:"completed"`
	Started         bool      `json:"started"`
	Archived        bool      `json:"archived"`
	StoryType       string    `json:"story_type"`
	Deadline        string    `json:"deadline"`
	WorkflowID      int64     `json:"workflow_id"`
	WorkflowStateID int64     `json:"workflow_state_id"`
	OwnerIDs        []string  `json:"owner_ids"`
	Comments        []comment `json:"comments"`
}

type comment struct {
	ID       int64  `json:"id"`
	Text     string `json:"text"`
	AuthorID string `json:"author_id"`
	Deleted  bool   `json:"deleted"`
}

type workflow struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	DefaultStateID int64   `json:"default_state_id"`
	States         []state `json:"states"`
}

type state struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Position int    `json:"position"`
}

type storyPage struct {
	Data  []story `json:"data"`
	Next  string  `json:"next"`
	Total int     `json:"total"`
}
