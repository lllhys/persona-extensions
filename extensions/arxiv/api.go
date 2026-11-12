package main

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var errUnavailable = errors.New("arXiv could not be reached; try again later")

type client struct {
	base string
	http *http.Client
}

func newClient(base string) *client {
	return &client{base: strings.TrimRight(base, "/"), http: &http.Client{
		Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (c *client) query(ctx context.Context, query string, recent bool, start int) (feed, error) {
	if len([]rune(query)) > 200 || strings.ContainsRune(query, 0) {
		return feed{}, errors.New("search text must be at most 200 characters")
	}
	if start < 0 {
		return feed{}, errors.New("start must not be negative")
	}
	q := url.Values{"start": {strconv.Itoa(start)}, "max_results": {"20"}}
	if recent {
		q.Set("search_query", "all:"+query)
		q.Set("sortBy", "submittedDate")
		q.Set("sortOrder", "descending")
	} else {
		if strings.TrimSpace(query) == "" {
			return feed{}, errors.New("search text is required")
		}
		q.Set("search_query", "all:"+query)
		q.Set("sortBy", "relevance")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/query", nil)
	if err != nil {
		return feed{}, err
	}
	req.Header.Set("Accept", "application/atom+xml")
	req.URL.RawQuery = q.Encode()
	res, err := c.http.Do(req)
	if err != nil {
		return feed{}, fmt.Errorf("%w: %v", errUnavailable, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20+1))
	if err != nil {
		return feed{}, errUnavailable
	}
	if len(data) > 4<<20 {
		return feed{}, errors.New("arXiv returned an oversized response")
	}
	if res.StatusCode != http.StatusOK {
		return feed{}, fmt.Errorf("arXiv returned HTTP %d", res.StatusCode)
	}
	var out feed
	if err := xml.Unmarshal(data, &out); err != nil {
		return feed{}, errors.New("arXiv returned invalid Atom XML")
	}
	if len(out.Entries) > 100 {
		return feed{}, errors.New("arXiv returned too many entries")
	}
	return out, nil
}

func (c *client) paper(ctx context.Context, id string) (paper, error) {
	if !validID(id) {
		return paper{}, errors.New("invalid arXiv ID")
	}
	q := url.Values{"id_list": {id}, "max_results": {"1"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/query", nil)
	if err != nil {
		return paper{}, err
	}
	req.URL.RawQuery = q.Encode()
	res, err := c.http.Do(req)
	if err != nil {
		return paper{}, fmt.Errorf("%w: %v", errUnavailable, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 2<<20+1))
	if err != nil {
		return paper{}, errUnavailable
	}
	if len(data) > 2<<20 {
		return paper{}, errors.New("arXiv returned an oversized response")
	}
	if res.StatusCode != http.StatusOK {
		return paper{}, fmt.Errorf("arXiv returned HTTP %d", res.StatusCode)
	}
	var out feed
	if err := xml.Unmarshal(data, &out); err != nil {
		return paper{}, errors.New("arXiv returned invalid Atom XML")
	}
	if len(out.Entries) != 1 {
		return paper{}, errors.New("arXiv could not find that paper")
	}
	return out.Entries[0], nil
}

// arxivID is an arXiv identifier with an optional version: YYMM.NNNNN, or archive[.XX]/YYMMNNN for papers from before
// April 2007 (hep-th/9901001, math.GT/0309136).
var arxivID = regexp.MustCompile(`^(\d{4}\.\d{4,5}|[a-z]+(-[a-z]+)*(\.[A-Z]{2})?/\d{7})(v\d+)?$`)

func validID(id string) bool { return len(id) <= 100 && arxivID.MatchString(id) }

// paperID is the arXiv ID in an entry's Atom <id>, which the API writes as http://arxiv.org/abs/<id>. It is an
// identifier, never opened, so either scheme is read; "" when it names no arXiv paper.
func paperID(item paper) string {
	for _, prefix := range []string{"http://arxiv.org/abs/", "https://arxiv.org/abs/"} {
		if id, ok := strings.CutPrefix(item.ID, prefix); ok && validID(id) {
			return id
		}
	}
	return ""
}

// paperURL is the paper's abstract page, always over https; "" when the entry names no arXiv paper.
func paperURL(item paper) string {
	if id := paperID(item); id != "" {
		return "https://arxiv.org/abs/" + id
	}
	return ""
}

func clipText(text string, limit int) string {
	r := []rune(text)
	if len(r) > limit {
		return string(r[:max(0, limit-1)]) + "…"
	}
	return text
}
