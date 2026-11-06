package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// A sentence on a speaker (homeassistant_say, homeassistant_speakers). Xiaomi speakers speak through their
// integration's own way, so each is tried in turn, by what Home Assistant has:
//
//  1. xiaomi_miot: the service xiaomi_miot.intelligent_speaker {entity_id, text, execute: false}.
//  2. xiaomi_home: a notify entity of the speaker's device for its play-text action (notify.<…>_play_text_…),
//     called with notify.send_message and the action's inputs as a JSON list (["text"]).
//  3. a text entity of the device for play-text (text.<…>_play_text…, xiaomi_miot makes these), set with
//     text.set_value.
//  4. tts.speak with a TTS engine (a tts.* entity) on the media player.
//  5. media_player.play_media with the engine's media-source URL (media-source://tts/<engine>?message=…).
//
// Without any of them the speaker cannot speak, and the answer says what is missing.

const sayMost = 300 // characters in one sentence

var sayMethods = []string{"xiaomi_miot", "xiaomi_home", "text", "tts", "media_source"}

var sayNames = map[string]string{"xiaomi_miot": "xiaomi_miot 的小爱播报", "xiaomi_home": "xiaomi_home 的播报实体",
	"text": "播报文字实体", "tts": "Home Assistant 的 TTS（tts.speak）", "media_source": "TTS 媒体源（media_player.play_media）"}

// sayPlan is how a speaker says a sentence: one service call.
type sayPlan struct {
	Method, Domain, Service, Via string
	Data                         map[string]any
}

// speakerKit is what Home Assistant has for one speaker, read from the caches.
type speakerKit struct {
	Speaker  State
	Platform string   // its integration, when the registry says
	Siblings []string // the entities of its device
	services map[string]map[string]Service
	tts      []string // the TTS engines (tts.* entities)
}

func (k speakerKit) has(domain, service string) bool {
	if k.services == nil {
		return false
	}
	_, ok := k.services[domain][service]
	return ok
}

// sibling finds an entity of the speaker's device in domain whose id has play_text.
func (k speakerKit) sibling(domain string) string {
	for _, e := range k.Siblings {
		if strings.HasPrefix(e, domain+".") && strings.Contains(e, "play_text") {
			return e
		}
	}
	return ""
}

// kit reads what Home Assistant has for speaker, from what is cached (states, services, registry).
func (a *app) kit(speaker string) (speakerKit, error) {
	h, _, _ := a.client()
	hm := a.cachedHome()
	h.mu.Lock()
	services := h.services
	h.mu.Unlock()
	if hm == nil || services == nil {
		return speakerKit{}, bad("还没读过 Home Assistant 的设备和服务：先用 " + id + "_speakers 看一下有哪些音箱")
	}
	s, ok := hm.ByID[speaker]
	if !ok || (hm.Hidden[speaker] && hm.AreaOf[speaker] == nil) {
		return speakerKit{}, bad("Home Assistant 里没有 " + speaker + "（用 " + id + "_speakers 看有哪些音箱）")
	}
	if d := s.Domain(); d != "media_player" && d != "remote" {
		return speakerKit{}, bad("音箱要写它的 media_player 或 remote 实体（用 " + id + "_speakers 看）")
	}
	k := speakerKit{Speaker: s, services: services}
	if reg := h.cachedRegistry(); reg != nil {
		if re, ok := reg.Entities[speaker]; ok {
			k.Platform = re.Platform
			if re.DeviceID != "" {
				k.Siblings = reg.ByDevice[re.DeviceID]
			}
		}
	}
	if len(k.Siblings) == 0 { // no registry: the entities named after the speaker's device
		_, obj, _ := strings.Cut(speaker, ".")
		for _, suffix := range []string{"_wifispeaker", "_speaker", "_play_control", "_media_player"} {
			obj = strings.TrimSuffix(obj, suffix)
		}
		for _, x := range hm.States {
			if _, o, _ := strings.Cut(x.EntityID, "."); strings.HasPrefix(o, obj) {
				k.Siblings = append(k.Siblings, x.EntityID)
			}
		}
	}
	for _, x := range hm.States {
		if x.Domain() == "tts" && !x.Unavailable() {
			k.tts = append(k.tts, x.EntityID)
		}
	}
	return k, nil
}

