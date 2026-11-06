package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Automations, scripts and scenes: listed (homeassistant_automations), an automation's config read
// (homeassistant_automation_config), run / switched on and off (homeassistant_run) and created, changed or deleted
// (homeassistant_automation_edit, through Home Assistant's config API /api/config/automation/config/<id>; the phone
// always confirms it, and the card shows the whole config and what changes).

var automationIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// toolAutomations lists the automations, scripts and scenes (kind: automation, script, scene or "" all of them).
func (a *app) toolAutomations(ctx context.Context, kind, query string) (string, error) {
	hm, err := a.home(ctx, statesFor)
	if err != nil {
		return "", err
	}
	kinds := []string{"automation", "script", "scene"}
	if kind = strings.ToLower(strings.TrimSpace(kind)); kind != "" && kind != "all" {
		if kind != "automation" && kind != "script" && kind != "scene" {
			return "", fmt.Errorf("kind 要是 automation、script 或 scene")
		}
		kinds = []string{kind}
	}
	q := strings.ToLower(strings.TrimSpace(query))
	var b strings.Builder
	for _, k := range kinds {
		var rows []string
		for _, s := range hm.States {
			if s.Domain() != k || (hm.Hidden[s.EntityID] && hm.AreaOf[s.EntityID] == nil) {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(s.Name()), q) && !strings.Contains(s.EntityID, q) {
				continue
			}
			rows = append(rows, automationLine(a, s))
		}
		fmt.Fprintf(&b, "## %s（%d 个）\n", domainName(k), len(rows))
		if len(rows) == 0 {
			b.WriteString("（没有）\n")
		}
		for _, r := range rows {
			b.WriteString(r + "\n")
		}
	}
	b.WriteString("\n触发 / 开关自动化、运行脚本、执行场景用 " + runAction + "；改自动化先用 " + id + "_automation_config 读出它的配置，再用 " + editAction + "。")
	return b.String(), nil
}

func automationLine(a *app, s State) string {
	line := fmt.Sprintf("- %s（%s）", s.Name(), s.EntityID)
	switch s.Domain() {
	case "automation":
		if s.State == "on" {
			line += "：开着"
		} else if s.State == "off" {
			line += "：停用了"
		} else {
			line += "：" + orDash(stateText(s))
		}
		if id := s.attr("id"); id != "" {
			line += "，配置 id " + id
		} else {
			line += "，没有配置 id（写在 YAML 里的，不能在这里改）"
		}
	case "script":
		if s.State == "on" {
			line += "：运行中"
		}
	}
	if t := s.attr("last_triggered"); t != "" {
		if at, err := time.Parse(time.RFC3339Nano, t); err == nil {
			line += "，上次 " + at.In(a.loc()).Format("01-02 15:04")
		}
	}
	return line
}

// automationConfig reads an automation's config by its config id.
func (h *ha) automationConfig(ctx context.Context, cid string) (map[string]any, error) {
	var cfg map[string]any
	err := h.do(ctx, http.MethodGet, "/api/config/automation/config/"+url.PathEscape(cid), nil, &cfg)
	if errors.Is(err, errNoEntity) {
		return nil, fmt.Errorf("Home Assistant 里没有配置 id 是 %s 的自动化（写在 YAML 里的自动化不能在这里读和改）", cid)
	}
	if err == nil {
		h.mu.Lock()
		if h.autoCfg == nil {
			h.autoCfg = map[string]string{}
		}
		h.autoCfg[cid] = canonical(cfg)
		h.mu.Unlock()
	}
	return cfg, err
}

// cachedAutomationConfig is the config last read of an automation ("" none), as canonical JSON.
func (h *ha) cachedAutomationConfig(cid string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.autoCfg[cid]
}

