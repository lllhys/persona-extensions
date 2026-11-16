package ext

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
)

// wireClient is the hub's end of the pipes: one request at a time, answers in order.
type wireClient struct {
	t   *testing.T
	in  io.WriteCloser
	out *bufio.Scanner
	n   int
}

func start(t *testing.T, prog Program) *wireClient {
	progR, hubW := io.Pipe()
	hubR, progW := io.Pipe()
	go func() { _ = Serve(context.Background(), progR, progW, prog); _ = progW.Close() }()
	t.Cleanup(func() { hubW.Close() })
	return &wireClient{t: t, in: hubW, out: bufio.NewScanner(hubR)}
}

func (c *wireClient) call(method string, params any) frame {
	c.t.Helper()
	c.n++
	raw, _ := json.Marshal(params)
	b, _ := json.Marshal(frame{ID: "h" + string(rune('0'+c.n)), Method: method, Params: raw})
	if _, err := c.in.Write(append(b, '\n')); err != nil {
		c.t.Fatal(err)
	}
	for c.out.Scan() {
		var f frame
		if json.Unmarshal(c.out.Bytes(), &f) == nil && f.ID != "" && f.Method == "" {
			return f
		}
	}
	c.t.Fatalf("%s: no answer", method)
	return frame{}
}

type coded struct{}

func (coded) Error() string                        { return "no such thing" }
func (coded) WireError() (string, json.RawMessage) { return "not_found", json.RawMessage(`{"id":"x"}`) }

func TestServe(t *testing.T) {
	var config struct {
		Name string `json:"name"`
	}
	c := start(t, Program{ID: "example", Version: "1.0.0",
		Init: func(ctx context.Context, in *Init, hub *Hub) (map[string]any, error) {
			return nil, in.Config(&config)
		},
		Namespaces: map[string]Handler{"thing": func(ctx context.Context, method string, params json.RawMessage) (any, error) {
			return nil, coded{}
		}},
		UI: map[string]Page{"home": {
			Render: func(ctx context.Context, r *UIRequest) (*wire.View, error) {
				return &wire.View{Title: r.Action + "|" + r.State, Sections: []wire.Section{}}, nil
			},
			Action: func(ctx context.Context, r *UIRequest) (*wire.ActionResult, error) {
				if r.Action == "toast" {
					return &wire.ActionResult{Toast: "好"}, nil
				}
				if r.Action == "lines" {
					return &wire.ActionResult{Lines: &wire.FileLines{Path: "a.go", Start: 1, Lines: []string{"x"}}}, nil
				}
				if r.Action == "viewed" {
					return &wire.ActionResult{Items: []wire.Component{}}, nil
				}
				r.State = "next"
				return nil, nil
			},
		}},
	})
	if f := c.call("ping", nil); f.Error == nil || f.Error.Code != wire.ErrorCodeNotInitialized {
		t.Fatalf("ping before initialize: %+v", f)
	}
	// A hub from before the rename sends the extension under plugin.
	f := c.call("initialize", map[string]any{"protocol": 1, "hub": map[string]any{"build": 1}, "plugin": map[string]any{"id": "example", "version": "1.0.0"},
		"config": map[string]any{"name": "示例"}, "granted": map[string]any{}})
	if f.Error != nil || config.Name != "示例" {
		t.Fatalf("initialize: %+v %+v", f, config)
	}
	if f := c.call("thing.get", nil); f.Error == nil || f.Error.Code != "not_found" || string(f.Error.Data) != `{"id":"x"}` {
		t.Fatalf("a coded error: %+v", f.Error)
	}
	if f := c.call("ui.render", wire.UIParams{Contribution: "nope"}); f.Error == nil || f.Error.Code != wire.ErrorCodeNoContribution {
		t.Fatalf("ui.render of another contribution: %+v", f.Error)
	}
	var res wire.ActionResult
	f = c.call("ui.action", wire.UIParams{Contribution: "home", Action: "more", State: "first"})
	if f.Error != nil || json.Unmarshal(f.Result, &res) != nil || res.View == nil || res.View.Title != "more|next" {
		t.Fatalf("an action that answers nothing draws again, with the state it set: %s %+v", f.Result, f.Error)
	}
	res = wire.ActionResult{}
	f = c.call("ui.action", wire.UIParams{Contribution: "home", Action: "toast"})
	if json.Unmarshal(f.Result, &res) != nil || res.Toast != "好" || res.View != nil {
		t.Fatalf("toast: %s", f.Result)
	}
	// UI version 5: lines, or components in place (none: done), answer as they are
	for _, a := range []string{"lines", "viewed"} {
		res = wire.ActionResult{}
		f = c.call("ui.action", wire.UIParams{Contribution: "home", Action: a, UIVersion: 5})
		if json.Unmarshal(f.Result, &res) != nil || res.View != nil || (a == "lines") != (res.Lines != nil) {
			t.Fatalf("%s: %s", a, f.Result)
		}
	}
	if f := c.call("other.method", nil); f.Error == nil || f.Error.Code != wire.ErrorCodeUnknownMethod {
		t.Fatalf("unknown method: %+v", f.Error)
	}
}

