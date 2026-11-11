package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

// page draws the plugin page st as an app of UI version v in locale, checking that the hub would draw it.
func (h *harness) page(st uiState, v int, locale string) *wire.View {
	h.t.Helper()
	var raw json.RawMessage
	if err := h.call("ui.render", wire.UIParams{Contribution: uiPlugins, State: ext.EncodeState(st), Placement: "extension_page",
		Size: "full", UIVersion: v, Locale: locale}, &raw); err != nil {
		h.t.Fatalf("ui.render %+v: %v", st, err)
	}
	return h.checkView(raw)
}

// tap sends an action on page st (with values), checking the view it answers.
func (h *harness) tap(st uiState, action string, values map[string]string) wire.ActionResult {
	h.t.Helper()
	var res struct {
		wire.ActionResult
		View json.RawMessage `json:"view"`
	}
	if err := h.call("ui.action", wire.UIParams{Contribution: uiPlugins, State: ext.EncodeState(st), Action: action, Values: values,
		Placement: "extension_page", Size: "full", UIVersion: 3}, &res); err != nil {
		h.t.Fatalf("ui.action %s: %v", action, err)
	}
	out := res.ActionResult
	if len(res.View) > 0 {
		out.View = h.checkView(res.View)
	}
	return out
}

func (h *harness) checkView(raw json.RawMessage) *wire.View {
	h.t.Helper()
	if err := exttest.CheckView(raw, false); err != nil {
		h.t.Fatalf("the hub would not draw this view: %v\n%s", err, raw)
	}
	var v wire.View
	if err := json.Unmarshal(raw, &v); err != nil {
		h.t.Fatal(err)
	}
	return &v
}

func stateOf(t *testing.T, v *wire.View) uiState {
	t.Helper()
	var st uiState
	if err := json.Unmarshal([]byte(v.State), &st); err != nil {
		t.Fatalf("state %q: %v", v.State, err)
	}
	return st
}

// items are a view's components with their section's title.
type item struct {
	section string
	wire.Component
}

func items(v *wire.View) []item {
	var out []item
	for _, s := range v.Sections {
		for _, c := range s.Items {
			out = append(out, item{s.Title, c})
		}
	}
	return out
}

func find(t *testing.T, v *wire.View, key string) item {
	t.Helper()
	for _, it := range items(v) {
		if it.Key == key {
			return it
		}
	}
	t.Fatalf("no %q in %s", key, dump(v))
	return item{}
}

func dump(v *wire.View) string {
	b, _ := json.MarshalIndent(v, "", " ")
	return string(b)
}

func TestThePluginPagesWaitForASignIn(t *testing.T) {
	f := newFakeChatGPT(t)
	h := start(t, f, "zh-Hans")
	v := h.page(uiState{}, 3, "")
	if e := find(t, v, "signed-out"); e.Type != wire.ComponentEmpty || e.Title != zhHans.ui.signedOut {
		t.Errorf("signed out: %+v", e)
	}
	if v.Search != nil || len(f.asked) != 0 {
		t.Errorf("a search box or a request while signed out: %v %v", v.Search, f.asked)
	}
	if v := h.page(uiState{}, 3, "en-US"); find(t, v, "signed-out").Title != english.ui.signedOut {
		t.Errorf("the page is not in the app's language: %s", dump(v))
	}
}

func TestTheMarketplacePage(t *testing.T) {
	f := newFakeChatGPT(t)
	h := start(t, f, "zh-Hans")
	h.signIn()
	v := h.page(uiState{}, 3, "")
	if v.Search == nil || v.Search.Action.ID != "search" || v.Search.Placeholder != zhHans.ui.search {
		t.Fatalf("search: %+v", v.Search)
	}
	notes := find(t, v, "i:plugin_example_notes")
	if notes.section != "已安装 · 1" || notes.Title != "Example Notes" || notes.Subtitle != "Example Co · Notes for the example tests" ||
		notes.Avatar == nil || notes.Avatar.Text != "EN" || notes.Badge != nil {
		t.Errorf("installed row: %+v", notes)
	}
	open := notes.Action
	if open == nil || open.Push == nil || open.Push.Title != "Example Notes" || open.Push.UI != "" {
		t.Fatalf("the row does not open the plugin: %+v", open)
	}
	var st uiState
	_ = json.Unmarshal([]byte(open.Push.State), &st)
	if st != (uiState{Page: pagePlugin, ID: "plugin_example_notes"}) {
		t.Errorf("pushed state: %+v", st)
	}
	apps := find(t, v, "apps")
	// the app installed with Example Notes and never connected is not counted as connected
	if apps.section != zhHans.ui.apps || apps.Title != "2 个应用" || apps.Subtitle != "5 个工具，2 个只读 · 1 个要在 ChatGPT 里连接" || apps.Action.Push == nil {
		t.Errorf("apps row: %+v", apps)
	}
	mail := find(t, v, "m:plugin_example_mail")
	if mail.section != "插件市场" || mail.Badge != nil {
		t.Errorf("marketplace row: %+v", mail)
	}
	if find(t, v, "m:plugin_example_notes").Badge == nil {
		t.Error("an installed plugin in the marketplace has no badge")
	}
	if s := v.Sections[len(v.Sections)-1]; s.More != nil {
		t.Errorf("load more with nothing more: %+v", s.More)
	}
}

