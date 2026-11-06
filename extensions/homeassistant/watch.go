package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
)

// Watches: rules on Home Assistant's state changes (a door opens, a leak, a device offline, humidity above a line)
// that, when they come true, notify the phone or wake the assistant, as they happen rather than by polling. The
// extension holds a WebSocket to Home Assistant subscribed to state_changed (subscribe_events) and checks each change
// against the rules itself, so a rule can be about an area, a type or a pattern and "for 10 minutes" survives a
// reconnect of neither side's making. Rules come from the settings (watches, one per line) and from the assistant
// (homeassistant_watch, confirmed on the phone; kept in the data directory).

// Rule is one watch.
type Rule struct {
	ID          string   `json:"id"`
	Name        string   `json:"name,omitempty"`
	Entity      string   `json:"entity_id,omitempty"` // an entity id, or a pattern with * (binary_sensor.*_door)
	Area        string   `json:"area,omitempty"`      // the area's name or id
	Domain      string   `json:"domain,omitempty"`
	DeviceClass string   `json:"device_class,omitempty"`
	To          string   `json:"to,omitempty"` // the state it changes to; "unavailable" is offline
	From        string   `json:"from,omitempty"`
	Above       *float64 `json:"above,omitempty"`
	Below       *float64 `json:"below,omitempty"`
	Attribute   string   `json:"attribute,omitempty"` // above / below read this attribute instead of the state
	For         int      `json:"for_seconds,omitempty"`
	Do          string   `json:"do,omitempty"`      // notify (default), assistant, both
	Message     string   `json:"message,omitempty"` // a line for the notification, or what the assistant is to do
	Cooldown    int      `json:"cooldown_minutes,omitempty"`
	Level       string   `json:"level,omitempty"` // active (default) or time-sensitive
	Source      string   `json:"-"`               // settings, assistant
}

const (
	watchMost       = 50
	defaultCooldown = 30  // minutes
	offlineFor      = 600 // seconds an entity is offline before an offline rule counts it: a restart is not an outage
	deliveriesMost  = 30  // per hour, all rules together
)

var (
	batchFor = 3 * time.Second // firings close together go out as one
	holdUnit = time.Second     // a rule's for_seconds (shorter in tests)
)

func (r Rule) cooldown() time.Duration {
	if r.Cooldown <= 0 {
		return defaultCooldown * time.Minute
	}
	return time.Duration(r.Cooldown) * time.Minute
}

// holdSeconds is how long the condition must hold: for_seconds, or offlineFor for an offline rule without it.
func (r Rule) holdSeconds() int {
	if r.For == 0 && r.To == "unavailable" {
		return offlineFor
	}
	return r.For
}

func (r Rule) holdFor() time.Duration { return time.Duration(r.holdSeconds()) * holdUnit }

func (r Rule) do() string {
	if r.Do == "" {
		return "notify"
	}
	return r.Do
}

