package x

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/x/internal/connector"
)

// The proxy setting takes http, https and socks5 proxies; a malformed one fails every request instead of going
// direct, and its error never shows the password.
func TestProxySetting(t *testing.T) {
	for _, ok := range []string{"", "http://proxy.example:8080", "https://user:pw@proxy.example:443", "socks5://proxy.example:1080", "socks5h://u:p@proxy.example:1080"} {
		if _, err := connector.ParseProxy(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"ftp://proxy.example:21", "proxy.example:8080", "http://user:hunter2@proxy.example:8080/path"} {
		_, err := connector.ParseProxy(bad)
		if err == nil || strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%s: %v", bad, err)
		}
	}
	c := New(Settings{Proxy: "ftp://proxy.example:21"}, Options{Base: "https://api.x.com"})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.x.com/2/users/me", nil)
	if _, err := c.api.http.Do(req); err == nil || !strings.Contains(err.Error(), "proxy setting") {
		t.Fatalf("a bad proxy: %v", err)
	}
}
