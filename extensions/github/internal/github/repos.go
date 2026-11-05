package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// What the page's repository side reads: the user's repositories (recent, their own, starred, an organization's, a
// search), one repository's tree at a ref, a file, the README, the branches, and its PR / issue lists. Every read
// goes through cachedGet (If-None-Match: unchanged answers cost nothing of the hour's requests).

// Repository lists of the page's 仓库 tab.
const (
	ReposRecent  = "recent"  // pushed to lately, all I can push to
	ReposMine    = "mine"    // owned by me
	ReposStarred = "starred" // starred by me
	ReposOrg     = "org:"    // + an organization's login
)

// PageSize is how many rows a page of a list reads.
const PageSize = 30

// Repo is a repository as the lists and its page show it.
type Repo struct {
	FullName      string    `json:"full_name"`
	Description   string    `json:"description"`
	Private       bool      `json:"private"`
	Fork          bool      `json:"fork"`
	Archived      bool      `json:"archived"`
	Stars         int       `json:"stargazers_count"`
	Forks         int       `json:"forks_count"`
	OpenIssues    int       `json:"open_issues_count"`
	Language      string    `json:"language"`
	DefaultBranch string    `json:"default_branch"`
	PushedAt      time.Time `json:"pushed_at"`
	HTMLURL       string    `json:"html_url"`
	Owner         person    `json:"owner"`
}

// pageGet reads page (from 1) of a list of per rows; more says whether there may be another.
func pageGet[T any](c *Connector, ctx context.Context, p string, page, per int) ([]T, bool, error) {
	sep := "?"
	if strings.Contains(p, "?") {
		sep = "&"
	}
	var batch []T
	if err := c.cachedGet(ctx, fmt.Sprintf("%s%sper_page=%d&page=%d", p, sep, per, max(page, 1)), &batch); err != nil {
		return nil, false, err
	}
	return batch, len(batch) == per, nil
}

// Repos is a page of one of the 仓库 tab's lists.
func (c *Connector) Repos(ctx context.Context, list string, page int) ([]Repo, bool, error) {
	switch {
	case list == ReposMine:
		return pageGet[Repo](c, ctx, "/user/repos?affiliation=owner&sort=pushed", page, PageSize)
	case list == ReposStarred:
		return pageGet[Repo](c, ctx, "/user/starred?sort=updated", page, PageSize)
	case strings.HasPrefix(list, ReposOrg):
		org := strings.TrimPrefix(list, ReposOrg)
		if !validOwner(org) {
			return nil, false, fmt.Errorf("no organization %q", org)
		}
		return pageGet[Repo](c, ctx, "/orgs/"+org+"/repos?sort=pushed", page, PageSize)
	}
	return pageGet[Repo](c, ctx, "/user/repos?affiliation=owner,collaborator,organization_member&sort=pushed", page, PageSize)
}

// SearchRepos is a page of GitHub's repository search, best match first.
func (c *Connector) SearchRepos(ctx context.Context, q string, page int) ([]Repo, bool, error) {
	var res struct {
		Total int    `json:"total_count"`
		Items []Repo `json:"items"`
	}
	p := "/search/repositories?" + url.Values{"q": {q}, "per_page": {strconv.Itoa(PageSize)}, "page": {strconv.Itoa(max(page, 1))}}.Encode()
	if err := c.cachedGet(ctx, p, &res); err != nil {
		return nil, false, err
	}
	return res.Items, max(page, 1)*PageSize < min(res.Total, 1000), nil
}

// Orgs are the logins of the organizations the user is in.
func (c *Connector) Orgs(ctx context.Context) ([]string, error) {
	var orgs []struct {
		Login string `json:"login"`
	}
	if err := c.cachedGet(ctx, "/user/orgs?per_page=100", &orgs); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(orgs))
	for _, o := range orgs {
		out = append(out, o.Login)
	}
	return out, nil
}

// RepoInfo is one repository.
func (c *Connector) RepoInfo(ctx context.Context, repo string) (Repo, error) {
	var r Repo
	if !validRepo(repo) {
		return r, fmt.Errorf("%q is not owner/name", repo)
	}
	err := c.cachedGet(ctx, "/repos/"+repo, &r)
	return r, err
}

