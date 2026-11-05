package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tgerr"
)

// isError says whether err is one of Telegram's RPC errors of these types (PHONE_CODE_INVALID…).
func isError(err error, types ...string) bool {
	return err != nil && tgerr.Is(err, types...)
}

// userError is a mistake the user can fix, said for people (the sign-in asks the same step again with it).
func userError(err error) (string, bool) {
	var held heldError
	if errors.As(err, &held) {
		return held.Error(), true
	}
	if d, ok := tgerr.AsFloodWait(err); ok {
		return fmt.Sprintf("Telegram 说请求太频繁，%d 秒后再试", int(d.Seconds())), true
	}
	if errors.Is(err, auth.ErrPasswordInvalid) {
		return "密码不对", true
	}
	for _, m := range []struct {
		types []string
		say   string
	}{
		{[]string{"PHONE_NUMBER_INVALID"}, "手机号不对（要带国家区号）"},
		{[]string{"PHONE_NUMBER_BANNED"}, "这个号码被 Telegram 停用了"},
		{[]string{"PHONE_NUMBER_UNOCCUPIED"}, "这个号码还没注册 Telegram"},
		{[]string{"PHONE_NUMBER_FLOOD"}, "这个号码最近收验证码太多次了，过一阵再试"},
		{[]string{"PHONE_CODE_INVALID", "PHONE_CODE_EMPTY"}, "验证码不对"},
		{[]string{"PASSWORD_HASH_INVALID"}, "密码不对"},
	} {
		if isError(err, m.types...) {
			return m.say, true
		}
	}
	return "", false
}

// explain is a short reason for a log or a status: the RPC error's type, or the error itself. Telegram's errors
// carry no secrets (not the API hash, the session, a phone number or a code), but a network error could carry an
// address: it is kept to one line.
func explain(err error) string {
	if err == nil {
		return ""
	}
	if d, ok := tgerr.AsFloodWait(err); ok {
		return fmt.Sprintf("限流，%d 秒后再试", seconds(d))
	}
	if rpc, ok := tgerr.As(err); ok {
		return rpc.Type
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "超时"
	}
	line, _, _ := strings.Cut(err.Error(), "\n")
	if len(line) > 200 {
		line = line[:200] + "…"
	}
	return line
}

// scrubbed is err as explain says it: what leaves the program.
func scrubbed(err error) error { return errors.New(explain(err)) }