func TestSearchingTheMarketplace(t *testing.T) {
	f := newFakeChatGPT(t)
	h := start(t, f, "zh-Hans")
	h.signIn()
	res := h.tap(uiState{}, "search", map[string]string{"q": " mail "})
	v := res.View
	if st := stateOf(t, v); st.Q != "mail" || v.Search.Value != "mail" {
		t.Errorf("the search is not kept: %+v %+v", st, v.Search)
	}
	got := items(v)
	if len(got) != 1 || got[0].Key != "r:plugin_example_mail" || got[0].section != "「mail」的结果" {
		t.Errorf("results: %s", dump(v))
	}
	v = h.tap(stateOf(t, v), "search", map[string]string{"q": "zzz"}).View
	if e := find(t, v, "no-results"); e.Type != wire.ComponentEmpty || e.Title != "没有找到「zzz」" {
		t.Errorf("no results: %+v", e)
	}
	// cleared: the marketplace again
	v = h.tap(stateOf(t, v), "search", map[string]string{"q": ""}).View
	find(t, v, "m:plugin_example_mail")
}

func TestLoadingMoreOfTheMarketplace(t *testing.T) {
	f := newFakeChatGPT(t)
	f.extra = morePlugins(68) // 70 in all: 50 a page
	h := start(t, f, "en")
	h.signIn()
	market := func(v *wire.View) wire.Section { return v.Sections[len(v.Sections)-1] }
	v := h.page(uiState{}, 3, "")
	if s := market(v); len(s.Items) != 20 || s.More == nil || s.More.Action.ID != "more" || s.More.Title != "Load more" {
		t.Fatalf("first load: %d rows, more %+v", len(s.Items), s.More)
	}
	for _, want := range []int{40, 60} {
		v = h.tap(stateOf(t, v), "more", nil).View
		if s := market(v); len(s.Items) != want || s.More == nil {
			t.Fatalf("after load more: %d rows (want %d), more %+v", len(s.Items), want, s.More)
		}
	}
	v = h.tap(stateOf(t, v), "more", nil).View
	if s := market(v); len(s.Items) != 70 || s.More != nil {
		t.Fatalf("the end: %d rows, more %+v", len(s.Items), s.More)
	}
	lists := 0
	for _, a := range f.asked {
		if a == "GET /ps/plugins/list" {
			lists++
		}
	}
	if lists != 2 {
		t.Errorf("the marketplace was read %d times for 70 plugins in pages of 50 (%v)", lists, f.asked)
	}
}