// canonical is v as indented JSON with sorted keys (encoding/json sorts map keys).
func canonical(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
	return strings.TrimRight(buf.String(), "\n")
}

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// configID finds an automation's config id from its entity id or the id itself.
func (a *app) configID(ctx context.Context, ref string, fetch bool) (string, string, error) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, "automation.") {
		if !automationIDRE.MatchString(ref) {
			return "", "", bad("要写自动化的实体 id（automation.xxx）或它的配置 id")
		}
		return ref, "", nil
	}
	var s State
	var ok bool
	if fetch {
		hm, err := a.home(ctx, statesFor)
		if err != nil {
			return "", "", err
		}
		s, ok = hm.ByID[ref]
	} else {
		h, _, _ := a.client()
		s, ok = h.cachedState(ref)
	}
	if !ok {
		return "", "", bad("没有自动化 " + ref + "（先用 " + id + "_automations 列一下）")
	}
	cid := s.attr("id")
	if cid == "" {
		return "", "", bad("「" + s.Name() + "」没有配置 id：它写在 YAML 里，只能在 Home Assistant 里改")
	}
	return cid, s.Name(), nil
}

func (a *app) toolAutomationConfig(ctx context.Context, ref string) (string, error) {
	cid, name, err := a.configID(ctx, ref, true)
	if err != nil {
		return "", err
	}
	h, _, _ := a.client()
	cfg, err := h.automationConfig(ctx, cid)
	if err != nil {
		return "", err
	}
	if name == "" {
		name, _ = cfg["alias"].(string)
	}
	return fmt.Sprintf("自动化「%s」（配置 id %s）的配置：\n%s\n\n要改它，把改好的完整配置交给 %s（op update，id %s）。", name, cid, canonical(cfg), editAction, cid), nil
}

// ---- homeassistant_run ----

type runWrite struct {
	Entity, Name, Op, Domain, Service string
	Data                              map[string]any
}

var runOps = map[string]map[string]string{
	"automation": {"trigger": "trigger", "enable": "turn_on", "disable": "turn_off"},
	"script":     {"run": "turn_on", "stop": "turn_off"},
	"scene":      {"run": "turn_on"},
}

var runVerbs = map[string]string{"automation.trigger": "手动触发自动化", "automation.enable": "启用自动化", "automation.disable": "停用自动化",
	"script.run": "运行脚本", "script.stop": "停下脚本", "scene.run": "执行场景"}

