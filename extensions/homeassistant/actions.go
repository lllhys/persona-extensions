package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
)

// The writes: each action's args read into a write, which writes the phone's card (actions.describe, from what is
// cached: no network) and runs after the hub's gate (actions.do).

const (
	runAction   = id + "_run"             // automations, scripts, scenes (automations.go)
	editAction  = id + "_automation_edit" // create / change / delete an automation (automations.go)
	sayAction   = id + "_say"             // a sentence on a speaker (tts.go)
	watchAction = id + "_watch"           // add / remove a watch (watch.go)
)

type write interface {
	summary(a *app) string                           // the card
	run(ctx context.Context, a *app) (string, error) // after the gate
	visible() string                                 // who sees it, for the card
	// pin is what the card showed that must still hold when it runs ("" nothing): the entities of a call by area,
	// the automation's config before a change.
	pin() string
}

// callWrite is homeassistant_call and homeassistant_secure_call.
type callWrite struct{ call }

func (c callWrite) summary(a *app) string                           { return a.summary(c.call) }
func (c callWrite) run(ctx context.Context, a *app) (string, error) { return a.run(ctx, c.call) }
func (c callWrite) visible() string                                 { return "家里的设备（Home Assistant）" }
func (c callWrite) pin() string {
	if c.Area == "" {
		return ""
	}
	return strings.Join(c.Entities, ",")
}

// readWrite reads an action's args (without the network).
func (a *app) readWrite(name string, raw json.RawMessage) (write, error) {
	switch name {
	case callAction, secureAction:
		c, err := a.readCall(name, raw)
		return callWrite{c}, err
	case runAction:
		return a.readRun(raw)
	case editAction:
		return a.readEdit(raw)
	case sayAction:
		return a.readSay(raw)
	case watchAction:
		return a.readWatch(raw)
	}
	return nil, ext.UnknownMethod("connector.action " + name)
}

// pins are what each card showed (by the action and its args), until it runs.
var (
	pinMu sync.Mutex
	pins  = map[string]pinRec{}
)

type pinRec struct {
	val string
	at  time.Time
}

func pinKey(name string, args json.RawMessage) string {
	sum := sha256.Sum256(append([]byte(name+"\x00"), args...))
	return hex.EncodeToString(sum[:])
}

// pin keeps what describe showed.
func pin(name string, args json.RawMessage, val string) {
	if val == "" {
		return
	}
	pinMu.Lock()
	defer pinMu.Unlock()
	for k, p := range pins {
		if time.Since(p.at) > time.Hour {
			delete(pins, k)
		}
	}
	pins[pinKey(name, args)] = pinRec{val, time.Now()}
}

var errChanged = errors.New("确认卡片上的内容和现在的不一样了（Home Assistant 里刚有变化），没有执行：请重新发起，在新的卡片上确认")

// pinned refuses a write whose card showed something else than what would run now.
func pinned(name string, args json.RawMessage, now string) error {
	pinMu.Lock()
	defer pinMu.Unlock()
	k := pinKey(name, args)
	p, ok := pins[k]
	if !ok {
		return nil
	}
	delete(pins, k)
	if p.val != now {
		return &ext.Error{Code: "conflict", Message: errChanged.Error()}
	}
	return nil
}
