package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
)

// DSN is a Sentry project's client key: https://<public key>@<host>[/<path>]/<project id>.
type DSN struct {
	Scheme, Host, Path, PublicKey, Project string
}

// ParseDSN reads a DSN. Only https is taken unless allowHTTP (the setting allow_http, for a self-hosted instance on a
// trusted network): over plain http the public key and the reports travel unencrypted.
func ParseDSN(s string, allowHTTP bool) (DSN, error) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Host == "" || u.User == nil || u.User.Username() == "" {
		return DSN{}, errors.New("the DSN is https://<public key>@<host>/<project id>")
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && allowHTTP:
	case u.Scheme == "http":
		return DSN{}, errors.New("the DSN is http: turn on the setting allow_http to use it (the key then travels unencrypted)")
	default:
		return DSN{}, errors.New("the DSN must be https")
	}
	path := strings.TrimSuffix(u.Path, "/")
	i := strings.LastIndexByte(path, '/')
	project := path[i+1:]
	if _, err := strconv.ParseUint(project, 10, 64); err != nil {
		return DSN{}, errors.New("the DSN ends with the project's number")
	}
	return DSN{Scheme: u.Scheme, Host: u.Host, Path: path[:i], PublicKey: u.User.Username(), Project: project}, nil
}

// EnvelopeURL is where events go.
func (d DSN) EnvelopeURL() string {
	return fmt.Sprintf("%s://%s%s/api/%s/envelope/", d.Scheme, d.Host, d.Path, d.Project)
}

// String is the DSN without its key, for logs.
func (d DSN) String() string { return fmt.Sprintf("%s://%s%s/%s", d.Scheme, d.Host, d.Path, d.Project) }

// EventID is the Sentry event id of a report: the same report always makes the same event (a retry is one event).
func EventID(r wire.ErrorReport) string {
	sum := sha256.Sum256([]byte(r.Source + "\x00" + r.ID))
	return hex.EncodeToString(sum[:16])
}

// platforms are Sentry's platform names by source (how it shows frames).
var platforms = map[string]string{"hub": "go", "manager": "go", "web": "javascript", "android": "java",
	"ios": "cocoa", "mac": "cocoa", "watch": "cocoa", "vision": "cocoa"}

// Options are what every event says besides the report.
type Options struct {
	Environment string
	Client      string // persona-sentry/<version>
}

// Event turns a report into a Sentry event (the JSON of the envelope's event item).
func Event(r wire.ErrorReport, o Options) map[string]any {
	ev := map[string]any{
		"event_id":    EventID(r),
		"timestamp":   r.Time,
		"platform":    platforms[r.Source],
		"level":       r.Level,
		"logger":      "persona." + r.Source,
		"environment": o.Environment,
		"tags":        tags(r),
		"extra":       map[string]any{"count": r.Count, "previous_launch": r.PreviousLaunch, "received_at": r.ReceivedAt},
		"sdk":         map[string]any{"name": "persona.sentry", "version": strings.TrimPrefix(o.Client, "persona-sentry/")},
	}
	if r.Release != "" {
		rel := "persona-" + r.Source + "@" + r.Release
		if r.Build != "" {
			rel += "+" + r.Build
		}
		ev["release"] = rel
	}
	if r.Build != "" {
		ev["dist"] = r.Build
	}
	if r.Fingerprint != "" {
		ev["fingerprint"] = []string{r.Fingerprint}
	}
	if r.Message != "" {
		ev["message"] = map[string]any{"formatted": r.Message}
	}
	contexts := map[string]any{}
	if r.OS != "" {
		contexts["os"] = map[string]any{"name": r.OS, "version": r.OSVersion}
	}
	if r.DeviceModel != "" {
		contexts["device"] = map[string]any{"model": r.DeviceModel}
	}
	if len(contexts) > 0 {
		ev["contexts"] = contexts
	}
	if len(r.Exceptions) > 0 {
		values := make([]any, 0, len(r.Exceptions))
		// Sentry lists exceptions oldest (the innermost cause) first; reports list the outermost first.
		for i := len(r.Exceptions) - 1; i >= 0; i-- {
			e := r.Exceptions[i]
			x := map[string]any{"type": e.Type, "value": e.Value, "mechanism": map[string]any{"type": r.Kind, "handled": r.Kind == "exception" || r.Kind == "log"}}
			if len(e.Frames) > 0 {
				frames := make([]any, 0, len(e.Frames))
				for _, f := range e.Frames {
					fr := map[string]any{"function": f.Function, "in_app": f.InApp}
					if f.Module != "" {
						fr["module"] = f.Module
					}
					if f.File != "" {
						fr["filename"] = f.File
					}
					if f.Line > 0 {
						fr["lineno"] = f.Line
					}
					if f.Column > 0 {
						fr["colno"] = f.Column
					}
					frames = append(frames, fr)
				}
				x["stacktrace"] = map[string]any{"frames": frames}
			}
			values = append(values, x)
		}
		ev["exception"] = map[string]any{"values": values}
	}
	if len(r.Breadcrumbs) > 0 {
		crumbs := make([]any, 0, len(r.Breadcrumbs))
		for _, c := range r.Breadcrumbs {
			b := map[string]any{"category": c.Category, "message": c.Message}
			if c.Level != "" {
				b["level"] = c.Level
			}
			if t, err := time.Parse(time.RFC3339Nano, c.Time); err == nil {
				b["timestamp"] = float64(t.UnixMilli()) / 1000
			}
			crumbs = append(crumbs, b)
		}
		ev["breadcrumbs"] = map[string]any{"values": crumbs}
	}
	return ev
}