func (a *app) readRun(raw json.RawMessage) (write, error) {
	var args struct {
		EntityID      string         `json:"entity_id"`
		Op            string         `json:"op"`
		Variables     map[string]any `json:"variables"`
		SkipCondition *bool          `json:"skip_condition"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &args) != nil || !entityRE.MatchString(args.EntityID) {
		return nil, bad("参数要有 entity_id（automation.xxx、script.xxx 或 scene.xxx）和 op")
	}
	w := &runWrite{Entity: args.EntityID, Op: strings.TrimSpace(args.Op)}
	w.Domain, _, _ = strings.Cut(w.Entity, ".")
	ops, ok := runOps[w.Domain]
	if !ok {
		return nil, bad("只能是自动化（automation）、脚本（script）或场景（scene）；别的设备用 " + callAction)
	}
	if w.Op == "" && w.Domain != "automation" {
		w.Op = "run"
	}
	if w.Service, ok = ops[w.Op]; !ok {
		var names []string
		for k := range ops {
			names = append(names, k)
		}
		sort.Strings(names)
		return nil, bad(fmt.Sprintf("%s 的 op 只能是 %s", w.Domain, strings.Join(names, "、")))
	}
	if err := a.canWrite(w.Domain, false); err != nil {
		return nil, bad(err.Error())
	}
	switch {
	case len(args.Variables) > 0 && w.Domain != "script":
		return nil, bad("只有脚本能带 variables")
	case len(args.Variables) > 0:
		w.Data = map[string]any{"variables": args.Variables}
	case w.Op == "trigger":
		skip := true
		if args.SkipCondition != nil {
			skip = *args.SkipCondition
		}
		w.Data = map[string]any{"skip_condition": skip}
	}
	h, _, _ := a.client()
	w.Name = w.Entity
	if s, ok := h.cachedState(w.Entity); ok {
		w.Name = s.Name()
	}
	return w, nil
}

func (w *runWrite) visible() string { return "家里的自动化和脚本（Home Assistant）" }
func (w *runWrite) pin() string     { return "" }

func (w *runWrite) summary(*app) string {
	out := fmt.Sprintf("%s「%s」（%s，%s.%s）", runVerbs[w.Domain+"."+w.Op], w.Name, w.Entity, w.Domain, w.Service)
	if skip, ok := w.Data["skip_condition"].(bool); ok {
		if skip {
			out += "，不检查它的条件，直接执行它的动作"
		} else {
			out += "，先检查它的条件"
		}
	}
	if v, ok := w.Data["variables"]; ok {
		out += "，变量 " + clip(canonicalLine(v), 600)
	}
	return out
}

func canonicalLine(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func (w *runWrite) run(ctx context.Context, a *app) (string, error) {
	h, _, _ := a.client()
	data := map[string]any{"entity_id": []string{w.Entity}}
	for k, v := range w.Data {
		data[k] = v
	}
	if err := h.Call(ctx, w.Domain, w.Service, data); err != nil {
		return "", err
	}
	time.Sleep(settle)
	out := "已" + runVerbs[w.Domain+"."+w.Op] + "「" + w.Name + "」"
	if s, err := h.State(ctx, w.Entity); err == nil && w.Domain != "scene" {
		out += "，现在 " + orDash(automationState(s))
	}
	return out, nil
}

func automationState(s State) string {
	if s.Domain() == "automation" {
		return map[string]string{"on": "开着", "off": "停用了"}[s.State]
	}
	return stateText(s)
}

// ---- homeassistant_automation_edit ----

type editWrite struct {
	Op     string // create, update, delete
	ID     string // the config id
	Name   string
	Config map[string]any // the new one (create, update)
	Old    string         // the current one as canonical JSON, as last read ("" not read)
}

func (a *app) readEdit(raw json.RawMessage) (write, error) {
	var args struct {
		Op     string          `json:"op"`
		ID     string          `json:"id"`
		Config json.RawMessage `json:"config"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &args) != nil {
		return nil, bad("参数要有 op（create、update、delete）")
	}
	if err := a.canWrite("automation", false); err != nil {
		return nil, bad(err.Error())
	}
	w := &editWrite{Op: strings.TrimSpace(args.Op)}
	switch w.Op {
	case "create", "update":
		if err := json.Unmarshal(args.Config, &w.Config); err != nil || w.Config == nil {
			return nil, bad("config 要是自动化的完整配置（一个对象，有 alias、triggers、actions，可以有 conditions、mode、description）")
		}
		if err := checkAutomation(w.Config); err != nil {
			return nil, err
		}
		w.Name, _ = w.Config["alias"].(string)
	case "delete":
		if len(args.Config) > 0 && string(args.Config) != "null" {
			return nil, bad("删除不用写 config")
		}
	default:
		return nil, bad("op 要是 create、update 或 delete")
	}
	h, _, _ := a.client()
	if w.Op == "create" {
		if args.ID != "" {
			return nil, bad("新建不用写 id：它由配置算出来")
		}
		// the id is the config's: describe and do read the same args, so they must come to the same id
		w.ID = hash(canonical(w.Config))
		return w, nil
	}
	cid, name, err := a.configID(context.Background(), args.ID, false)
	if err != nil {
		return nil, err
	}
	w.ID = cid
	if w.Name == "" {
		w.Name = name
	}
	if w.Old = h.cachedAutomationConfig(cid); w.Old == "" {
		return nil, bad("先用 " + id + "_automation_config 读出这个自动化现在的配置（卡片要列出改了什么），再" + map[string]string{"update": "改", "delete": "删"}[w.Op])
	}
	if w.Name == "" {
		var old map[string]any
		_ = json.Unmarshal([]byte(w.Old), &old)
		w.Name, _ = old["alias"].(string)
	}
	return w, nil
}

// checkAutomation holds a config to what an automation has: triggers and actions (either spelling), and no keys of
// another shape.
func checkAutomation(c map[string]any) error {
	has := func(ks ...string) bool {
		for _, k := range ks {
			if v, ok := c[k]; ok && v != nil {
				return true
			}
		}
		return false
	}
	switch {
	case !has("triggers", "trigger"):
		return bad("自动化要有 triggers（什么时候触发）")
	case !has("actions", "action"):
		return bad("自动化要有 actions（做什么）")
	}
	if alias, _ := c["alias"].(string); strings.TrimSpace(alias) == "" {
		return bad("自动化要有 alias（名字）")
	}
	if m, ok := c["mode"].(string); ok && m != "single" && m != "restart" && m != "queued" && m != "parallel" {
		return bad("mode 只能是 single、restart、queued 或 parallel")
	}
	return nil
}

