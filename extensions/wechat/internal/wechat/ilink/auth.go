package ilink

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

// QR is one authorization QR code. Content is what WeChat scans (draw it as a QR code; opening it as a link does
// nothing useful); Code is the handle for polling its status and must not be shown.
type QR struct {
	Code    string
	Content string
}

// BeginQR asks for a new authorization QR code. localTokens are bot tokens this client had before (at most the last
// 10 are sent), so iLink can tell a re-authorization from a new bot.
func (c *Client) BeginQR(ctx context.Context, localTokens []string) (QR, error) {
	if len(localTokens) > 10 {
		localTokens = localTokens[len(localTokens)-10:]
	}
	if localTokens == nil {
		localTokens = []string{}
	}
	auth, _ := url.Parse(AuthOrigin + "/")
	out, err := c.call(ctx, http.MethodPost, "ilink/bot/get_bot_qrcode?bot_type=3",
		map[string]any{"local_token_list": localTokens}, false, apiTimeout, auth)
	if err != nil {
		return QR{}, err
	}
	q := QR{Code: str(out, "qrcode"), Content: str(out, "qrcode_img_content")}
	if q.Code == "" {
		return QR{}, &Error{Code: CodeBadResponse, Msg: "iLink answered no QR code", Retry: true}
	}
	u, err := url.Parse(q.Content)
	if err != nil || u.Scheme != "https" || u.User != nil || !tencentHost(u.Hostname()) {
		return QR{}, &Error{Code: CodeBadResponse, Msg: "iLink answered an invalid QR code", Retry: true}
	}
	return q, nil
}

// QRState is where an authorization stands.
type QRState string

const (
	QRWaiting      QRState = "wait"
	QRScanned      QRState = "scaned" // sic, the wire value
	QRConfirmed    QRState = "confirmed"
	QRExpired      QRState = "expired"
	QRRedirect     QRState = "scaned_but_redirect"
	QRNeedCode     QRState = "need_verifycode"
	QRCodeBlocked  QRState = "verify_code_blocked"
	QRBoundAlready QRState = "binded_redirect"
)

// Credentials are what a confirmed authorization gives: keep them secret.
type Credentials struct {
	Token   string `json:"token"`
	BotID   string `json:"bot_id"`
	UserID  string `json:"user_id"` // the WeChat user who scanned: the bot's owner
	BaseURL string `json:"base_url"`
}

// QRStatus is one answer of PollQR.
type QRStatus struct {
	State QRState
	// RedirectBase is the origin to poll from now on (QRRedirect).
	RedirectBase string
	// Credentials when State is QRConfirmed.
	Credentials Credentials
}

var redirectHost = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)

// PollQR asks how the authorization of code stands, from base ("" for AuthOrigin). It long-polls for up to wait
// (0: iLink's 35 seconds); running out of time answers QRWaiting. verifyCode is the number the user typed after
// QRNeedCode, "" otherwise.
func (c *Client) PollQR(ctx context.Context, code, verifyCode, base string, wait time.Duration) (QRStatus, error) {
	if base == "" {
		base = AuthOrigin
	}
	b, err := ValidateBaseURL(base)
	if err != nil {
		return QRStatus{}, err
	}
	if wait <= 0 {
		wait = longPollTimeout
	}
	q := url.Values{"qrcode": {code}}
	if verifyCode != "" {
		q.Set("verify_code", verifyCode)
	}
	out, err := c.call(ctx, http.MethodGet, "ilink/bot/get_qrcode_status?"+q.Encode(), nil, false, wait, b)
	if err != nil {
		var e *Error
		if errors.As(err, &e) && e.Code == CodeTimeout {
			return QRStatus{State: QRWaiting}, nil
		}
		return QRStatus{}, err
	}
	st := QRStatus{State: QRState(str(out, "status"))}
	switch st.State {
	case QRWaiting, QRScanned, QRExpired, QRNeedCode, QRCodeBlocked, QRBoundAlready:
	case QRRedirect:
		h := str(out, "redirect_host")
		if !redirectHost.MatchString(h) {
			return QRStatus{}, &Error{Code: CodeBadResponse, Msg: "iLink answered an invalid redirect", Retry: true}
		}
		r, err := ValidateBaseURL("https://" + h)
		if err != nil {
			return QRStatus{}, err
		}
		st.RedirectBase = r.String()
	case QRConfirmed:
		cr := Credentials{Token: str(out, "bot_token"), BotID: str(out, "ilink_bot_id"), UserID: str(out, "ilink_user_id"), BaseURL: str(out, "baseurl")}
		if cr.Token == "" || cr.BotID == "" || cr.UserID == "" || cr.BaseURL == "" {
			return QRStatus{}, &Error{Code: CodeBadResponse, Msg: "iLink confirmed without credentials", Retry: true}
		}
		u, err := ValidateBaseURL(cr.BaseURL)
		if err != nil {
			return QRStatus{}, err
		}
		cr.BaseURL = u.String()
		st.Credentials = cr
	default:
		return QRStatus{}, &Error{Code: CodeBadResponse, Msg: "iLink answered an unknown authorization status", Retry: true}
	}
	return st, nil
}
