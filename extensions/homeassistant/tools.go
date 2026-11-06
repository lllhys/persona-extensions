package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// The tools and actions beyond the first ones (connector.go): automations, the logbook, statistics and energy,
// speakers, cameras and watches.

const cameraTool = id + "_camera_snapshot"

func obj(required []string, props map[string]any) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

var spanProps = map[string]any{
	"start": str("开始时间（用户时区），如 2026-01-02 08:00 或 2026-01-02；不写就是往前 hours / days"),
	"end":   str("结束时间，不写就是现在"),
}

func statProps() map[string]any {
	p := map[string]any{
		"period": map[string]any{"type": "string", "enum": []string{"hour", "day", "week", "month"}, "description": "按什么汇总，默认 day"},
		"days":   map[string]any{"type": "integer", "minimum": 1, "maximum": 730, "description": "最近几天，默认按小时 2、按天 30、按周 90、按月 365"},
	}
	for k, v := range spanProps {
		p[k] = v
	}
	return p
}

var newTools = []tool{
	{Name: id + "_automations",
		Description: "列出 Home Assistant 的自动化、脚本和场景：名字、实体 id、开着还是停用、上次触发时间、自动化的配置 id。触发 / 开关自动化、运行脚本用 " + runAction + "。",
		InputSchema: obj(nil, map[string]any{
			"kind":  map[string]any{"type": "string", "enum": []string{"all", "automation", "script", "scene"}, "description": "只看一种，默认全部"},
			"query": str("名字或实体 id 的一部分")})},
	{Name: id + "_automation_config",
		Description: "读一个自动化的完整配置（triggers、conditions、actions……），JSON。修改或删除自动化前先用它读一次（" + editAction + " 的确认卡片要列出改了什么）。",
		InputSchema: obj([]string{"automation"}, map[string]any{"automation": str("自动化的实体 id（automation.xxx）或配置 id")})},
	{Name: id + "_logbook",
		Description: "全屋的日志：一段时间里家里发生了什么（灯开关、门开关、谁触发了哪个自动化、设备离线……），按时间排好、用户时区，变化太频繁的实体合成一行。可以只看一个区域或一个实体。",
		InputSchema: obj(nil, func() map[string]any {
			p := map[string]any{
				"area":      str("只看这个区域（名字或 id）"),
				"entity_id": str("只看这个实体"),
				"hours":     map[string]any{"type": "integer", "minimum": 1, "maximum": 744, "description": "最近几小时，默认 24"},
			}
			for k, v := range spanProps {
				p[k] = v
			}
			return p
		}())},
	{Name: id + "_statistics",
		Description: "传感器的长期统计（recorder 的小时统计，比历史保存得久，可以查几个月）：按小时 / 天 / 周 / 月汇总。温度湿度这类给每段的平均、最低、最高；电量、水量这类累计的给每段用了多少和合计。最近几天的逐条变化用 " + id + "_history。",
		InputSchema: obj([]string{"entity_id"}, func() map[string]any {
			p := statProps()
			p["entity_id"] = map[string]any{"description": "传感器的实体 id（或统计 id），最多 5 个", "oneOf": []any{
				map[string]any{"type": "string"}, map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1, "maxItems": 5}}}
			return p
		}())},
	{Name: id + "_energy",
		Description: "家里的能耗：Home Assistant 能源设置里的传感器（电网用电、送电、光伏、燃气、用水、各设备用电），按小时 / 天 / 周 / 月汇总，给合计和每段的用量。",
		InputSchema: obj(nil, statProps())},
	{Name: id + "_speakers",
		Description: "列出家里的音箱 / 播放器，以及每台能怎么播报（小米音箱的 xiaomi_miot / xiaomi_home 播报、TTS），不能播报的说缺什么。要在音箱上念一句话用 " + sayAction + "。",
		InputSchema: obj(nil, map[string]any{})},
	{Name: cameraTool,
		Description: "取一个摄像头（camera 实体）现在的画面，作为图片给你看；扫地机的地图这种 camera 实体也行。不写 entity_id 会列出有哪些。",
		InputSchema: obj(nil, map[string]any{"entity_id": str("camera.xxx")})},
	{Name: id + "_watches",
		Description: "家里的提醒规则：Home Assistant 的状态满足条件时（门开了、漏水、设备离线、湿度超过多少……）主动推送到用户手机或叫助手处理。列出规则、是否连着 Home Assistant、最近触发的记录。加 / 删用 " + watchAction + "。",
		InputSchema: obj(nil, map[string]any{})},
}