func (w *editWrite) visible() string { return "家里的自动化（Home Assistant）" }

func (w *editWrite) pin() string { return hash(w.Old) }

func (w *editWrite) summary(*app) string {
	var b strings.Builder
	switch w.Op {
	case "create":
		fmt.Fprintf(&b, "【新建自动化】「%s」（配置 id %s），完整配置：\n%s", w.Name, w.ID, canonical(w.Config))
	case "update":
		fmt.Fprintf(&b, "【修改自动化】「%s」（配置 id %s）\n改动（- 去掉，+ 加上）：\n", w.Name, w.ID)
		d := lineDiff(w.Old, canonical(w.withID()))
		if d == "" {
			d = "（和现在的一样，没有改动）"
		}
		b.WriteString(d)
		fmt.Fprintf(&b, "\n\n改完后的完整配置：\n%s", canonical(w.withID()))
	case "delete":
		fmt.Fprintf(&b, "【删除自动化】「%s」（配置 id %s），删掉后不能恢复。现在的配置：\n%s", w.Name, w.ID, w.Old)
	}
	return b.String()
}

// withID is the new config as Home Assistant keeps it: with its id.
func (w *editWrite) withID() map[string]any {
	c := map[string]any{}
	for k, v := range w.Config {
		c[k] = v
	}
	c["id"] = w.ID
	return c
}

func (w *editWrite) run(ctx context.Context, a *app) (string, error) {
	h, _, _ := a.client()
	path := "/api/config/automation/config/" + url.PathEscape(w.ID)
	if w.Op != "create" { // what the card showed must be what is there
		live, err := h.automationConfig(ctx, w.ID)
		if err != nil {
			return "", err
		}
		if canonical(live) != w.Old {
			return "", errors.New("这个自动化在确认之后被改过了，没有执行：请重新读它的配置再发起")
		}
	} else if _, err := h.send(ctx, http.MethodGet, path, nil); err == nil {
		return "", errors.New("已经有这个配置的自动化了（配置 id " + w.ID + "）")
	} else if !errors.Is(err, errNoEntity) {
		return "", err
	}
	var err error
	if w.Op == "delete" {
		_, err = h.send(ctx, http.MethodDelete, path, nil)
	} else {
		_, err = h.send(ctx, http.MethodPost, path, w.withID())
	}
	h.mu.Lock()
	delete(h.autoCfg, w.ID)
	h.statesAt = time.Time{}
	h.mu.Unlock()
	if err != nil {
		return "", err
	}
	return map[string]string{"create": "已新建", "update": "已修改", "delete": "已删除"}[w.Op] + "自动化「" + w.Name + "」（配置 id " + w.ID + "），Home Assistant 已重新载入自动化", nil
}

// lineDiff is b against a, line by line (a longest common subsequence): "- " lines only in a, "+ " only in b, a
// few lines of context around them.
func lineDiff(a, b string) string {
	x, y := strings.Split(a, "\n"), strings.Split(b, "\n")
	if len(x)*len(y) > 4_000_000 { // too long to compare: show both
		return "- " + strings.Join(x, "\n- ") + "\n+ " + strings.Join(y, "\n+ ")
	}
	n, m := len(x), len(y)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	type line struct {
		op   byte
		text string
	}
	var out []line
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && x[i] == y[j]:
			out = append(out, line{' ', x[i]})
			i, j = i+1, j+1
		case i < n && (j == m || lcs[i+1][j] >= lcs[i][j+1]):
			out = append(out, line{'-', x[i]})
			i++
		default:
			out = append(out, line{'+', y[j]})
			j++
		}
	}
	const context = 2
	keep := make([]bool, len(out))
	changed := false
	for k, l := range out {
		if l.op != ' ' {
			changed = true
			for c := max(0, k-context); c <= min(len(out)-1, k+context); c++ {
				keep[c] = true
			}
		}
	}
	if !changed {
		return ""
	}
	var sb strings.Builder
	gap := false
	for k, l := range out {
		if !keep[k] {
			gap = true
			continue
		}
		if gap && sb.Len() > 0 {
			sb.WriteString("  …\n")
		}
		gap = false
		sb.WriteByte(l.op)
		sb.WriteString(" " + l.text + "\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}
