package telegram

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/telegram/internal/connector"
)

// Sign-in steps (connector.Login): the API credentials, the phone number, the code, the 2FA password.
const (
	stepCredentials = "credentials"
	stepPhone       = "phone"
	stepCode        = "code"
	stepPassword    = "password"
)

const credentialsHint = "去 my.telegram.org，用你的 Telegram 账号登录，进 API development tools 建一个应用，把 api_id 和 api_hash 填到这里。" +
	"它们和登录后的会话一起加密存在 hub 上，退出登录时一起删掉。"

func credentialsPrompt(problem string) connector.Prompt {
	return connector.Prompt{Step: stepCredentials, Hint: credentialsHint, Error: problem, Fields: []connector.Field{
		{Name: "api_id", Label: "api_id", Placeholder: "1234567"},
		{Name: "api_hash", Label: "api_hash", Secret: true, Placeholder: "32 位字母和数字"},
	}}
}

func phonePrompt(problem string) connector.Prompt {
	return connector.Prompt{Step: stepPhone, Error: problem, Hint: "带国家区号，比如 +86 开头。",
		Fields: []connector.Field{{Name: "phone", Label: "手机号", Placeholder: "+1 555 0100"}}}
}

func codePrompt(via, problem string) connector.Prompt {
	return connector.Prompt{Step: stepCode, Hint: via, Error: problem,
		Fields: []connector.Field{{Name: "code", Label: "验证码", Placeholder: "12345"}}}
}

func passwordPrompt(hint, problem string) connector.Prompt {
	h := "这个账号开了两步验证，填它的密码。"
	if hint != "" {
		h += "密码提示：" + hint
	}
	return connector.Prompt{Step: stepPassword, Hint: h, Error: problem,
		Fields: []connector.Field{{Name: "password", Label: "两步验证密码", Secret: true}}}
}

var apiHashForm = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// connectWait is how long a step waits for the client to connect with new credentials.
var connectWait = 30 * time.Second

// LoginStep goes one step of the sign-in. A mistake the user can fix (a wrong code) asks the same step again with a
// reason; an error is for what should not happen.
func (c *Conn) LoginStep(ctx context.Context, step string, v map[string]string) (connector.Prompt, error) {
	st, err := c.load(ctx)
	if err != nil {
		return connector.Prompt{}, err
	}
	switch step {
	case "":
		if !st.hasCredentials() {
			return credentialsPrompt(""), nil
		}
		if s := c.Status(ctx); s.State == connector.StateOK || s.State == connector.StateRateLimited { // signed in, waiting
			return connector.Prompt{Done: true, Account: s.Account}, nil
		}
		return phonePrompt(""), nil
	case stepCredentials:
		return c.stepCredentials(ctx, v)
	}
	if !st.hasCredentials() {
		return credentialsPrompt(""), nil
	}
	cl := c.waitClient(ctx)
	if cl == nil {
		return phonePrompt("连不上 Telegram，稍后再试（" + c.Status(ctx).Error + "）"), nil
	}
	switch step {
	case stepPhone:
		phone := strings.TrimSpace(v["phone"])
		if phone == "" {
			return phonePrompt("填上手机号"), nil
		}
		hash, via, err := cl.SendCode(ctx, phone)
		if err != nil {
			if why, ok := userError(err); ok {
				return phonePrompt(why), nil
			}
			return connector.Prompt{}, scrubbed(err)
		}
		c.mu.Lock()
		c.pending.phone, c.pending.hash = phone, hash
		c.mu.Unlock()
		return codePrompt(via, ""), nil
	case stepCode:
		c.mu.Lock()
		phone, hash := c.pending.phone, c.pending.hash
		c.mu.Unlock()
		if hash == "" {
			return phonePrompt("先填手机号收验证码"), nil
		}
		me, err := cl.SignIn(ctx, phone, strings.TrimSpace(v["code"]), hash)
		switch {
		case err == errPasswordNeeded:
			hint, _ := cl.PasswordHint(ctx)
			return passwordPrompt(hint, ""), nil
		case isError(err, "PHONE_CODE_EXPIRED"):
			return phonePrompt("验证码过期了，重新收一个"), nil
		case err != nil:
			if why, ok := userError(err); ok {
				return codePrompt("", why), nil
			}
			return connector.Prompt{}, scrubbed(err)
		}
		return c.signedIn(ctx, me), nil
	case stepPassword:
		me, err := cl.Password(ctx, v["password"])
		if err != nil {
			if why, ok := userError(err); ok {
				hint, _ := cl.PasswordHint(ctx)
				return passwordPrompt(hint, why), nil
			}
			return connector.Prompt{}, scrubbed(err)
		}
		return c.signedIn(ctx, me), nil
	}
	return phonePrompt(""), nil
}