// check holds a rule to what it must have: a target, a condition, known values.
func (r *Rule) check() error {
	r.Entity, r.Area, r.Domain = strings.TrimSpace(r.Entity), strings.TrimSpace(r.Area), strings.ToLower(strings.TrimSpace(r.Domain))
	if r.To == "offline" {
		r.To = "unavailable"
	}
	switch {
	case r.Entity == "" && r.Area == "" && r.Domain == "" && r.DeviceClass == "":
		return errors.New("要写看哪些实体：entity_id（可以带 *）、area、domain 或 device_class")
	case r.Entity != "" && !strings.Contains(r.Entity, "."):
		return fmt.Errorf("「%s」不是实体 id", clip(r.Entity, 60))
	case r.Entity != "" && !strings.Contains(r.Entity, "*") && !entityRE.MatchString(r.Entity):
		return fmt.Errorf("「%s」不是实体 id", clip(r.Entity, 60))
	case r.Domain != "" && r.Domain != "*" && !domainRE.MatchString(r.Domain):
		return fmt.Errorf("「%s」不是类型（domain）", clip(r.Domain, 30))
	case r.To == "" && r.From == "" && r.Above == nil && r.Below == nil:
		return errors.New("要写条件：to（变成什么状态，offline 是离线）、from、above 或 below")
	case r.Above != nil && r.Below != nil && *r.Above >= *r.Below:
		return errors.New("above 要小于 below")
	case r.For < 0 || r.For > 7*24*3600:
		return errors.New("for_seconds 要在 0 到 7 天之间")
	case r.Cooldown < 0 || r.Cooldown > 7*24*60:
		return errors.New("cooldown_minutes 要在 0 到 7 天之间")
	case r.Do != "" && r.Do != "notify" && r.Do != "assistant" && r.Do != "both":
		return errors.New("do 要是 notify、assistant 或 both")
	case r.Level != "" && r.Level != "active" && r.Level != "time-sensitive":
		return errors.New("level 要是 active 或 time-sensitive")
	case len([]rune(r.Message)) > 500 || len([]rune(r.Name)) > 60:
		return errors.New("message 最多 500 字，name 最多 60 字")
	}
	if _, err := path.Match(r.Entity, "x.y"); err != nil {
		return fmt.Errorf("entity_id 的通配写错了：%v", err)
	}
	return nil
}

// ruleID is a rule's id from its content: the same rule is the same id.
func ruleID(r Rule) string {
	r.ID, r.Source = "", ""
	b, _ := json.Marshal(r)
	sum := sha256.Sum256(b)
	return "w" + hex.EncodeToString(sum[:4])
}

// describe writes a rule in words.
func (r Rule) describe() string {
	var who []string
	if r.Entity != "" {
		who = append(who, r.Entity)
	}
	if r.Area != "" {
		who = append(who, "「"+r.Area+"」里")
	}
	if r.Domain != "" && r.Domain != "*" {
		who = append(who, "的"+domainName(r.Domain)+"（"+r.Domain+"）")
	}
	if r.DeviceClass != "" {
		who = append(who, "（"+r.DeviceClass+" 类）")
	}
	target := strings.Join(who, "")
	if r.Entity == "" && r.Domain == "" {
		target += "的实体"
	}
	var cond []string
	if r.From != "" {
		cond = append(cond, "从 "+r.From)
	}
	switch {
	case r.To == "unavailable":
		cond = append(cond, "离线")
	case r.To != "":
		cond = append(cond, "变成 "+r.To)
	}
	what := "状态"
	if r.Attribute != "" {
		what = "属性 " + r.Attribute
	}
	if r.Above != nil {
		cond = append(cond, what+"高于 "+tidy(*r.Above))
	}
	if r.Below != nil {
		cond = append(cond, what+"低于 "+tidy(*r.Below))
	}
	out := "当" + target + strings.Join(cond, "、")
	if d := time.Duration(r.holdSeconds()) * time.Second; d > 0 {
		out += "，持续 " + d.String()
	}
	switch r.do() {
	case "notify":
		out += " → 推送到手机"
	case "assistant":
		out += " → 叫助手处理"
	case "both":
		out += " → 推送到手机，并叫助手处理"
	}
	if r.Level == "time-sensitive" {
		out += "（时间敏感，穿透专注模式）"
	}
	if r.Message != "" {
		out += "，附言：" + r.Message
	}
	out += fmt.Sprintf("；同一个实体 %s 内只提醒一次", r.cooldown())
	if r.Name != "" {
		out = "「" + r.Name + "」" + out
	}
	return out
}

