package main

type namedResource struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type issue struct {
	ID          int64         `json:"id"`
	Subject     string        `json:"subject"`
	Description string        `json:"description"`
	Project     namedResource `json:"project"`
	Tracker     namedResource `json:"tracker"`
	Status      namedResource `json:"status"`
	Priority    namedResource `json:"priority"`
	Author      namedResource `json:"author"`
	AssignedTo  namedResource `json:"assigned_to"`
	StartDate   string        `json:"start_date"`
	DueDate     string        `json:"due_date"`
	DoneRatio   int           `json:"done_ratio"`
	CreatedOn   string        `json:"created_on"`
	UpdatedOn   string        `json:"updated_on"`
	Journals    []journal     `json:"journals"`
}

type journal struct {
	ID           int64         `json:"id"`
	User         namedResource `json:"user"`
	Notes        string        `json:"notes"`
	CreatedOn    string        `json:"created_on"`
	PrivateNotes bool          `json:"private_notes"`
}

type issuePage struct {
	Issues     []issue `json:"issues"`
	TotalCount int     `json:"total_count"`
	Limit      int     `json:"limit"`
	Offset     int     `json:"offset"`
}

type projectPage struct {
	Projects   []namedResource `json:"projects"`
	TotalCount int             `json:"total_count"`
	Limit      int             `json:"limit"`
	Offset     int             `json:"offset"`
}
