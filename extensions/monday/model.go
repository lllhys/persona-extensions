package main

import "encoding/json"

type user struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type board struct {
	ID      int64    `json:"id,string"`
	Name    string   `json:"name"`
	URL     string   `json:"url"`
	Columns []column `json:"columns"`
}

type column struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Type        string `json:"type"`
	SettingsStr string `json:"settings_str"`
}

type item struct {
	ID           int64  `json:"id,string"`
	Name         string `json:"name"`
	URL          string `json:"url"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	ColumnValues []struct {
		ID    string `json:"id"`
		Text  string `json:"text"`
		Value string `json:"value"`
	} `json:"column_values"`
}

type itemsPage struct {
	Cursor string `json:"cursor"`
	Items  []item `json:"items"`
}

type boardPage struct {
	Boards []board `json:"boards"`
}

type itemPage struct {
	Boards []struct {
		ItemsPage itemsPage `json:"items_page"`
	} `json:"boards"`
}

type createPage struct {
	CreateItem item `json:"create_item"`
}

type updatePage struct {
	ChangeMultipleColumnValues item `json:"change_multiple_column_values"`
}

type mePage struct {
	Me user `json:"me"`
}

type graphqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}
