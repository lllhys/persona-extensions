package github

import (
	"context"
	"fmt"

	"github.com/nwverse/persona-extensions/extensions/github/internal/connector"
)

// What running as an extension adds to the connector the hub had built in: the settings come from the extension's
// form instead of hub.json, a computer's gh login comes through the hub (connector.credentials.gh_token), the user's
// triage rules and a fallback tier go on every inbox thing, and the inbox kinds describe themselves.

// Settings is the extension's configuration (persona-extension.json's form).
type Settings struct {
	Host  string `json:"host,omitempty"`
	Proxy string `json:"proxy,omitempty"`
	// TriageRules are the user's own rules for GitHub's inbox items (pluginkit.TriageRules reads them).
	TriageRules string `json:"triage_rules,omitempty"`
	// Inbox: the repositories and kinds that come into the inbox (filter.go).
	Inbox InboxFilter `json:"inbox,omitempty"`
}

// Config is the connector's configuration for s.
func (s Settings) Config() Config {
	return Config{Auth: AuthConfig{Host: s.Host}, Egress: connector.Egress{Proxy: s.Proxy}}
}

// KindDescriptions are the inbox kinds as the models read them, word for word what the hub's prompt for GitHub
// (prompts/inbox/kinds/github.md) said after「类型：」, so items read the same whoever serves the connector.
var KindDescriptions = map[string]string{
	"review_requested": "GitHub 上有人请用户 review 一个 PR（用户在 reviewers 里），一般需要用户",
	"mention":          "GitHub 的 PR / issue 里 @ 了用户",
	"team_mention":     "GitHub 上 @ 了用户所在的团队（不是用户本人），多数知悉即可",
	"author":           "用户自己的 PR / issue 有了别人的新评论或 review；要用户改代码、回答问题时需要用户，LGTM、批准、机器人消息知悉即可",
	"ci_failed":        "用户触发的 GitHub Actions（CI）失败了",
	"assign":           "GitHub 上有 issue / PR 分配给了用户",
}

// WithRules sets the user's triage rules, carried on every inbox thing ("" leaves the inbox's global rules).
func (c *Connector) WithRules(rules string) *Connector {
	c.mu.Lock()
	c.rules = rules
	c.mu.Unlock()
	return c
}

// stamped is t as the inbox gets it from an extension: the user's rules, and a fallback tier for when no model sorts
// it (the rules' tier; fyi when the rules leave it to the model, as a person's ordinary comment on the user's PR).
func (c *Connector) stamped(t connector.InboxThing) connector.InboxThing {
	c.mu.Lock()
	t.Rules = c.rules
	c.mu.Unlock()
	tier := t.Tier
	if !tier.Known() {
		tier = connector.TierFYI
	}
	extra := connector.ThingExtra{}
	if t.Extra != nil {
		extra = *t.Extra
	}
	extra.RuleTier = string(tier)
	t.Extra = &extra
	return t
}

// UseHub reads a computer's gh login through fetch (the hub's connector.credentials.gh_token) when the user picks
// one.
func (c *Connector) UseHub(fetch func(ctx context.Context, machine, host string) (string, error)) {
	c.mu.Lock()
	c.hubFetch = fetch
	c.mu.Unlock()
}

// Auth is where the sign-in comes from now, and what its token may do.
func (c *Connector) Auth() connector.AuthState {
	scopes, known := c.Scopes()
	return connector.AuthState{Source: c.Source(), Machine: c.MachineID(), Scopes: scopes, ScopesKnown: known}
}

// SetAuth takes the user's choice, as the hub keeps it: a computer's gh login, or the device flow.
func (c *Connector) SetAuth(ctx context.Context, ch connector.AuthChoice) (connector.AuthState, error) {
	switch ch.Source {
	case SourceMachine:
		if ch.Machine == "" {
			return connector.AuthState{}, fmt.Errorf("source %s without a machine", ch.Source)
		}
		c.mu.Lock()
		fetch := c.hubFetch
		c.mu.Unlock()
		name := ch.MachineName
		if name == "" {
			name = ch.Machine
		}
		machine := ch.Machine
		var ts TokenSource = func(context.Context, string) (string, error) { return "", ErrHubTooOld }
		if fetch != nil {
			ts = func(ctx context.Context, host string) (string, error) { return fetch(ctx, machine, host) }
		}
		c.UseMachine(ctx, machine, name, ts)
	case SourceDevice:
		c.UseDevice()
	default:
		return connector.AuthState{}, fmt.Errorf("unknown source %q", ch.Source)
	}
	return c.Auth(), nil
}
