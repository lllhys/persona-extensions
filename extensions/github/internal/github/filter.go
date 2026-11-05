package github

import (
	"path"
	"slices"
	"strings"
)

// What of GitHub may come into the inbox at all (nwverse/persona#3178): the extension's settings (persona-extension.json's
// "inbox" group) name the repositories and kinds. A notification they leave out is passed over as if it were not
// there: no item, so nothing is pushed for it either. What is already in the inbox stays as it is; the page and the
// assistant's tools read GitHub as before. The on / off switch is the hub's own (the connector's 进收件箱).

// InboxFilter is the extension's choice of repositories and kinds for the inbox. Empty lets everything in, so a
// configuration from before it changes nothing.
type InboxFilter struct {
	// Repos, when any is valid, are the only repositories that come in: owner/name or a path.Match pattern of it
	// (owner/*), any case.
	Repos []string `json:"repos,omitempty"`
	// ExcludeRepos are left out, after Repos.
	ExcludeRepos []string `json:"exclude_repos,omitempty"`
	// SkipKinds are kinds (review_requested, mention…) that never come in.
	SkipKinds []string `json:"skip_kinds,omitempty"`
}

// repoPatterns are the valid owner/name patterns of ps, lowercased; the rest are left out (the form's pattern stops
// most of them before they get here).
func repoPatterns(ps []string) []string {
	var out []string
	for _, p := range ps {
		p = strings.ToLower(strings.TrimSpace(p))
		owner, name, ok := strings.Cut(p, "/")
		if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
			continue
		}
		if _, err := path.Match(p, ""); err != nil {
			continue
		}
		out = append(out, p)
	}
	return out
}

func matchRepo(patterns []string, repo string) bool {
	repo = strings.ToLower(repo)
	return slices.ContainsFunc(patterns, func(p string) bool {
		ok, _ := path.Match(p, repo)
		return ok
	})
}

// lets says whether a notification of kind on repo (owner/name) may come into the inbox.
func (f InboxFilter) lets(repo, kind string) bool {
	if slices.Contains(f.SkipKinds, kind) {
		return false
	}
	if only := repoPatterns(f.Repos); len(only) > 0 && !matchRepo(only, repo) {
		return false
	}
	return !matchRepo(repoPatterns(f.ExcludeRepos), repo)
}

// WithFilter sets the repositories and kinds the inbox takes.
func (c *Connector) WithFilter(f InboxFilter) *Connector {
	c.mu.Lock()
	c.filter = f
	c.mu.Unlock()
	return c
}

func (c *Connector) inboxFilter() InboxFilter {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.filter
}