// plans are the ways k's speaker can say text, in order; missing says why each other one cannot.
func (k speakerKit) plans(text, engine string) (plans []sayPlan, missing []string) {
	sp := k.Speaker.EntityID
	isMP := k.Speaker.Domain() == "media_player"
	// 1. xiaomi_miot
	switch {
	case !k.has("xiaomi_miot", "intelligent_speaker"):
		missing = append(missing, "xiaomi_miot：没有服务 xiaomi_miot.intelligent_speaker（没装 xiaomi_miot 集成）")
	case k.Platform != "" && k.Platform != "xiaomi_miot":
		missing = append(missing, "xiaomi_miot：这台音箱是 "+k.Platform+" 集成的，xiaomi_miot.intelligent_speaker 只认 xiaomi_miot 的实体")
	default:
		plans = append(plans, sayPlan{Method: "xiaomi_miot", Domain: "xiaomi_miot", Service: "intelligent_speaker", Via: sp,
			Data: map[string]any{"entity_id": []string{sp}, "text": text, "execute": false, "silent": false}})
	}
	// 2. xiaomi_home's notify entity
	if n := k.sibling("notify"); n == "" {
		missing = append(missing, "xiaomi_home：这台音箱的设备上没有播报的 notify 实体（notify.…_play_text_…）")
	} else if !k.has("notify", "send_message") {
		missing = append(missing, "xiaomi_home：有 "+n+"，但 Home Assistant 没有服务 notify.send_message")
	} else {
		msg, _ := json.Marshal([]string{text})
		plans = append(plans, sayPlan{Method: "xiaomi_home", Domain: "notify", Service: "send_message", Via: n,
			Data: map[string]any{"entity_id": []string{n}, "message": string(msg)}})
	}
	// 3. a play-text text entity
	if t := k.sibling("text"); t == "" {
		missing = append(missing, "播报文字实体：这台音箱的设备上没有 text.…play_text… 实体")
	} else if !k.has("text", "set_value") {
		missing = append(missing, "播报文字实体：有 "+t+"，但 Home Assistant 没有服务 text.set_value")
	} else {
		plans = append(plans, sayPlan{Method: "text", Domain: "text", Service: "set_value", Via: t, Data: map[string]any{"entity_id": []string{t}, "value": text}})
	}
	// 4, 5. a TTS engine
	eng := engine
	if eng == "" && len(k.tts) > 0 {
		eng = k.tts[0]
	}
	switch {
	case !isMP:
		missing = append(missing, "TTS：要音箱的 media_player 实体（"+sp+" 不是播放器）")
	case eng == "":
		missing = append(missing, "TTS：Home Assistant 里没有 TTS 引擎（tts.* 实体，如 Google Translate、Home Assistant Cloud、Piper）")
	default:
		if k.has("tts", "speak") {
			plans = append(plans, sayPlan{Method: "tts", Domain: "tts", Service: "speak", Via: eng,
				Data: map[string]any{"entity_id": []string{eng}, "media_player_entity_id": sp, "message": text}})
		} else {
			missing = append(missing, "TTS：没有服务 tts.speak")
		}
		if k.has("media_player", "play_media") {
			plans = append(plans, sayPlan{Method: "media_source", Domain: "media_player", Service: "play_media", Via: eng,
				Data: map[string]any{"entity_id": []string{sp}, "media_content_type": "music",
					"media_content_id": "media-source://tts/" + eng + "?message=" + url.QueryEscape(text)}})
		} else {
			missing = append(missing, "TTS 媒体源：没有服务 media_player.play_media")
		}
	}
	return plans, missing
}

// errNoSay is the answer for a speaker that cannot speak: every way and why not.
func errNoSay(s State, missing []string) error {
	return bad(fmt.Sprintf("「%s」（%s）不支持播报：\n- %s", s.Name(), s.EntityID, strings.Join(missing, "\n- ")))
}

type sayWrite struct {
	Name, Speaker, Text string
	Plan                sayPlan
}

