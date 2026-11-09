package ilink

import "errors"

// Code is a stable error class; messages never carry tokens, QR codes or message text.
type Code string

const (
	CodeAborted      Code = "aborted"
	CodeTimeout      Code = "timeout"
	CodeNetwork      Code = "network"
	CodeHTTP         Code = "http"
	CodeBadResponse  Code = "bad_response"
	CodeProtocol     Code = "protocol"
	CodeReplaced     Code = "replaced"      // another client took the bot token: authorize again
	CodeAuthExpired  Code = "auth_expired"  // the QR code expired too often, or the authorization is gone
	CodeAuthBlocked  Code = "auth_blocked"  // too many wrong verification codes
	CodeAlreadyBound Code = "already_bound" // this WeChat already has this bot
)

// Error is what the package returns; Retry says whether trying the same call again later can work.
type Error struct {
	Code  Code
	Msg   string
	Retry bool
	err   error
}

func (e *Error) Error() string { return e.Msg }
func (e *Error) Unwrap() error { return e.err }

// CodeOf is the Code of err, "" when err is not an *Error.
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// Retryable says whether err may go away by itself.
func Retryable(err error) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Retry
	}
	return true
}