func tags(r wire.ErrorReport) map[string]string {
	out := map[string]string{"source": r.Source, "kind": r.Kind}
	for k, v := range r.Tags {
		if _, taken := out[k]; !taken {
			out[k] = v
		}
	}
	if r.PreviousLaunch {
		out["previous_launch"] = "true"
	}
	return out
}

// Envelope is one event as a Sentry envelope.
func Envelope(d DSN, ev map[string]any) ([]byte, error) {
	body, err := json.Marshal(ev)
	if err != nil {
		return nil, err
	}
	head, _ := json.Marshal(map[string]any{"event_id": ev["event_id"], "dsn": d.String(), "sent_at": time.Now().UTC().Format(time.RFC3339)})
	item, _ := json.Marshal(map[string]any{"type": "event", "length": len(body), "content_type": "application/json"})
	var b bytes.Buffer
	b.Write(head)
	b.WriteByte('\n')
	b.Write(item)
	b.WriteByte('\n')
	b.Write(body)
	b.WriteByte('\n')
	return b.Bytes(), nil
}

// errLimited: Sentry said to wait (429, or a rate limit header); until is when to try again.
type errLimited struct{ until time.Time }

func (e errLimited) Error() string { return "rate limited until " + e.until.Format(time.RFC3339) }

// Send posts one envelope.
func Send(ctx context.Context, c *http.Client, d DSN, client string, envelope []byte, now time.Time) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.EnvelopeURL(), bytes.NewReader(envelope))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-sentry-envelope")
	req.Header.Set("X-Sentry-Auth", fmt.Sprintf("Sentry sentry_version=7, sentry_client=%s, sentry_key=%s", client, d.PublicKey))
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if until, ok := limitedUntil(resp, now); ok {
		return errLimited{until}
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

// limitedUntil reads Sentry's rate limits: X-Sentry-Rate-Limits ("60:error;transaction:key, …": the longest that
// covers errors or every category), else Retry-After on a 429.
func limitedUntil(resp *http.Response, now time.Time) (time.Time, bool) {
	var until time.Time
	if h := resp.Header.Get("X-Sentry-Rate-Limits"); h != "" {
		for _, part := range strings.Split(h, ",") {
			fields := strings.Split(strings.TrimSpace(part), ":")
			secs, err := strconv.Atoi(fields[0])
			if err != nil {
				continue
			}
			cats := ""
			if len(fields) > 1 {
				cats = fields[1]
			}
			if cats == "" || strings.Contains(";"+cats+";", ";error;") || strings.Contains(";"+cats+";", ";default;") {
				if t := now.Add(time.Duration(secs) * time.Second); t.After(until) {
					until = t
				}
			}
		}
		if !until.IsZero() {
			return until, true
		}
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		secs, err := strconv.Atoi(resp.Header.Get("Retry-After"))
		if err != nil || secs <= 0 {
			secs = 60
		}
		return now.Add(time.Duration(secs) * time.Second), true
	}
	return time.Time{}, false
}
