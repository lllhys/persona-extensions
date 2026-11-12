package main

// The hub program's way to 1Password through op on the user's computer (#376): the same reads as the service
// account's client (opAPI), each a call to the program on that computer (node.go) through the hub's nodes.call. What
// comes back is already clean of values; only resolve answers one.

import (
	"context"
	"errors"
	"time"

	op "github.com/1password/onepassword-sdk-go"
	"github.com/nwverse/persona/sdk/go/ext"
)

// nodeCaller calls a method of the program on a computer: the hub's nodes.call, or a fake in tests.
type nodeCaller func(ctx context.Context, machine, method string, params, out any) error

func hubNodeCaller(hub *ext.Hub) nodeCaller {
	return func(ctx context.Context, machine, method string, params, out any) error {
		return hub.Call(ctx, "nodes.call", map[string]any{"machine": machine, "method": method, "params": params}, out)
	}
}

type nodeAPI struct {
	call    nodeCaller
	machine string
}

// ask calls method on the computer with params and how long this side still waits (within_ms), so op there gives up
// when the caller does rather than finishing (reading a value, say) for nobody.
func (n nodeAPI) ask(ctx context.Context, method string, params map[string]any, out any) error {
	if params == nil {
		params = map[string]any{}
	}
	if d, ok := ctx.Deadline(); ok {
		params["within_ms"] = max(time.Until(d).Milliseconds(), 1)
	}
	return n.call(ctx, n.machine, method, params, out)
}

func (n nodeAPI) listVaults(ctx context.Context) ([]op.VaultOverview, error) {
	var out []op.VaultOverview
	return out, n.ask(ctx, "op.vaults", nil, &out)
}

func (n nodeAPI) listItems(ctx context.Context, vaultID string) ([]op.ItemOverview, error) {
	var out []op.ItemOverview
	return out, n.ask(ctx, "op.items", map[string]any{"vault": vaultID}, &out)
}

func (n nodeAPI) getItem(ctx context.Context, vaultID, itemID string) (op.Item, error) {
	var out op.Item
	return out, n.ask(ctx, "op.item", map[string]any{"vault": vaultID, "item": itemID}, &out)
}

func (n nodeAPI) resolve(ctx context.Context, reference string) (string, error) {
	var out struct {
		Value string `json:"value"`
	}
	return out.Value, n.ask(ctx, "op.read", map[string]any{"reference": reference}, &out)
}

// opStatus asks the computer whether op is installed (op --version: asking the 1Password app nothing).
func (n nodeAPI) opStatus(ctx context.Context) (nodeStatus, error) {
	var out nodeStatus
	return out, n.ask(ctx, "op.status", nil, &out)
}

// opCode is the code of an error from the computer's program, or from the hub about that computer ("offline").
func opCode(err error) string {
	var e *ext.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// opWords are the title and guide the page and the secret provider give for an error of op on a computer that needs
// the user (op missing, no account, locked or not approved, the computer offline); ok is false for any other error.
func opWords(t *texts, err error) (title, guide string, ok bool) {
	switch opCode(err) {
	case codeMissing:
		return t.opMissing, t.opMissingGuide, true
	case codeNotSignedIn:
		return t.opNoAccount, t.opNoAccountGuide, true
	case codeLocked:
		return t.opLocked, t.opLockedGuide, true
	case "offline":
		return t.opOffline, t.opOfflineGuide, true
	}
	return "", "", false
}