// parseRule reads a rule of the settings, a line like
//
//	binary_sensor.front_door =on for=1m
//	sensor.bathroom_humidity >75 for=10m do=assistant message=开一下排风扇
//	binary_sensor@厨房 class=moisture =on level=time-sensitive
//	switch.* offline
func parseRule(line string) (Rule, error) {
	var r Rule
	f := strings.Fields(line)
	if len(f) < 2 {
		return r, errors.New("一条要写成「看谁 条件 …」，如 binary_sensor.front_door =on for=1m")
	}
	if d, ar, ok := strings.Cut(f[0], "@"); ok {
		r.Domain, r.Area = d, ar
	} else {
		r.Entity = f[0]
	}
	for i := 1; i < len(f); i++ {
		t := f[i]
		k, v, kv := strings.Cut(t, "=")
		switch {
		case t == "offline":
			r.To = "unavailable"
		case strings.HasPrefix(t, ">") || strings.HasPrefix(t, "<"):
			n, err := strconv.ParseFloat(t[1:], 64)
			if err != nil {
				return r, fmt.Errorf("「%s」不是数", t[1:])
			}
			if t[0] == '>' {
				r.Above = &n
			} else {
				r.Below = &n
			}
		case kv && k == "":
			r.To = v
		case kv && k == "message":
			r.Message = strings.Join(append([]string{v}, f[i+1:]...), " ")
			i = len(f)
		case kv:
			switch k {
			case "for", "cooldown":
				d, err := parseDur(v)
				if err != nil {
					return r, err
				}
				if k == "for" {
					r.For = int(d.Seconds())
				} else {
					r.Cooldown = int(d.Minutes())
				}
			case "do":
				r.Do = v
			case "level":
				r.Level = v
			case "name":
				r.Name = v
			case "class":
				r.DeviceClass = v
			case "attr":
				r.Attribute = v
			case "from":
				r.From = v
			case "to":
				r.To = v
			default:
				return r, fmt.Errorf("不认识「%s」", k)
			}
		default:
			return r, fmt.Errorf("不认识「%s」", t)
		}
	}
	if r.Domain == "*" {
		r.Domain = ""
	}
	if err := r.check(); err != nil {
		return r, err
	}
	r.ID, r.Source = ruleID(r), "settings"
	return r, nil
}

func parseDur(s string) (time.Duration, error) {
	if n, err := strconv.Atoi(s); err == nil {
		return time.Duration(n) * time.Second, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("「%s」不是时长：写成 30s、10m、1h", s)
	}
	return d, nil
}

// ---- the watcher ----

// firing is one rule that came true for one entity.
type firing struct {
	Rule   Rule
	Entity string
	Name   string
	State  string // the state as people read it
	At     time.Time
}

// fired is a delivery, for homeassistant_watches.
type fired struct {
	At       time.Time
	Rule     string
	Entities []string
	Did      string // what went out, or why not
}

type watcher struct {
	a *app

	mu        sync.Mutex
	settings  []Rule // from the settings
	mine      []Rule // the assistant's
	errs      []string
	file      string                 // where the assistant's are kept
	timers    map[string]*time.Timer // rule|entity → its "for"
	last      map[string]State       // the newest state of each entity seen
	sent      map[string]time.Time   // rule|entity → when it last went out
	queue     []firing
	flushing  bool
	log       []fired
	delivered []time.Time
	connected bool
	connErr   string
	wake      chan struct{} // the rules changed
	hm        *home         // the areas, rebuilt at most every homeFor (a busy home changes many times a second)
	hmAt      time.Time
}

const homeFor = 30 * time.Second

// home is the areas and hidden entities the rules are checked against (nil: nothing read yet).
func (w *watcher) home() *home {
	w.mu.Lock()
	hm, at := w.hm, w.hmAt
	w.mu.Unlock()
	if hm != nil && time.Since(at) < homeFor {
		return hm
	}
	hm = w.a.cachedHome()
	w.mu.Lock()
	w.hm, w.hmAt = hm, time.Now()
	w.mu.Unlock()
	return hm
}

func newWatcher(a *app) *watcher {
	return &watcher{a: a, timers: map[string]*time.Timer{}, last: map[string]State{}, sent: map[string]time.Time{}, wake: make(chan struct{}, 1)}
}

