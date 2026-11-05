package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/github/internal/connector"
)

// With the proxy setting, the device flow (code and polling) and the REST API all go to the proxy, addressed to
// GitHub's hosts; nothing goes to GitHub directly.
func TestProxyCarriesEverything(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	var seen []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Host+r.URL.Path)
		mu.Unlock()
		switch r.URL.Host + r.URL.Path {
		case "github.example/login/device/code":
			if r.Form.Get("client_id") != ghCLIClientID {
				t.Errorf("client_id %q", r.Form.Get("client_id"))
			}
			_, _ = w.Write([]byte(`{"device_code":"dc","user_code":"PRXY-0001","verification_uri":"https://github.com/login/device","expires_in":900,"interval":5}`))
		case "github.example/login/oauth/access_token":
			_, _ = w.Write([]byte(`{"access_token":"gho_viaproxy"}`))
		case "api.github.example/user":
			w.Header().Set("X-OAuth-Scopes", "repo")
			_, _ = w.Write([]byte(`{"login":"example-user"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(proxy.Close)

	var cfg Config
	if err := json.Unmarshal([]byte(`{"proxy":"`+proxy.URL+`"}`), &cfg); err != nil || cfg.Proxy != proxy.URL {
		t.Fatalf("proxy from the settings: %+v %v", cfg, err)
	}
	c := New(cfg, "", nil)
	c.web, c.api = "http://github.example", "http://api.github.example" // hosts only the proxy knows
	c.UseSecrets(&memSecrets{})
	p, err := c.LoginStep(ctx, "", nil)
	if err != nil || p.UserCode != "PRXY-0001" {
		t.Fatalf("code through the proxy: %+v %v", p, err)
	}
	if p, err = c.LoginStep(ctx, "device", nil); err != nil || !p.Done || p.Account != "example-user" {
		t.Fatalf("signed in through the proxy: %+v %v", p, err)
	}
	mu.Lock()
	got := strings.Join(seen, ", ")
	mu.Unlock()
	want := "POST github.example/login/device/code, POST github.example/login/oauth/access_token, GET api.github.example/user"
	if got != want {
		t.Fatalf("through the proxy:\n got %s\nwant %s", got, want)
	}
}

// Without a proxy setting the connector uses the environment's proxy (the hub's egress proxy when the extension runs in
// the sandbox), which as everywhere in Go leaves loopback direct: a GitHub on this machine is reached directly.
func TestNoProxySettingLeavesLoopbackDirect(t *testing.T) {
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("went through the environment's proxy: %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(trap.Close)
	t.Setenv("HTTP_PROXY", trap.URL)
	t.Setenv("HTTPS_PROXY", trap.URL)
	t.Setenv("NO_PROXY", "")
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"device_code":"dc","user_code":"DRCT-0002","verification_uri":"https://github.com/login/device","expires_in":900,"interval":5}`))
	}))
	t.Cleanup(gh.Close)
	c := New(Config{}, "", nil)
	c.web, c.api = gh.URL, gh.URL
	c.UseSecrets(&memSecrets{})
	if p, err := c.LoginStep(context.Background(), "", nil); err != nil || p.UserCode != "DRCT-0002" {
		t.Fatalf("direct: %+v %v", p, err)
	}
}

// A malformed proxy setting fails every request and says so; it never falls back to direct, and never shows the
// proxy's password.
func TestBadProxy(t *testing.T) {
	ctx := context.Background()
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("went direct: %s", r.URL)
	}))
	t.Cleanup(gh.Close)
	c := New(Config{Egress: connector.Egress{Proxy: "socks5://user:s3cret@127.0.0.1:1080"}}, "", nil)
	c.web, c.api = gh.URL, gh.URL
	c.UseSecrets(&memSecrets{data: []byte(`{"token":"gho_example"}`)})
	if _, err := c.LoginStep(ctx, "", nil); err == nil || !strings.Contains(err.Error(), "代理") || strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("sign-in: %v", err)
	}
	c.check(ctx)
	if st := c.Status(ctx); st.State != connector.StateNotConfigured || !strings.Contains(st.Error, "代理") {
		t.Fatalf("status: %+v", st)
	}
}