// An app before UI version 3 does not know push and pop: it calls ui.action with the action's id, and gets the pushed
// page in place of this one; a newer app gets the push as written.
func TestPushForOlderApps(t *testing.T) {
	list := func(ctx context.Context, r *UIRequest) (*wire.View, error) {
		if r.State != "" {
			return &wire.View{Title: "详情 " + r.State, Sections: []wire.Section{}}, nil
		}
		return &wire.View{Title: "列表", Sections: []wire.Section{{Items: []wire.Component{
			{Type: wire.ComponentRow, Title: "第一条", Action: Open("open", "", "1", "第一条")},
		}}}}, nil
	}
	c := start(t, Program{ID: "example", Version: "1.0.0", UI: map[string]Page{"home": {Render: list,
		Action: func(ctx context.Context, r *UIRequest) (*wire.ActionResult, error) {
			if r.Action == "save" {
				return &wire.ActionResult{Pop: true, Toast: "存好了"}, nil
			}
			return nil, nil
		}}}})
	c.call("initialize", map[string]any{"protocol": 1, "hub": map[string]any{"build": 1}, "granted": map[string]any{}})
	var v wire.View
	f := c.call("ui.render", wire.UIParams{Contribution: "home", UIVersion: 3})
	_ = json.Unmarshal(f.Result, &v)
	if a := v.Sections[0].Items[0].Action; a.ID != "open" || a.Push == nil || a.Push.State != `"1"` {
		t.Fatalf("a newer app gets the push as written: %+v", a)
	}
	f = c.call("ui.render", wire.UIParams{Contribution: "home", UIVersion: 2})
	_ = json.Unmarshal(f.Result, &v)
	id := v.Sections[0].Items[0].Action.ID
	if !strings.HasPrefix(id, pushPrefix) {
		t.Fatalf("an older app gets an id of the SDK's: %q", id)
	}
	var res wire.ActionResult
	f = c.call("ui.action", wire.UIParams{Contribution: "home", UIVersion: 2, Action: id})
	if json.Unmarshal(f.Result, &res); res.View == nil || res.View.Title != `详情 "1"` {
		t.Fatalf("the pushed page in place: %s %+v", f.Result, f.Error)
	}
	res = wire.ActionResult{}
	f = c.call("ui.action", wire.UIParams{Contribution: "home", UIVersion: 2, Action: "save", State: `"1"`})
	if json.Unmarshal(f.Result, &res); res.View == nil || res.View.Title != "列表" || res.Toast != "存好了" || res.Pop {
		t.Fatalf("a pop is the page as first drawn: %s", f.Result)
	}
	res = wire.ActionResult{}
	f = c.call("ui.action", wire.UIParams{Contribution: "home", UIVersion: 3, Action: "save"})
	if json.Unmarshal(f.Result, &res); !res.Pop || res.View != nil {
		t.Fatalf("a newer app gets the pop: %s", f.Result)
	}
}

func TestShortcuts(t *testing.T) {
	c := start(t, Program{ID: "example", Version: "1.0.0", Shortcuts: map[string]Shortcut{
		"count": func(ctx context.Context, r *ShortcutRequest) (*wire.ShortcutResult, error) {
			return &wire.ShortcutResult{Text: r.Values["what"] + "：3 个"}, nil
		}}})
	c.call("initialize", map[string]any{"protocol": 1, "hub": map[string]any{"build": 1}, "granted": map[string]any{}})
	var res wire.ShortcutResult
	f := c.call("shortcut.run", wire.ShortcutRunParams{Contribution: "count", Values: map[string]string{"what": "苹果"}})
	if json.Unmarshal(f.Result, &res); res.Text != "苹果：3 个" {
		t.Fatalf("shortcut.run: %s %+v", f.Result, f.Error)
	}
	if f := c.call("shortcut.run", wire.ShortcutRunParams{Contribution: "nope"}); f.Error == nil || f.Error.Code != wire.ErrorCodeNoContribution {
		t.Fatalf("another shortcut: %+v", f.Error)
	}
	if f := c.call("shortcut.list", nil); f.Error == nil || f.Error.Code != wire.ErrorCodeUnknownMethod {
		t.Fatalf("another method: %+v", f.Error)
	}
}

// manifest answers the manifest without its tags: hubs from before them refuse the field.
func TestManifestWithoutTags(t *testing.T) {
	for _, source := range []json.RawMessage{
		json.RawMessage(`{"id": "example", "tags": ["示例"], "icon": "assets/icon.png", "version": "1.0.0"}`),
		json.RawMessage(`{"id": "example", "icon": "assets/icon.png", "version": "1.0.0"}`),
	} {
		c := start(t, Program{ID: "example", Version: "1.0.0", Manifest: source})
		f := c.call("manifest", nil)
		var got map[string]any
		if err := json.Unmarshal(f.Result, &got); err != nil || got["tags"] != nil || got["icon"] != nil || got["id"] != "example" || got["version"] != "1.0.0" {
			t.Fatalf("manifest: %s %v", f.Result, err)
		}
	}
}
