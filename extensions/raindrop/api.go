package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultAPIBase = "https://api.raindrop.io"

var (
	errMissingToken = errors.New("Raindrop access token is not configured")
	errBadToken     = errors.New("Raindrop rejected the access token; replace it in the extension settings")
	errRateLimited  = errors.New("Raindrop rate limit reached; try again shortly")
	errUnreachable  = errors.New("Raindrop could not be reached; try again later")
	errNotFound     = errors.New("bookmark not found")
)

type apiClient struct {
	base  string
	token string
	http  *http.Client
}

func newAPIClient(base, token string) *apiClient {
	return &apiClient{base: strings.TrimRight(base, "/"), token: token,
		http: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}}
}

func (c *apiClient) call(ctx context.Context, method, path string, query url.Values, body io.Reader, out any) error {
	if strings.TrimSpace(c.token) == "" {
		return errMissingToken
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "persona-raindrop/"+about.Version)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if query != nil {
		req.URL.RawQuery = query.Encode()
	}
	// No permanent-copy redirect: credentials stay on the configured public API origin.
	res, err := c.http.Do(req)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return fmt.Errorf("%w: timeout", errUnreachable)
		}
		return fmt.Errorf("%w: %v", errUnreachable, err)
	}
	defer res.Body.Close()
	const limit = 4 << 20
	data, readErr := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if readErr != nil {
		return errUnreachable
	}
	if int64(len(data)) > limit {
		return errors.New("Raindrop returned an oversized response")
	}
	switch res.StatusCode {
	case http.StatusOK, http.StatusCreated:
		var envelope struct {
			Result bool   `json:"result"`
			Error  string `json:"errorMessage"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			return errors.New("Raindrop returned invalid JSON")
		}
		if !envelope.Result {
			return fmt.Errorf("Raindrop refused the request: %s", clipText(envelope.Error, 160))
		}
		return json.Unmarshal(data, out)
	case http.StatusUnauthorized, http.StatusForbidden:
		return errBadToken
	case http.StatusNotFound:
		return errNotFound
	case http.StatusTooManyRequests:
		return errRateLimited
	default:
		return fmt.Errorf("Raindrop returned HTTP %d", res.StatusCode)
	}
}

func (c *apiClient) collections(ctx context.Context) ([]collection, error) {
	var roots, children struct {
		Items []collection `json:"items"`
	}
	if err := c.call(ctx, http.MethodGet, "/rest/v1/collections", nil, nil, &roots); err != nil {
		return nil, err
	}
	if err := c.call(ctx, http.MethodGet, "/rest/v1/collections/childrens", nil, nil, &children); err != nil {
		return nil, err
	}
	return collectionPaths(append(roots.Items, children.Items...)), nil
}

func collectionPaths(all []collection) []collection {
	byID := map[int64]collection{}
	for _, c := range all {
		byID[c.ID] = c
	}
	out := make([]collection, 0, len(all))
	for _, original := range all {
		c, parts := original, []string{original.Title}
		seen := map[int64]bool{c.ID: true}
		for c.Parent != nil && !seen[c.Parent.ID] {
			seen[c.Parent.ID] = true
			parent, ok := byID[c.Parent.ID]
			if !ok {
				break
			}
			parts = append([]string{parent.Title}, parts...)
			c = parent
		}
		out = append(out, collection{ID: original.ID, Title: strings.Join(parts, " / ")})
	}
	return out
}

// searchPagesMost is the most pages one search reads.
const searchPagesMost = 4

// search reads at most pagesToRead pages, and never more than searchPagesMost (50 is the API's documented per-page
// maximum).
func (c *apiClient) search(ctx context.Context, collectionID int64, query, sort string, pageNumber, pagesToRead int) (page, error) {
	if pageNumber < 0 {
		pageNumber = 0
	}
	pagesToRead = max(1, min(pagesToRead, searchPagesMost))
	var combined page
	for n := range pagesToRead {
		q := url.Values{"page": {strconv.Itoa(pageNumber + n)}, "perpage": {"50"}}
		if strings.TrimSpace(query) != "" {
			q.Set("search", query)
		}
		if sort != "" {
			q.Set("sort", sort)
		}
		var result page
		if err := c.call(ctx, http.MethodGet, "/rest/v1/raindrops/"+strconv.FormatInt(collectionID, 10), q, nil, &result); err != nil {
			return page{}, err
		}
		combined.Items = append(combined.Items, result.Items...)
		combined.Count, combined.Page = result.Count, result.Page
		if len(result.Items) < 50 {
			break
		}
	}
	return combined, nil
}

func (c *apiClient) bookmark(ctx context.Context, id int64) (raindrop, error) {
	var result struct {
		Item raindrop `json:"item"`
	}
	err := c.call(ctx, http.MethodGet, "/rest/v1/raindrop/"+strconv.FormatInt(id, 10), nil, nil, &result)
	return result.Item, err
}

func (c *apiClient) save(ctx context.Context, args saveArgs) (raindrop, error) {
	body, err := json.Marshal(map[string]any{
		"link": args.URL, "pleaseParse": map[string]any{}, "collection": map[string]int64{"$id": args.CollectionID},
		"tags": args.Tags, "title": args.Title, "note": args.Note,
	})
	if err != nil {
		return raindrop{}, err
	}
	var result struct {
		Item raindrop `json:"item"`
	}
	err = c.call(ctx, http.MethodPost, "/rest/v1/raindrop", nil, bytes.NewReader(body), &result)
	return result.Item, err
}