// stepCredentials checks the API credentials against Telegram and keeps them; the client connects with them.
func (c *Conn) stepCredentials(ctx context.Context, v map[string]string) (connector.Prompt, error) {
	id, err := strconv.Atoi(strings.TrimSpace(v["api_id"]))
	hash := strings.TrimSpace(v["api_hash"])
	if err != nil || id <= 0 || !apiHashForm.MatchString(hash) {
		return credentialsPrompt("api_id 是一串数字，api_hash 是 32 位字母和数字，照 my.telegram.org 上的填"), nil
	}
	cctx, cancel := context.WithTimeout(ctx, connectWait)
	defer cancel()
	probe, err := c.dial(cctx, creds{id, hash}, &memSession{}, c.gate, func() {}) // the same gate: the same account's machine
	if err == nil {
		err = probe.Check(cctx)
	}
	cancel() // the probe's connection ends here
	if err != nil {
		if isError(err, "API_ID_INVALID", "API_ID_PUBLISHED_FLOOD") {
			return credentialsPrompt("Telegram 不认这个 api_id / api_hash"), nil
		}
		if why, ok := userError(err); ok { // a flood wait
			return credentialsPrompt(why), nil
		}
		return credentialsPrompt("连不上 Telegram，稍后再试（" + explain(err) + "）"), nil
	}
	c.mu.Lock()
	c.st = stored{APIID: id, APIHash: hash}
	c.pending.phone, c.pending.hash = "", ""
	err = c.saveLocked(ctx)
	c.mu.Unlock()
	if err != nil {
		return connector.Prompt{}, err
	}
	c.kick()
	return phonePrompt(""), nil
}

// waitClient waits for Run to connect (the credentials just changed), or gives nil.
func (c *Conn) waitClient(ctx context.Context) client {
	deadline := time.Now().Add(connectWait)
	for {
		c.mu.Lock()
		cl := c.cl
		c.mu.Unlock()
		if cl != nil || time.Now().After(deadline) || ctx.Err() != nil {
			return cl
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (c *Conn) signedIn(ctx context.Context, me string) connector.Prompt {
	c.mu.Lock()
	c.pending.phone, c.pending.hash = "", ""
	c.st.Account = me
	_ = c.saveLocked(ctx)
	via := c.st.via()
	c.mu.Unlock()
	c.setStatus(connector.Status{State: connector.StateOK, Account: me, Via: via})
	c.updated() // the inbox and the archive start now
	return connector.Prompt{Done: true, Account: me}
}

// Logout ends the session at Telegram and forgets the credentials and the session.
func (c *Conn) Logout(ctx context.Context) error {
	if _, err := c.load(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	cl := c.cl
	c.mu.Unlock()
	if cl != nil {
		// Only a session Telegram says is not signed in is forgotten without auth.logOut: when it could not be asked
		// (a flood wait, the network), the session is kept for another try, as it is still signed in there.
		_, ok, err := cl.Me(ctx)
		if err != nil {
			return c.writeError(err)
		}
		if ok {
			if err := cl.LogOut(ctx); err != nil {
				return scrubbed(err)
			}
		}
	}
	c.mu.Lock()
	c.st, c.pending.phone, c.pending.hash = stored{}, "", ""
	err := c.saveLocked(ctx)
	c.mu.Unlock()
	c.setStatus(connector.Status{State: connector.StateNotSignedIn, Error: "还没填 Telegram 的 API 凭据"})
	c.kick()
	return err
}

// memSession keeps a session in memory: the credentials' probe connection.
type memSession struct{ data []byte }

func (m *memSession) LoadSession(context.Context) ([]byte, error) {
	if m.data == nil {
		return nil, errNoSession
	}
	return m.data, nil
}

func (m *memSession) StoreSession(_ context.Context, d []byte) error {
	m.data = d
	return nil
}
