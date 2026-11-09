package main

type user struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
}

type team struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Members []struct {
		User user `json:"user"`
	} `json:"members"`
}

type taskList struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Archived bool   `json:"archived"`
}

type task struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	TextContent string `json:"text_content"`
	Status      struct {
		Status string `json:"status"`
		Type   string `json:"type"`
	} `json:"status"`
	DueDate     string `json:"due_date"`
	DateUpdated string `json:"date_updated"`
	Archived    bool   `json:"archived"`
	URL         string `json:"url"`
	List        struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"list"`
	Assignees []user `json:"assignees"`
}

type teamPage struct {
	Teams []team `json:"teams"`
}

type listPage struct {
	Lists []taskList `json:"lists"`
}

type taskPage struct {
	Tasks []task `json:"tasks"`
	Last  bool   `json:"last"`
}
