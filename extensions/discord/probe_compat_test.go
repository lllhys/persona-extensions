package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These public compatibility identifiers belong only to the synthetic fixture.
// A resident node uses this complete unauthenticated shape to discover dev ports.
var nodePortProbeAgents = []string{"persona-ports", "jarvis-ports"}

func isNodePortProbe(r *http.Request) bool {
	if r.Method != http.MethodGet || r.URL == nil || r.RequestURI != "/" || r.URL.Path != "/" ||
		r.URL.RawPath != "" || r.URL.RawQuery != "" || r.URL.User != nil || r.ProtoMajor != 1 || r.ProtoMinor != 1 ||
		r.ContentLength != 0 || len(r.TransferEncoding) != 0 || r.Body != nil && r.Body != http.NoBody || len(r.Header) != 3 {
		return false
	}
	for name, values := range r.Header {
		if len(values) != 1 {
			return false
		}
		switch name {
		case "User-Agent", "Accept", "Connection":
		default:
			return false
		}
	}
	if r.Header.Get("Accept") != "text/html" || r.Header.Get("Connection") != "close" {
		return false
	}
	for _, agent := range nodePortProbeAgents {
		if r.UserAgent() == agent {
			return true
		}
	}
	return false
}

func fixturePortProbe(agent string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("User-Agent", agent)
	r.Header.Set("Accept", "text/html")
	r.Header.Set("Connection", "close")
	return r
}

func TestFakeDiscordExcludesOnlyNodePortProbes(t *testing.T) {
	for _, agent := range nodePortProbeAgents {
		t.Run(agent, func(t *testing.T) {
			f := newFakeDiscord(t)
			r := fixturePortProbe(agent)
			w := httptest.NewRecorder()
			f.serve(w, r)
			if w.Code != http.StatusNoContent || f.requested("") != 0 {
				t.Fatalf("node probe: status %d, requests %d", w.Code, f.requested(""))
			}
		})
	}
	cases := []struct {
		name string
		edit func(*http.Request)
	}{
		{"ordinary-root", func(r *http.Request) { r.Header.Set("User-Agent", "example-client") }},
		{"post-root", func(r *http.Request) { r.Method = http.MethodPost }},
		{"api", func(r *http.Request) {
			r.URL.Path, r.RequestURI = "/api/v10/applications/@me", "/api/v10/applications/@me"
		}},
		{"query", func(r *http.Request) { r.URL.RawQuery, r.RequestURI = "x=1", "/?x=1" }},
		{"escaped-root", func(r *http.Request) { r.URL.RawPath, r.RequestURI = "%2F", "%2F" }},
		{"body", func(r *http.Request) {
			r.Body = http.NoBody
			r.ContentLength = int64(len("payload"))
		}},
		{"empty-reader-body", func(r *http.Request) { r.Body = httptest.NewRequest(http.MethodGet, "/", strings.NewReader("")).Body }},
		{"authorization", func(r *http.Request) { r.Header.Set("Authorization", "Bot "+fakeToken) }},
		{"empty-authorization", func(r *http.Request) { r.Header.Set("Authorization", "") }},
		{"proxy-authorization", func(r *http.Request) { r.Header.Set("Proxy-Authorization", "example") }},
		{"extra-header", func(r *http.Request) { r.Header.Set("X-Example", "value") }},
		{"duplicate-agent", func(r *http.Request) { r.Header.Add("User-Agent", nodePortProbeAgents[0]) }},
		{"wrong-accept", func(r *http.Request) { r.Header.Set("Accept", "application/json") }},
		{"http2", func(r *http.Request) { r.ProtoMajor, r.ProtoMinor = 2, 0 }},
		{"cookie", func(r *http.Request) { r.Header.Set("Cookie", "example=value") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeDiscord(t)
			r := fixturePortProbe(nodePortProbeAgents[0])
			tc.edit(r)
			w := httptest.NewRecorder()
			f.serve(w, r)
			if f.requested("") != 1 {
				t.Fatalf("ordinary request was excluded: status %d, requests %d", w.Code, f.requested(""))
			}
		})
	}
}