// setSettings takes the settings' rules.
func (w *watcher) setSettings(lines []string) {
	var rules []Rule
	var errs []string
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		r, err := parseRule(l)
		if err != nil {
			errs = append(errs, fmt.Sprintf("设置里第 %d 条（%s）：%v", i+1, clip(l, 60), err))
			continue
		}
		rules = append(rules, r)
	}
	w.mu.Lock()
	w.settings, w.errs = rules, errs
	w.mu.Unlock()
	w.poke()
}

// load reads the assistant's rules from dir.
func (w *watcher) load(dir string) {
	if dir == "" {
		return
	}
	file := filepath.Join(dir, "watches.json")
	var rules []Rule
	if b, err := os.ReadFile(file); err == nil {
		if err := json.Unmarshal(b, &rules); err != nil {
			w.a.logf("watches.json: %v", err)
		}
	}
	for i := range rules {
		rules[i].Source = "assistant"
	}
	w.mu.Lock()
	w.file, w.mine = file, rules
	w.mu.Unlock()
	w.poke()
}

func (w *watcher) save() error {
	w.mu.Lock()
	file, rules := w.file, slices.Clone(w.mine)
	w.mu.Unlock()
	if file == "" {
		return errors.New("扩展没有数据目录，存不下规则")
	}
	b, _ := json.MarshalIndent(rules, "", "  ")
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

func (w *watcher) rules() []Rule {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append(slices.Clone(w.settings), w.mine...)
}

func (w *watcher) poke() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// run keeps the subscription while there are rules, until ctx ends.
func (w *watcher) run(ctx context.Context) {
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		h, _, _ := w.a.client()
		if len(w.rules()) == 0 || !h.configured() {
			select {
			case <-ctx.Done():
				return
			case <-w.wake:
			}
			continue
		}
		start := time.Now()
		err := w.session(ctx, h)
		w.mu.Lock()
		w.connected = false
		if err != nil {
			w.connErr = err.Error()
		}
		w.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > time.Minute {
			backoff = 5 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		case <-w.wake:
		}
		backoff = min(backoff*2, 5*time.Minute)
	}
}

// session is one connection: subscribed to state_changed until it drops, the rules change or the client is
// replaced.
func (w *watcher) session(ctx context.Context, h *ha) error {
	c, err := h.dialWS(ctx)
	if err != nil {
		return err
	}
	defer c.close()
	if _, err := w.a.home(ctx, statesFor); err != nil { // the areas, for rules about one
		w.a.logf("watch: %v", err)
	}
	err = c.send(ctx, "subscribe_events", map[string]any{"event_type": "state_changed"}, nil, func(ev json.RawMessage) {
		var e struct {
			Data struct {
				EntityID string `json:"entity_id"`
				Old      *State `json:"old_state"`
				New      *State `json:"new_state"`
			} `json:"data"`
		}
		if json.Unmarshal(ev, &e) == nil && e.Data.New != nil {
			w.change(e.Data.EntityID, e.Data.Old, *e.Data.New)
		}
	})
	if err != nil {
		return err
	}
	w.mu.Lock()
	w.connected, w.connErr = true, ""
	w.mu.Unlock()
	areas := time.NewTicker(5 * time.Minute)
	defer areas.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.done:
			return errors.New("Home Assistant 的 WebSocket 断了")
		case <-w.wake:
			if len(w.rules()) == 0 {
				return nil
			}
		case <-areas.C:
			_, _ = w.a.home(ctx, statesFor)
			if cur, _, _ := w.a.client(); cur != h {
				return nil // a new configuration
			}
		}
	}
}

// targets says whether r is about entity s.
func (w *watcher) targets(r Rule, s State, hm *home) bool {
	switch {
	case r.Entity != "" && r.Entity != s.EntityID:
		if !strings.Contains(r.Entity, "*") {
			return false
		}
		if ok, _ := path.Match(r.Entity, s.EntityID); !ok {
			return false
		}
	}
	if r.Domain != "" && s.Domain() != r.Domain {
		return false
	}
	if r.DeviceClass != "" && s.attr("device_class") != r.DeviceClass {
		return false
	}
	if hm != nil && hm.Hidden[s.EntityID] && hm.AreaOf[s.EntityID] == nil {
		return false
	}
	if r.Area != "" {
		if hm == nil {
			return false
		}
		ar := hm.area(r.Area)
		if ar == nil || hm.AreaOf[s.EntityID] != ar {
			return false
		}
	}
	return true
}

