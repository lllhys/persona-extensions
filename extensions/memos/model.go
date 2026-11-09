package main

type memo struct {
	Name       string   `json:"name"`
	Creator    string   `json:"creator"`
	Content    string   `json:"content"`
	Visibility string   `json:"visibility"`
	State      string   `json:"state"`
	Pinned     bool     `json:"pinned"`
	Tags       []string `json:"tags"`
	Snippet    string   `json:"snippet"`
	CreateTime string   `json:"createTime"`
	UpdateTime string   `json:"updateTime"`
}

type memoPage struct {
	Memos         []memo `json:"memos"`
	NextPageToken string `json:"nextPageToken"`
}

type user struct {
	Name        string `json:"name"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	State       string `json:"state"`
}