func TestAPluginsPage(t *testing.T) {
	f := newFakeChatGPT(t)
	h := start(t, f, "zh-Hans")
	h.signIn()
	v := h.page(uiState{Page: pagePlugin, ID: "plugin_example_notes"}, 3, "")
	if v.Title != "Example Notes" {
		t.Errorf("title %q", v.Title)
	}
	head := find(t, v, "head")
	if head.Subtitle != "Example Co" || head.Badge == nil || head.Badge.Text != "已安装" || head.Badge.Tone != wire.TonePositive {
		t.Errorf("head: %+v", head)
	}
	if about := find(t, v, "about"); about.Type != wire.ComponentText || about.Text != "A plugin for tests." {
		t.Errorf("description: %+v", about)
	}
	if v.Sections[0].Footer != "版本 1.2.0" {
		t.Errorf("version: %q", v.Sections[0].Footer)
	}
	app := find(t, v, "app:connector_notes")
	if app.section != "带的应用 · 1" || app.Title != "Example Notes" || app.Badge == nil || app.Badge.Text != "要连接" {
		t.Errorf("an installed plugin's app that is not connected: %+v", app)
	}
	if link := find(t, v, "connect"); link.Type != wire.ComponentLink || link.URL != "https://chatgpt.com/plugins/plugin_example_notes" {
		t.Errorf("connect link: %+v", link)
	}
	if skill := find(t, v, "skill:0"); skill.section != "技能 · 1" || skill.Title != "notes" || skill.Subtitle != "Work with notes" {
		t.Errorf("skill: %+v", skill)
	}
	un := find(t, v, "uninstall")
	if un.Type != wire.ComponentButton || un.Role != wire.ButtonRoleDestructive || un.Action == nil ||
		un.Action.Confirm != "从你的 ChatGPT 账号上卸载「Example Notes」？助手就不能再用它的应用和技能了。" {
		t.Errorf("uninstall button: %+v", un)
	}

	v = h.page(uiState{Page: pagePlugin, ID: "plugin_example_mail"}, 3, "")
	if app := find(t, v, "app:connector_mail"); app.Badge == nil || app.Badge.Text != "已连接" || app.Subtitle != "3 个工具" {
		t.Errorf("a connected app: %+v", app)
	}
	for _, it := range items(v) {
		if it.Key == "connect" || it.Key == "uninstall" {
			t.Errorf("a plugin not installed has %s", it.Key)
		}
	}
	in := find(t, v, "install")
	if in.Role != "" || in.Action.Confirm != "在你的 ChatGPT 账号上安装「Example Mail」？它带 1 个应用、0 个技能，装好后助手就能用。" {
		t.Errorf("install button: %+v", in)
	}
}

func TestInstallingAndUninstallingFromThePage(t *testing.T) {
	f := newFakeChatGPT(t)
	h := start(t, f, "zh-Hans")
	h.signIn()
	st := uiState{Page: pagePlugin, ID: "plugin_example_mail"}
	h.page(st, 3, "")
	res := h.tap(st, "install", nil)
	if res.Toast != "已安装「Example Mail」，还有 1 个应用要在 ChatGPT 里连接" {
		t.Errorf("toast: %q", res.Toast)
	}
	f.mu.Lock()
	installed := f.installed["plugin_example_mail"]
	f.mu.Unlock()
	if !installed {
		t.Fatal("not installed")
	}
	find(t, res.View, "uninstall")
	// the marketplace knows at once
	if find(t, h.page(uiState{}, 3, ""), "m:plugin_example_mail").Badge == nil {
		t.Error("the marketplace still says not installed")
	}

	res = h.tap(st, "uninstall", nil)
	if res.Toast != "已卸载「Example Mail」" {
		t.Errorf("toast: %q", res.Toast)
	}
	find(t, res.View, "install")
	f.mu.Lock()
	installed = f.installed["plugin_example_mail"]
	f.mu.Unlock()
	if installed {
		t.Error("still installed")
	}

	// the installed list's swipe names its plugin in values
	res = h.tap(uiState{Page: pageInstalled}, "uninstall", map[string]string{"id": "plugin_example_notes"})
	if res.Toast != "已卸载「Example Notes」" || find(t, res.View, "none").Title != "还没有装插件" {
		t.Errorf("swipe: %q %s", res.Toast, dump(res.View))
	}
	// an action without a plugin does nothing
	if res := h.tap(uiState{}, "install", nil); !strings.HasPrefix(res.Toast, "没有完成") {
		t.Errorf("install with no plugin: %q", res.Toast)
	}
	h.noSecretsInLogs()
}

func TestTheInstalledPage(t *testing.T) {
	f := newFakeChatGPT(t)
	h := start(t, f, "zh-Hans")
	h.signIn()
	v := h.page(uiState{Page: pageInstalled}, 3, "")
	row := find(t, v, "i:plugin_example_notes")
	if row.Action == nil || row.Action.Push == nil || len(row.Actions) != 0 {
		t.Errorf("row at UI version 3: %+v", row)
	}
	row = find(t, h.page(uiState{Page: pageInstalled}, 6, ""), "i:plugin_example_notes")
	if len(row.Actions) != 1 || !row.Actions[0].Swipe || row.Actions[0].Role != wire.ButtonRoleDestructive ||
		row.Actions[0].Action.Confirm == "" || row.Actions[0].Action.Values["id"] != "plugin_example_notes" {
		t.Errorf("swipe to uninstall at UI version 6: %+v", row.Actions)
	}
}

