package main

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestLiveInstance reads a real RSSHub instance, when RSSHUB_LIVE_URL names one (and RSSHUB_LIVE_KEY its access key,
// RSSHUB_LIVE_ROUTE a route it serves): subscribe, a conditional refresh, the tools and the page. It is skipped
// otherwise, so the suite never needs the network.
func TestLiveInstance(t *testing.T) {
	base := os.Getenv("RSSHUB_LIVE_URL")
	if base == "" {
		t.Skip("RSSHUB_LIVE_URL not set")
	}
	route := os.Getenv("RSSHUB_LIVE_ROUTE")
	if route == "" {
		route = "/github/issue/DIYgod/RSSHub"
	}
	key := os.Getenv("RSSHUB_LIVE_KEY")
	ctx := context.Background()
	for _, auth := range []string{"key", "code"} {
		t.Run(auth, func(t *testing.T) {
			a, hub, _ := testApp(t, nil, config{InstanceURL: base, AccessAuth: auth}, key)
			p, err := a.preview(ctx, route)
			if err != nil || p.Err != "" || len(p.Feed.Entries) == 0 {
				t.Fatalf("preview %s: %v %+v", route, err, p)
			}
			t.Logf("preview: %q, %d entries, first %q (%s)", p.Feed.Title, len(p.Feed.Entries), p.Feed.Entries[0].Title, p.Feed.Format)
			f, added, n, err := a.subscribe(ctx, route, "", "live")
			if err != nil || !added || n == 0 {
				t.Fatalf("subscribe: %v %v %d", err, added, n)
			}
			t.Logf("subscribed %s (%s): %d entries, etag %q, last-modified %q, badge %s", f.ID, f.name(), n, f.ETag, f.LastModified, hub.lastBadge())
			n, err = a.refresh(ctx, f.ID)
			if err != nil {
				t.Fatal(err)
			}
			f, _ = a.feeds().get(f.ID)
			t.Logf("refresh: %d new, still %d entries", n, len(f.Entries))
			if key != "" {
				for _, e := range f.Entries {
					if strings.Contains(e.Link+e.Content+e.Summary+f.SiteLink, key) {
						t.Fatal("the access key was kept")
					}
				}
			}
			text, err := a.callTool(ctx, toolEntries, []byte(`{"limit":3}`))
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("rsshub_entries:\n%s", text)
			text, _ = a.callTool(ctx, toolFeeds, nil)
			t.Logf("rsshub_feeds:\n%s", text)
			if err := a.ping(ctx); err != nil {
				t.Fatal(err)
			}
			v, _ := a.render(ctx, uiRequest(4, "", "", nil))
			checkView(t, v)
		})
	}
}