func (a *app) readSay(raw json.RawMessage) (write, error) {
	var args struct {
		EntityID string `json:"entity_id"`
		Text     string `json:"text"`
		Method   string `json:"method"`
		Engine   string `json:"tts_entity"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &args) != nil || !entityRE.MatchString(args.EntityID) {
		return nil, bad("参数要有 entity_id（音箱）和 text（要念的话）")
	}
	text := strings.Join(strings.Fields(args.Text), " ")
	switch {
	case text == "":
		return nil, bad("text 不能是空的")
	case utf8.RuneCountInString(text) > sayMost:
		return nil, bad(fmt.Sprintf("一次最多念 %d 个字", sayMost))
	}
	if args.Engine != "" && (!entityRE.MatchString(args.Engine) || !strings.HasPrefix(args.Engine, "tts.")) {
		return nil, bad("tts_entity 要是 tts.xxx")
	}
	method := strings.TrimSpace(args.Method)
	if method != "" && method != "auto" && !slices.Contains(sayMethods, method) {
		return nil, bad("method 要是 auto、" + strings.Join(sayMethods, "、"))
	}
	k, err := a.kit(args.EntityID)
	if err != nil {
		return nil, err
	}
	if err := a.canWrite(k.Speaker.Domain(), false); err != nil {
		return nil, bad(err.Error())
	}
	plans, missing := k.plans(text, args.Engine)
	w := &sayWrite{Name: k.Speaker.Name(), Speaker: args.EntityID, Text: text}
	for _, p := range plans {
		if method == "" || method == "auto" || p.Method == method {
			w.Plan = p
			return w, nil
		}
	}
	if method != "" && method != "auto" && len(plans) > 0 {
		var have []string
		for _, p := range plans {
			have = append(have, p.Method)
		}
		return nil, bad(fmt.Sprintf("「%s」不能用 %s 播报；能用：%s", k.Speaker.Name(), method, strings.Join(have, "、")))
	}
	return nil, errNoSay(k.Speaker, missing)
}

func (w *sayWrite) visible() string { return "家里的人（音箱念出来）" }
func (w *sayWrite) pin() string     { return "" }

func (w *sayWrite) summary(*app) string {
	return fmt.Sprintf("在「%s」（%s）上念：「%s」\n方式：%s，%s.%s（%s）", w.Name, w.Speaker, w.Text, sayNames[w.Plan.Method], w.Plan.Domain, w.Plan.Service, w.Plan.Via)
}

func (w *sayWrite) run(ctx context.Context, a *app) (string, error) {
	h, _, _ := a.client()
	if err := h.Call(ctx, w.Plan.Domain, w.Plan.Service, w.Plan.Data); err != nil {
		return "", err
	}
	return fmt.Sprintf("已让「%s」念了（%s）", w.Name, sayNames[w.Plan.Method]), nil
}

// toolSpeakers lists the speakers and how each can speak.
func (a *app) toolSpeakers(ctx context.Context) (string, error) {
	hm, err := a.home(ctx, statesFor)
	if err != nil {
		return "", err
	}
	h, _, _ := a.client()
	if _, err := h.Services(ctx); err != nil {
		return "", err
	}
	_, _ = h.Registry(ctx) // without it the device's entities are guessed from the names
	var b strings.Builder
	n := 0
	for _, s := range hm.States {
		if hm.Hidden[s.EntityID] && hm.AreaOf[s.EntityID] == nil {
			continue
		}
		if s.Domain() != "media_player" && !(s.Domain() == "remote" && strings.Contains(s.EntityID, "speaker")) {
			continue
		}
		k, err := a.kit(s.EntityID)
		if err != nil {
			continue
		}
		n++
		plans, missing := k.plans("你好", "")
		line := fmt.Sprintf("- %s（%s）：%s", s.Name(), s.EntityID, orDash(stateText(s)))
		if ar := hm.areaName(s.EntityID); ar != "" {
			line += "，在" + ar
		}
		if k.Platform != "" {
			line += "，" + k.Platform + " 集成"
		}
		b.WriteString(line + "\n")
		if len(plans) == 0 {
			b.WriteString("  不支持播报：" + strings.Join(missing, "；") + "\n")
			continue
		}
		var ways []string
		for _, p := range plans {
			ways = append(ways, fmt.Sprintf("%s（%s，%s.%s）", p.Method, sayNames[p.Method], p.Domain, p.Service))
		}
		b.WriteString("  能播报：" + strings.Join(ways, "；") + "，默认用第一种\n")
		if len(missing) > 0 {
			b.WriteString("  其他方式不行：" + strings.Join(missing, "；") + "\n")
		}
	}
	if n == 0 {
		return "Home Assistant 里没有音箱（media_player）", nil
	}
	return fmt.Sprintf("%d 个音箱 / 播放器（%s）：\n%s要念一句话用 %s。", n, time.Now().In(a.loc()).Format("15:04"), b.String(), sayAction), nil
}