var newActions = []tool{
	{Name: runAction,
		Description: "运行 Home Assistant 的自动化、脚本和场景：手动触发自动化（op trigger，默认不检查条件）、启用 / 停用自动化（enable / disable）、运行 / 停下脚本（run / stop，可带 variables）、执行场景（run）。用户在手机上确认后才执行。",
		InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"entity_id"}, "properties": map[string]any{
			"entity_id":      str("automation.xxx、script.xxx 或 scene.xxx"),
			"op":             map[string]any{"type": "string", "enum": []string{"trigger", "enable", "disable", "run", "stop"}, "description": "自动化：trigger / enable / disable；脚本：run（默认）/ stop；场景：run"},
			"variables":      map[string]any{"type": "object", "description": "脚本的变量"},
			"skip_condition": map[string]any{"type": "boolean", "description": "手动触发时跳过条件，默认 true"},
		}}},
	{Name: editAction,
		Description: "新建、修改、删除 Home Assistant 的自动化（只能是在界面里建的、有配置 id 的）。修改和删除前先用 " + id + "_automation_config 读出现在的配置；修改要交改好的完整配置。每次都要用户在手机上确认，卡片上有完整配置和改动。",
		InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"op"}, "properties": map[string]any{
			"op":     map[string]any{"type": "string", "enum": []string{"create", "update", "delete"}},
			"id":     str("修改、删除时：自动化的实体 id（automation.xxx）或配置 id"),
			"config": map[string]any{"type": "object", "description": "新建、修改时：完整配置，如 {\"alias\":\"…\",\"description\":\"…\",\"triggers\":[…],\"conditions\":[…],\"actions\":[…],\"mode\":\"single\"}"},
		}}},
	{Name: sayAction,
		Description: "让家里的一台音箱念一句话（小米音箱用它自己的播报，别的用 TTS）。先用 " + id + "_speakers 看哪台能播报。每次都要用户在手机上确认。",
		InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"entity_id", "text"}, "properties": map[string]any{
			"entity_id":  str("音箱的 media_player 或 remote 实体"),
			"text":       map[string]any{"type": "string", "maxLength": sayMost, "description": "要念的话"},
			"method":     map[string]any{"type": "string", "enum": append([]string{"auto"}, sayMethods...), "description": "怎么播报，默认 auto（按 " + id + "_speakers 列的顺序用第一种）"},
			"tts_entity": str("用 TTS 时的引擎，如 tts.google_translate_zh_cn；不写用第一个"),
		}}},
	{Name: watchAction,
		Description: "加或删一条家里的提醒规则：Home Assistant 的实体满足条件时（变成某个状态、离线、数值高于 / 低于，可以要求持续多久）推送到用户手机（do notify）或叫助手处理（do assistant，message 写要助手做什么）。看谁可以是一个实体、带 * 的通配、一个区域、一种类型或 device_class。用户在手机上确认后才生效。",
		InputSchema: map[string]any{"type": "object", "additionalProperties": false, "required": []string{"op"}, "properties": map[string]any{
			"op":               map[string]any{"type": "string", "enum": []string{"add", "remove"}},
			"id":               str("删除时：规则的 id（" + id + "_watches 列出）"),
			"name":             str("规则的名字，如 门开了"),
			"entity_id":        str("实体 id，可以带 *，如 binary_sensor.*_door"),
			"area":             str("区域的名字或 id"),
			"domain":           str("类型，如 binary_sensor、sensor、switch"),
			"device_class":     str("如 door、moisture、humidity、connectivity"),
			"to":               str("变成什么状态（原值，如 on、off、open）；offline 是离线"),
			"from":             str("从什么状态变过来"),
			"above":            map[string]any{"type": "number", "description": "数值高于"},
			"below":            map[string]any{"type": "number", "description": "数值低于"},
			"attribute":        str("above / below 看这个属性，不写看状态"),
			"for_seconds":      map[string]any{"type": "integer", "minimum": 0, "description": "条件要持续几秒才算（离线默认 600）"},
			"do":               map[string]any{"type": "string", "enum": []string{"notify", "assistant", "both"}, "description": "默认 notify"},
			"message":          str("推送里附的一句话，或要助手做什么"),
			"cooldown_minutes": map[string]any{"type": "integer", "minimum": 0, "description": "同一个实体多久内只提醒一次，默认 30"},
			"level":            map[string]any{"type": "string", "enum": []string{"active", "time-sensitive"}, "description": "time-sensitive 会穿透专注模式（漏水、烟雾用）"},
		}}},
}

func init() {
	tools = append(tools, newTools...)
	actions = append(actions, newActions...)
}

// callNewTool runs the tools of this file (false: not one of them).
func (a *app) callNewTool(ctx context.Context, name string, raw json.RawMessage) (any, bool, error) {
	var args struct {
		Kind       string          `json:"kind"`
		Query      string          `json:"query"`
		Automation string          `json:"automation"`
		Area       string          `json:"area"`
		EntityID   json.RawMessage `json:"entity_id"`
		Hours      int             `json:"hours"`
		Start      string          `json:"start"`
		End        string          `json:"end"`
		Period     string          `json:"period"`
		Days       int             `json:"days"`
	}
	known := false
	for _, t := range newTools {
		known = known || t.Name == name
	}
	if !known {
		return nil, false, nil
	}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, true, fmt.Errorf("参数不对：%w", err)
		}
	}
	var ids []string
	var one string
	if json.Unmarshal(args.EntityID, &one) == nil {
		if one = strings.TrimSpace(one); one != "" {
			ids = []string{one}
		}
	} else if json.Unmarshal(args.EntityID, &ids) != nil && len(args.EntityID) > 0 {
		return nil, true, fmt.Errorf("entity_id 要是一个实体 id 或几个")
	}
	text := func(s string, err error) (any, bool, error) {
		if err != nil {
			return nil, true, err
		}
		return map[string]any{"text": s}, true, nil
	}
	switch name {
	case id + "_automations":
		return text(a.toolAutomations(ctx, args.Kind, args.Query))
	case id + "_automation_config":
		return text(a.toolAutomationConfig(ctx, args.Automation))
	case id + "_logbook":
		return text(a.toolLogbook(ctx, args.Area, one, args.Start, args.End, args.Hours))
	case id + "_statistics":
		return text(a.toolStatistics(ctx, statArgs{IDs: ids, Period: args.Period, Days: args.Days, Start: args.Start, End: args.End}))
	case id + "_energy":
		return text(a.toolEnergy(ctx, statArgs{Period: args.Period, Days: args.Days, Start: args.Start, End: args.End}))
	case id + "_speakers":
		return text(a.toolSpeakers(ctx))
	case cameraTool:
		res, err := a.toolCamera(ctx, one)
		if err != nil {
			return nil, true, err
		}
		return res, true, nil
	case id + "_watches":
		return text(a.toolWatches(), nil)
	}
	return nil, false, nil
}