// holds says whether s meets r's condition (prev: the state before, for from).
func holds(r Rule, s *State, prev *State) bool {
	if s == nil {
		return false
	}
	if r.To != "" && s.State != r.To {
		return false
	}
	if r.From != "" && (prev == nil || prev.State != r.From) {
		return false
	}
	if r.Above != nil || r.Below != nil {
		var raw string
		if r.Attribute != "" {
			raw = s.attr(r.Attribute)
		} else {
			raw = s.State
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return false
		}
		if r.Above != nil && v <= *r.Above {
			return false
		}
		if r.Below != nil && v >= *r.Below {
			return false
		}
	}
	return true
}

// change is one state_changed: each rule about the entity that comes true now (it did not before) fires, or after
// its "for" when it still holds then.
func (w *watcher) change(entity string, old *State, s State) {
	hm := w.home()
	rules := w.rules()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.last[entity] = s
	for _, r := range rules {
		if !w.targets(r, s, hm) {
			continue
		}
		key := r.ID + "|" + entity
		now := holds(r, &s, old)
		// from is about the change itself; the others about the state before and after
		was := r.From == "" && holds(r, old, nil)
		switch {
		case !now:
			if t, ok := w.timers[key]; ok {
				t.Stop()
				delete(w.timers, key)
			}
		case was:
			// still true: nothing new
		case r.holdFor() == 0:
			w.fire(r, s)
		default:
			if _, ok := w.timers[key]; ok {
				continue
			}
			r := r
			w.timers[key] = time.AfterFunc(r.holdFor(), func() {
				w.mu.Lock()
				defer w.mu.Unlock()
				delete(w.timers, key)
				cur, ok := w.last[entity]
				still := r
				still.From = "" // from was about the change; what must still hold is the rest
				if ok && holds(still, &cur, nil) {
					w.fire(r, cur)
				}
			})
		}
	}
}

// fire queues a firing (w.mu held), unless the rule told about the entity within its cooldown.
func (w *watcher) fire(r Rule, s State) {
	key := r.ID + "|" + s.EntityID
	if t, ok := w.sent[key]; ok && time.Since(t) < r.cooldown() {
		return
	}
	w.sent[key] = time.Now()
	w.queue = append(w.queue, firing{Rule: r, Entity: s.EntityID, Name: s.Name(), State: orDash(stateText(s)), At: time.Now()})
	if !w.flushing {
		w.flushing = true
		time.AfterFunc(batchFor, w.flush)
	}
}

// flush delivers what fired, one delivery per rule (a restart of Home Assistant takes many devices offline at once).
func (w *watcher) flush() {
	w.mu.Lock()
	queue := w.queue
	w.queue, w.flushing = nil, false
	w.mu.Unlock()
	byRule := map[string][]firing{}
	var order []string
	for _, f := range queue {
		if _, ok := byRule[f.Rule.ID]; !ok {
			order = append(order, f.Rule.ID)
		}
		byRule[f.Rule.ID] = append(byRule[f.Rule.ID], f)
	}
	for _, rid := range order {
		w.deliver(byRule[rid])
	}
}