// TreeEntry is one entry of a directory.
type TreeEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"` // file, dir, symlink, submodule
	Size int    `json:"size"`
}

// Dir is a directory: its entries, directories first, then by name.
func (c *Connector) Dir(ctx context.Context, repo, ref, dir string) ([]TreeEntry, error) {
	p, err := contentsPath(repo, ref, dir)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := c.cachedGet(ctx, p, &raw); err != nil {
		return nil, err
	}
	var entries []TreeEntry
	if err := decodeBody(p, raw, &entries); err != nil {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		di, dj := entries[i].Type == "dir", entries[j].Type == "dir"
		if di != dj {
			return di
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	return entries, nil
}

// File is a file's content at a ref.
type File struct {
	Path    string
	Size    int
	Text    string // "" for a binary file, or one too big
	Binary  bool
	TooBig  bool // over the contents API's 1 MB
	HTMLURL string
}

// FileAt reads a file.
func (c *Connector) FileAt(ctx context.Context, repo, ref, file string) (File, error) {
	p, err := contentsPath(repo, ref, file)
	if err != nil {
		return File{}, err
	}
	var raw json.RawMessage
	if err := c.cachedGet(ctx, p, &raw); err != nil {
		return File{}, err
	}
	var f struct {
		Type     string `json:"type"`
		Path     string `json:"path"`
		Size     int    `json:"size"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
		HTMLURL  string `json:"html_url"`
	}
	if err := decodeBody(p, raw, &f); err != nil || f.Type != "file" {
		return File{}, fmt.Errorf("%s is not a file", file)
	}
	out := File{Path: f.Path, Size: f.Size, HTMLURL: f.HTMLURL}
	if f.Encoding != "base64" {
		out.TooBig = f.Size > 0
		return out, nil
	}
	b, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(f.Content, "\n", ""))
	if err != nil {
		return File{}, fmt.Errorf("%s: %v", file, err)
	}
	if isBinary(b) {
		out.Binary = true
		return out, nil
	}
	out.Text = string(b)
	return out, nil
}

// Readme is the README of a directory ("" the root), its path and text; "" when there is none.
func (c *Connector) Readme(ctx context.Context, repo, ref, dir string) (string, string, error) {
	if !validRepo(repo) {
		return "", "", fmt.Errorf("%q is not owner/name", repo)
	}
	p := "/repos/" + repo + "/readme"
	if dir != "" {
		clean, ok := cleanPath(dir)
		if !ok {
			return "", "", fmt.Errorf("no path %q", dir)
		}
		p += "/" + escapePath(clean)
	}
	if ref != "" {
		p += "?ref=" + url.QueryEscape(ref)
	}
	var f struct {
		Path     string `json:"path"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if err := c.cachedGet(ctx, p, &f); err != nil {
		if strings.Contains(err.Error(), ": 404") {
			return "", "", nil
		}
		return "", "", err
	}
	b, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(f.Content, "\n", ""))
	if err != nil || isBinary(b) {
		return f.Path, "", nil
	}
	return f.Path, string(b), nil
}

// Branch is a branch's name.
type Branch struct {
	Name      string `json:"name"`
	Protected bool   `json:"protected"`
}

// Branches is a page of a repository's branches (100 a page).
func (c *Connector) Branches(ctx context.Context, repo string, page int) ([]Branch, bool, error) {
	if !validRepo(repo) {
		return nil, false, fmt.Errorf("%q is not owner/name", repo)
	}
	return pageGet[Branch](c, ctx, "/repos/"+repo+"/branches", page, 100)
}

// RepoItems is a page of a repository's PRs (pr) or issues, open or closed, newest update first; with q, GitHub's
// search within the repository.
func (c *Connector) RepoItems(ctx context.Context, repo string, pr, closed bool, q string, page int) ([]Entry, bool, error) {
	if !validRepo(repo) {
		return nil, false, fmt.Errorf("%q is not owner/name", repo)
	}
	state := "open"
	if closed {
		state = "closed"
	}
	if q = strings.TrimSpace(q); q != "" {
		kind := "is:issue"
		if pr {
			kind = "is:pr"
		}
		var res struct {
			Total int          `json:"total_count"`
			Items []searchItem `json:"items"`
		}
		p := "/search/issues?" + url.Values{"q": {fmt.Sprintf("repo:%s %s is:%s %s", repo, kind, state, q)}, "sort": {"updated"},
			"per_page": {strconv.Itoa(PageSize)}, "page": {strconv.Itoa(max(page, 1))}}.Encode()
		if err := c.cachedGet(ctx, p, &res); err != nil {
			return nil, false, err
		}
		out := make([]Entry, 0, len(res.Items))
		for _, it := range res.Items {
			out = append(out, Entry{Repo: repo, Number: it.Number, PR: it.PullRequest != nil, Title: it.Title, Author: it.User.Login,
				Avatar: it.User.AvatarURL, Updated: it.UpdatedAt, URL: it.HTMLURL, Draft: it.Draft})
		}
		return out, max(page, 1)*PageSize < min(res.Total, 1000), nil
	}
	if pr {
		type pull struct {
			Number    int       `json:"number"`
			Title     string    `json:"title"`
			HTMLURL   string    `json:"html_url"`
			UpdatedAt time.Time `json:"updated_at"`
			Draft     bool      `json:"draft"`
			MergedAt  *string   `json:"merged_at"`
			User      person    `json:"user"`
		}
		ps, more, err := pageGet[pull](c, ctx, "/repos/"+repo+"/pulls?sort=updated&direction=desc&state="+state, page, PageSize)
		if err != nil {
			return nil, false, err
		}
		out := make([]Entry, 0, len(ps))
		for _, p := range ps {
			e := Entry{Repo: repo, Number: p.Number, PR: true, Title: p.Title, Author: p.User.Login, Avatar: p.User.AvatarURL,
				Updated: p.UpdatedAt, URL: p.HTMLURL, Draft: p.Draft}
			if p.MergedAt != nil {
				e.Why = "已合并"
			}
			out = append(out, e)
		}
		return out, more, nil
	}
	is, more, err := pageGet[searchItem](c, ctx, "/repos/"+repo+"/issues?sort=updated&direction=desc&state="+state, page, PageSize)
	if err != nil {
		return nil, false, err
	}
	out := make([]Entry, 0, len(is))
	for _, it := range is {
		if it.PullRequest != nil { // the issues list has the PRs too
			continue
		}
		out = append(out, Entry{Repo: repo, Number: it.Number, Title: it.Title, Author: it.User.Login, Avatar: it.User.AvatarURL,
			Updated: it.UpdatedAt, URL: it.HTMLURL})
	}
	return out, more, nil
}

// contentsPath is the contents API's path of file or dir at ref.
func contentsPath(repo, ref, p string) (string, error) {
	if !validRepo(repo) {
		return "", fmt.Errorf("%q is not owner/name", repo)
	}
	clean, ok := cleanPath(p)
	if !ok {
		return "", fmt.Errorf("no path %q", p)
	}
	out := "/repos/" + repo + "/contents"
	if clean != "" {
		out += "/" + escapePath(clean)
	}
	if ref != "" {
		out += "?ref=" + url.QueryEscape(ref)
	}
	return out, nil
}

// cleanPath is p within the repository ("" the root); ok is false for one that climbs out of it.
func cleanPath(p string) (string, bool) {
	p = strings.Trim(p, "/")
	if p == "" {
		return "", true
	}
	c := path.Clean(p)
	if c == ".." || strings.HasPrefix(c, "../") || strings.Contains(p, "\x00") || len(p) > 1024 {
		return "", false
	}
	if c == "." {
		return "", true
	}
	return c, true
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

func validOwner(s string) bool { return s != "" && validRepo(s+"/x") }

// isBinary says whether b looks like a binary file: a NUL in its first 8 KB, or not UTF-8.
func isBinary(b []byte) bool {
	head := b
	if len(head) > 8<<10 {
		head = head[:8<<10]
	}
	for _, x := range head {
		if x == 0 {
			return true
		}
	}
	return !utf8.Valid(head) && !utf8.Valid(head[:max(0, len(head)-3)])
}