func TestTheConnectedAppsPages(t *testing.T) {
	f := newFakeChatGPT(t)
	h := start(t, f, "zh-Hans")
	h.signIn()
	v := h.page(uiState{Page: pageApps}, 3, "")
	var keys []string
	for _, it := range items(v) {
		keys = append(keys, it.Key)
	}
	// connected first, by name; the one installed with a plugin and never connected last
	if !slices.Equal(keys, []string{"app:connector_broker", "app:connector_mail", "app:connector_notes"}) {
		t.Fatalf("apps: %v", keys)
	}
	mail := find(t, v, "app:connector_mail")
	if mail.Value != "3 个工具" || mail.Subtitle != "1 个只读，2 个会改动" || mail.Badge != nil {
		t.Errorf("mail: %+v", mail)
	}
	if notes := find(t, v, "app:connector_notes"); notes.Badge == nil || notes.Badge.Text != "要连接" || notes.Value != "" ||
		notes.Subtitle != zhHans.ui.needsDetail {
		t.Errorf("notes: %+v", notes)
	}
	v = h.page(uiState{Page: pageApp, ID: "connector_mail"}, 3, "")
	var tools []string
	for _, it := range items(v) {
		tools = append(tools, it.Title+" "+it.Badge.Text)
	}
	if !slices.Equal(tools, []string{"search_messages 只读", "forward_message 会改动", "send_message 会改动"}) {
		t.Errorf("tools: %v", tools)
	}
	v = h.page(uiState{Page: pageApp, ID: "connector_notes"}, 3, "")
	if find(t, v, "not-connected").Text != zhHans.ui.notConnectedApp {
		t.Errorf("not connected: %s", dump(v))
	}
}

func TestAProblemReadingChatGPTIsAPageNotAnError(t *testing.T) {
	f := newFakeChatGPT(t)
	h := start(t, f, "zh-Hans")
	h.signIn()
	f.mu.Lock()
	f.failList = true
	f.mu.Unlock()
	v := h.page(uiState{}, 3, "")
	e := find(t, v, "problem")
	if e.Type != wire.ComponentEmpty || e.Title != "读不到 ChatGPT" || !strings.Contains(e.Detail, "HTTP 500") || strings.Contains(e.Detail, "internal error") ||
		e.Button == nil || e.Button.Action.ID != "retry" {
		t.Errorf("problem: %+v", e)
	}
	f.mu.Lock()
	f.failList = false
	f.mu.Unlock()
	find(t, h.tap(stateOf(t, v), "retry", nil).View, "m:plugin_example_mail")
}

func TestThePackageHostIsLoggedOnce(t *testing.T) {
	f := newFakeChatGPT(t)
	h := start(t, f, "zh-Hans")
	h.signIn()
	h.page(uiState{Page: pagePlugin, ID: "plugin_example_notes"}, 3, "")
	h.page(uiState{Page: pagePlugin, ID: "plugin_example_notes"}, 3, "")
	logs := h.logs.String()
	if n := strings.Count(logs, "plugin packages come from 127.0.0.1, which is not in permissions.network"); n != 1 {
		t.Errorf("package host logged %d times:\n%s", n, logs)
	}
	if strings.Contains(logs, "example-notes.tgz") {
		t.Error("the package URL is in the log")
	}
	if !networkAllows("chatgpt.com") || networkAllows("files.chatgpt.com") || networkAllows("example.com") ||
		!networkAllows("sdmntprsouthcentralus.oaiusercontent.com") || networkAllows("oaiusercontent.com") {
		t.Error("networkAllows does not follow the hub's rule")
	}
}

func TestInitializeSaysItHasTheConnector(t *testing.T) {
	f := newFakeChatGPT(t)
	h := start(t, f, "zh-Hans")
	var said []wire.ConnectorItem
	if err := json.Unmarshal(h.host.Init.Contributes["connectors"], &said); err != nil || len(said) != 1 || said[0].ID != connectorID {
		t.Errorf("contributes: %s (%v)", h.host.Init.Contributes["connectors"], err)
	}
}

func TestInitials(t *testing.T) {
	for in, want := range map[string]string{"Example Notes": "EN", "GitHub": "G", "飞书": "飞", "google-drive": "GD", "": "?", "3D Viewer": "3V"} {
		if got := initials(in); got != want {
			t.Errorf("initials(%q) = %q, want %q", in, got, want)
		}
	}
}