func (w *watcher) deliver(fs []firing) {
	r := fs[0].Rule
	var ents, lines []string
	for _, f := range fs {
		ents = append(ents, f.Entity)
		lines = append(lines, fmt.Sprintf("%s（%s）：%s", f.Name, f.Entity, f.State))
	}
	rec := fired{At: fs[0].At, Rule: r.ID, Entities: ents}
	w.mu.Lock()
	var recent []time.Time
	for _, t := range w.delivered {
		if time.Since(t) < time.Hour {
			recent = append(recent, t)
		}
	}
	w.delivered = recent
	over := len(recent) >= deliveriesMost
	if !over {
		w.delivered = append(w.delivered, time.Now())
	}
	w.mu.Unlock()
	if over {
		rec.Did = fmt.Sprintf("没有发：一小时里已经提醒了 %d 次", deliveriesMost)
		w.record(rec)
		return
	}
	hub := w.a.hubAPI()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	title := "家里：" + orDash(r.Name)
	if r.Name == "" {
		title = "家里：" + fs[0].Name
		if len(fs) > 1 {
			title = fmt.Sprintf("家里：%d 个设备", len(fs))
		}
	}
	at := fs[0].At.In(w.a.loc()).Format("15:04")
	body := at + " " + strings.Join(lines, "；")
	if r.Message != "" && r.do() == "notify" {
		body += "\n" + r.Message
	}
	var did []string
	if r.do() == "notify" || r.do() == "both" {
		n := notice{Title: clip(title, 120), Body: clip(body, 1000), Level: r.Level, ID: clip("ha-"+r.ID, 64), UI: pageUI}
		if len(fs) == 1 {
			n.State = ext.EncodeState(pageState{Entity: fs[0].Entity})
		}
		if err := hub.notify(ctx, n); err != nil {
			did = append(did, "推送失败："+err.Error())
		} else {
			did = append(did, "推送了")
		}
	}
	if r.do() == "assistant" || r.do() == "both" {
		prompt := fmt.Sprintf("Home Assistant 的提醒规则触发了（%s，规则 %s：%s）。\n%s\n", fs[0].At.In(w.a.loc()).Format("2006-01-02 15:04"), r.ID, r.describe(), strings.Join(lines, "\n"))
		if r.Message != "" {
			prompt += "用户给这条规则的要求：" + r.Message + "\n"
		}
		prompt += "用 homeassistant_* 工具看一下现在的情况；要操作设备时照常经手机确认；需要用户知道的，简短告诉用户。"
		if sid, err := hub.ask(ctx, prompt); err != nil {
			did = append(did, "叫助手失败："+err.Error())
		} else {
			did = append(did, "叫了助手（对话 "+orDash(sid)+"）")
		}
	}
	rec.Did = strings.Join(did, "，")
	w.record(rec)
}

func (w *watcher) record(f fired) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.log = append(w.log, f)
	if len(w.log) > 50 {
		w.log = w.log[len(w.log)-50:]
	}
	if f.Did != "" && !strings.HasPrefix(f.Did, "推送了") && !strings.HasPrefix(f.Did, "叫了") {
		w.a.logf("watch %s: %s", f.Rule, f.Did)
	}
}

// ---- the tool and the action ----

func (a *app) toolWatches() string {
	w := a.watch
	rules := w.rules()
	w.mu.Lock()
	errs, log, connected, connErr := slices.Clone(w.errs), slices.Clone(w.log), w.connected, w.connErr
	w.mu.Unlock()
	hub := a.hubAPI()
	var b strings.Builder
	fmt.Fprintf(&b, "家里的提醒规则 %d 条", len(rules))
	switch {
	case len(rules) == 0:
		b.WriteString("（没有）")
	case connected:
		b.WriteString("，正在实时盯着 Home Assistant 的状态变化")
	case connErr != "":
		b.WriteString("，现在没连上 Home Assistant：" + connErr)
	default:
		b.WriteString("，正在连 Home Assistant")
	}
	b.WriteString("\n")
	for _, s := range []string{"notify", "assistant.ask"} {
		if !hub.can(s) {
			b.WriteString("注意：" + errNoScope(s).Error() + "\n")
		}
	}
	for _, r := range rules {
		src := "设置里的"
		if r.Source == "assistant" {
			src = "助手加的"
		}
		fmt.Fprintf(&b, "- %s（%s，%s）\n", r.describe(), r.ID, src)
	}
	for _, e := range errs {
		b.WriteString("- 读不懂：" + e + "\n")
	}
	if len(log) > 0 {
		b.WriteString("## 最近触发\n")
		for i := len(log) - 1; i >= 0 && i >= len(log)-15; i-- {
			f := log[i]
			fmt.Fprintf(&b, "- %s 规则 %s：%s → %s\n", f.At.In(a.loc()).Format("01-02 15:04"), f.Rule, strings.Join(f.Entities, "、"), orDash(f.Did))
		}
	}
	fmt.Fprintf(&b, "\n加 / 删规则用 %s（手机上确认）；设置里的规则在扩展设置的「提醒规则」里改。", watchAction)
	return b.String()
}

