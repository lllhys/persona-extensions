package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The program on the hub: only the extension's page, which asks each switched-on computer's program for its state
// and its newest auto-allows (nodes.call → bypass.status) and lists them.

const logUI = "log"

// nodeCaller is how the page reaches a computer's program: the hub's nodes.call, or a fake in tests.
type nodeCaller func(ctx context.Context, machine string, out any) error

type hubApp struct {
	mu       sync.Mutex
	machines []string
	call     nodeCaller
}

func (a *hubApp) program(id, version string) ext.Program {
	return ext.Program{
		ID: id, Version: version, Manifest: manifest,
		Init: a.init,
		UI:   map[string]ext.Page{logUI: {Render: a.render}},
	}
}

func (a *hubApp) init(_ context.Context, in *ext.Init, hub *ext.Hub) (map[string]any, error) {
	var c config
	if err := in.Config(&c); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.machines = c.Machines
	if a.call == nil {
		a.call = func(ctx context.Context, machine string, out any) error {
			return hub.Call(ctx, "nodes.call", map[string]any{"machine": machine, "method": "bypass.status", "params": map[string]any{}}, out)
		}
	}
	return nil, nil
}

func (a *hubApp) render(ctx context.Context, r *ext.UIRequest) (*wire.View, error) {
	t := texts(r.Locale)
	loc := time.Local
	if l, err := time.LoadLocation(r.TZ); err == nil && r.TZ != "" {
		loc = l
	}
	a.mu.Lock()
	machines, call := append([]string(nil), a.machines...), a.call
	a.mu.Unlock()

	v := &wire.View{Title: t.title, RefreshAfter: 60}
	if len(machines) == 0 {
		v.Sections = []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Title: t.noMachines, Text: t.noMachinesHint}}}}
		return v, nil
	}
	states := make([]status, len(machines))
	errs := make([]error, len(machines))
	var wg sync.WaitGroup
	for i, m := range machines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			errs[i] = call(cctx, m, &states[i])
		}()
	}
	wg.Wait()
	for i, m := range machines {
		v.Sections = append(v.Sections, machineSection(t, loc, m, states[i], errs[i]))
	}
	v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{Type: wire.ComponentText, Style: wire.TextStyleCaption, Text: t.footer}}})
	return v, nil
}

func machineSection(t text, loc *time.Location, machine string, st status, err error) wire.Section {
	s := wire.Section{Title: machine}
	if err != nil {
		s.Items = append(s.Items, wire.Component{Type: wire.ComponentRow, Title: t.unreachable, Subtitle: oneLine(err.Error()),
			Badge: &wire.Badge{Text: t.offline, Tone: wire.ToneCaution}})
		return s
	}
	for _, d := range st.Dirs {
		row := wire.Component{Type: wire.ComponentRow, Title: d.Dir}
		switch {
		case d.Error != "":
			row.Subtitle, row.Badge = oneLine(d.Error), &wire.Badge{Text: t.notInstalled, Tone: wire.ToneCaution}
		case d.Installed:
			row.Subtitle, row.Badge = d.Settings, &wire.Badge{Text: t.installed, Tone: wire.TonePositive}
		default:
			row.Subtitle, row.Badge = d.Settings, &wire.Badge{Text: t.notInstalled, Tone: wire.ToneCaution}
		}
		s.Items = append(s.Items, row)
	}
	if len(st.Recent) == 0 {
		s.Items = append(s.Items, wire.Component{Type: wire.ComponentText, Style: wire.TextStyleSecondary, Text: t.none})
		return s
	}
	for _, e := range st.Recent {
		when := e.At
		if at, err := time.Parse(time.RFC3339, e.At); err == nil {
			when = at.In(loc).Format("01-02 15:04:05")
		}
		meta := []string{when}
		if e.Cwd != "" {
			meta = append(meta, filepath.Base(e.Cwd))
		}
		if len(e.Session) >= 8 {
			meta = append(meta, e.Session[:8])
		}
		if e.Agent != "" {
			meta = append(meta, t.subagent)
		}
		s.Items = append(s.Items, wire.Component{Type: wire.ComponentRow, Title: e.Command, Subtitle: strings.Join(meta, " · ")})
	}
	s.Footer = fmt.Sprintf(t.recent, len(st.Recent))
	return s
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	return s
}

// text is the page's words in one language.
type text struct {
	title, noMachines, noMachinesHint, unreachable, offline, installed, notInstalled, none, subagent, recent, footer string
}

func texts(locale string) text {
	switch {
	case strings.HasPrefix(locale, "en"):
		return text{
			title: "Auto-allowed", noMachines: "No computer switched on",
			noMachinesHint: "Turn on a computer in this extension's settings: its Claude Code then gets the hook.",
			unreachable:    "Can't reach this computer's program", offline: "Unreachable",
			installed: "Hook installed", notInstalled: "No hook", none: "Nothing auto-allowed yet.", subagent: "subagent",
			recent: "Newest %d, newest first", footer: "Only Bash calls in bypassPermissions sessions are allowed; your own ask and deny rules still ask. Commands are cut and secrets masked.",
		}
	case strings.HasPrefix(locale, "ja"):
		return text{
			title: "自動許可の記録", noMachines: "オンのコンピュータがありません",
			noMachinesHint: "この拡張の設定でコンピュータをオンにすると、その Claude Code に hook が入ります。",
			unreachable:    "このコンピュータのプログラムに接続できません", offline: "接続できません",
			installed: "hook あり", notInstalled: "hook なし", none: "まだ自動許可はありません。", subagent: "サブエージェント",
			recent: "新しい順に %d 件", footer: "許可するのは bypassPermissions のセッションの Bash だけです。自分で設定した ask / deny ルールは引き続き確認されます。コマンドは切り詰め、秘密情報は伏せています。",
		}
	}
	return text{
		title: "自动允许记录", noMachines: "还没有打开任何电脑",
		noMachinesHint: "在这个扩展的设置里打开一台电脑，那台电脑的 Claude Code 就会装上 hook。",
		unreachable:    "连不上这台电脑上的程序", offline: "连不上",
		installed: "已装 hook", notInstalled: "没有 hook", none: "还没有自动允许过。", subagent: "子 Agent",
		recent: "最近 %d 条，新的在前", footer: "只放行 bypassPermissions 会话里的 Bash；你自己配的 ask / deny 规则照常问你。命令已截断、去掉了密钥。",
	}
}
