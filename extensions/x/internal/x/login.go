package x

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/x/internal/connector"
)

// Signing in: one fields step with the four values X's developer console gives the owner of an app (the API Key and
// Secret, and the Access Token and Secret generated for their own account). They are checked with GET /2/users/me
// and kept in the hub's sealed connector secret. Every app can draw a fields step, which a link plus a PIN on one
// step (OAuth 1.0a's PIN flow) is not yet.

// stepKeys is the one step.
const stepKeys = "keys"

// Field names.
const (
	fieldAPIKey            = "api_key"
	fieldAPISecret         = "api_secret"
	fieldAccessToken       = "access_token"
	fieldAccessTokenSecret = "access_token_secret"
)

func (c *Connector) keysPrompt(errText string) connector.Prompt {
	return connector.Prompt{Step: stepKeys, Hint: c.tx.hint, Error: errText, Fields: []connector.Field{
		{Name: fieldAPIKey, Label: c.tx.apiKey},
		{Name: fieldAPISecret, Label: c.tx.apiSecret, Secret: true},
		{Name: fieldAccessToken, Label: c.tx.accessToken},
		{Name: fieldAccessTokenSecret, Label: c.tx.accessTokenSecret, Secret: true},
	}}
}

// LoginStep is the keys step: "" asks for them, stepKeys checks what was given.
func (c *Connector) LoginStep(ctx context.Context, step string, values map[string]string) (connector.Prompt, error) {
	switch step {
	case "":
		return c.keysPrompt(""), nil
	case stepKeys:
	default:
		return connector.Prompt{}, fmt.Errorf("unknown step %q", step)
	}
	k := Keys{APIKey: strings.TrimSpace(values[fieldAPIKey]), APISecret: strings.TrimSpace(values[fieldAPISecret]),
		AccessToken: strings.TrimSpace(values[fieldAccessToken]), AccessTokenSecret: strings.TrimSpace(values[fieldAccessTokenSecret])}
	if !k.complete() {
		return c.keysPrompt(c.tx.missing), nil
	}
	var me oneUser
	err := c.api.get(ctx, k, "/2/users/me", url.Values{"user.fields": {"id,name,username"}}, &me)
	var rl *rateLimited
	switch {
	case errors.Is(err, errUnauthorized):
		return c.keysPrompt(c.tx.wrongKeys), nil
	case errors.Is(err, errCredits):
		return c.keysPrompt(c.tx.noCredits), nil
	case errors.As(err, &rl):
		return c.keysPrompt(fmt.Sprintf(c.tx.rateLimited, c.clock(rl.Until))), nil
	case err != nil:
		if ctx.Err() != nil {
			return connector.Prompt{}, ctx.Err()
		}
		log.Printf("sign-in check failed: %s", scrub(err.Error(), k))
		return c.keysPrompt(c.tx.unreachable), nil
	case me.Data == nil || !validID(me.Data.ID) || me.Data.Username == "":
		return c.keysPrompt(c.tx.wrongKeys), nil
	}
	in := signIn{Keys: k, UserID: me.Data.ID, Username: me.Data.Username, Name: me.Data.Name}
	raw, _ := json.Marshal(in)
	c.mu.Lock()
	s := c.secrets
	c.mu.Unlock()
	if s == nil {
		return connector.Prompt{}, errors.New("no secrets store")
	}
	if err := s.Save(ctx, raw); err != nil {
		return connector.Prompt{}, fmt.Errorf("keeping the sign-in: %w", errors.New(scrub(err.Error(), k)))
	}
	c.disk.Lock() // before mu: a save in flight finishes first, and none starts until the wipe is done
	defer c.disk.Unlock()
	c.mu.Lock()
	switched := c.me.ID != "" && c.me.ID != in.UserID
	c.keys, c.me, c.loaded = k, Account{ID: in.UserID, Username: in.Username, Name: in.Name}, true
	c.gen++
	c.status = connector.Status{State: connector.StateOK}
	if switched { // another account: what the last one read is not this one's
		c.forgetLocked()
	}
	c.mu.Unlock()
	if switched {
		_ = c.store.wipe(fileGrok)
	}
	c.Poke()
	return connector.Prompt{Done: true, Account: "@" + in.Username}, nil
}

// Logout forgets the keys and everything read with them: the hub's secret, memory, and every file in the data
// directory but what the Grok source found (not the X sign-in's; grokdigest.go).
func (c *Connector) Logout(ctx context.Context) error {
	c.disk.Lock() // before mu: a save in flight finishes first, then the wipe; a later save finds gen changed
	c.mu.Lock()
	s := c.secrets
	c.keys, c.me = Keys{}, Account{}
	c.gen++
	c.status = connector.Status{State: connector.StateNotSignedIn}
	c.forgetLocked()
	notify := c.notify
	c.mu.Unlock()
	errs := []error{c.store.wipe(fileGrok)}
	c.disk.Unlock()
	if s != nil {
		errs = append(errs, s.Delete(ctx))
	}
	if notify != nil {
		notify()
	}
	return errors.Join(errs...)
}

// forgetLocked drops everything read (c.mu held).
func (c *Connector) forgetLocked() {
	c.following, c.state, c.rateUntil = nil, syncState{}, c.now().Add(-1)
	c.posts, c.mentions = map[string]Post{}, map[string]Post{}
}