type watchWrite struct {
	Op   string
	Rule Rule
}

func (a *app) readWatch(raw json.RawMessage) (write, error) {
	var args struct {
		Op string `json:"op"`
		ID string `json:"id"`
		Rule
	}
	if len(raw) == 0 || json.Unmarshal(raw, &args) != nil {
		return nil, bad("参数要有 op（add 或 remove）")
	}
	w := &watchWrite{Op: strings.TrimSpace(args.Op)}
	switch w.Op {
	case "add":
		r := args.Rule
		r.ID = ""
		if err := r.check(); err != nil {
			return nil, bad(err.Error())
		}
		r.ID, r.Source = ruleID(r), "assistant"
		for _, x := range a.watch.rules() {
			if x.ID == r.ID {
				return nil, bad("已经有这条规则了（" + r.ID + "）")
			}
		}
		if len(a.watch.rules()) >= watchMost {
			return nil, bad(fmt.Sprintf("最多 %d 条规则", watchMost))
		}
		w.Rule = r
	case "remove":
		rid := strings.TrimSpace(args.ID)
		for _, x := range a.watch.rules() {
			if x.ID != rid {
				continue
			}
			if x.Source != "assistant" {
				return nil, bad("这条是扩展设置里写的，要在设置的「提醒规则」里删")
			}
			w.Rule = x
			return w, nil
		}
		return nil, bad("没有规则 " + clip(rid, 20) + "（用 " + id + "_watches 看有哪些）")
	default:
		return nil, bad("op 要是 add 或 remove")
	}
	return w, nil
}

func (w *watchWrite) visible() string { return "你自己（家里的提醒规则）" }
func (w *watchWrite) pin() string     { return "" }

func (w *watchWrite) summary(*app) string {
	if w.Op == "remove" {
		return "删掉家里的提醒规则 " + w.Rule.ID + "：" + w.Rule.describe()
	}
	return "新建家里的提醒规则 " + w.Rule.ID + "：" + w.Rule.describe()
}

func (w *watchWrite) run(_ context.Context, a *app) (string, error) {
	wt := a.watch
	wt.mu.Lock()
	switch w.Op {
	case "add":
		if !slices.ContainsFunc(wt.mine, func(x Rule) bool { return x.ID == w.Rule.ID }) {
			wt.mine = append(wt.mine, w.Rule)
		}
	case "remove":
		wt.mine = slices.DeleteFunc(wt.mine, func(x Rule) bool { return x.ID == w.Rule.ID })
	}
	wt.mu.Unlock()
	if err := wt.save(); err != nil {
		return "", fmt.Errorf("规则存不下：%w", err)
	}
	wt.poke()
	if w.Op == "remove" {
		return "已删掉规则 " + w.Rule.ID, nil
	}
	out := "已加上规则 " + w.Rule.ID + "：" + w.Rule.describe()
	hub := a.hubAPI()
	if (w.Rule.do() != "assistant" && !hub.can("notify")) || (w.Rule.do() != "notify" && !hub.can("assistant.ask")) {
		out += "。注意：hub 还没给扩展发通知 / 开对话的权限，规则触发了也只记在 " + id + "_watches 里"
	}
	return out, nil
}
