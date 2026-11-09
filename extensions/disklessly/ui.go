package main

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

const (
	pageUI = "disklessly"
	cardUI = "disklessly-card"
)

type page struct {
	Machine string `json:"m,omitempty"`
	Tab     string `json:"t,omitempty"`
	Mode    string `json:"mode,omitempty"`
	Query   string `json:"q,omitempty"`
}

const (
	tabFleet    = "fleet"
	tabItems    = "items"
	tabPlan     = "plan"
	tabSessions = "sessions"

	modeHome       = ""
	modeNewMachine = "new-machine"
	modeEditMach   = "edit-machine"
)

type draw struct {
	st  *State
	now time.Time
	loc *time.Location
}

func (a *app) render(_ context.Context, r *ext.UIRequest) (*wire.View, error) {
	var p page
	if err := r.DecodeState(&p); err != nil {
		return nil, &ext.Error{Code: "invalid", Message: "invalid page state"}
	}
	var v *wire.View
	err := a.db().read(func(st *State) { v = a.drawer(st, r).view(p) })
	return v, err
}

func (a *app) drawer(st *State, r *ext.UIRequest) draw {
	loc, err := time.LoadLocation(r.TZ)
	if err != nil {
		loc = time.UTC
	}
	return draw{st: st, now: a.now(), loc: loc}
}

func (d draw) view(p page) *wire.View {
	switch p.Mode {
	case modeNewMachine:
		return d.machineForm(p, nil)
	case modeEditMach:
		m := d.st.machine(p.Machine)
		if m == nil {
			return d.home(page{})
		}
		return d.machineForm(p, m)
	}
	return d.home(p)
}

func (d draw) home(p page) *wire.View {
	statuses := d.st.statuses()
	if len(statuses) == 0 {
		d.st.ensureDefaults(d.now)
		statuses = d.st.statuses()
	}

	selected := d.st.machine(p.Machine)
	if selected == nil || !selected.Enabled {
		selected = statuses[0].Machine
	}
	p.Machine = selected.ID
	if p.Tab != tabFleet && p.Tab != tabItems && p.Tab != tabPlan && p.Tab != tabSessions {
		p.Tab = tabFleet
	}

	v := &wire.View{Title: "Disklessly 机器磁盘"}
	choices := make([]wire.Choice, 0, len(statuses)+1)
	for _, ms := range statuses {
		label := ms.Machine.Host
		if ms.Machine.IsLocal {
			label = "local (本机)"
		}
		if ms.TotalBytes > 0 {
			label = fmt.Sprintf("%s · %.0f%%", label, ms.UsedPercent)
		}
		choices = append(choices, wire.Choice{Value: ms.Machine.ID, Title: clip(label, 28)})
	}
	choices = append(choices, wire.Choice{Value: "+", Title: "＋ 添加 SSH 机器"})
	v.Filter = &wire.Filter{
		Name:    "machine",
		Title:   "机器",
		Choices: choices,
		Value:   selected.ID,
		Action:  wire.Action{ID: "machine"},
	}

	scan := d.st.Scans[selected.ID]
	itemCount := 0
	if scan != nil {
		itemCount = len(scan.Items)
	}
	planCount := 0
	if scan != nil {
		if pl, err := BuildCleanPlan(selected, scan, nil, nil, false); err == nil {
			planCount = len(pl.Items)
		}
	}

	v.Tabs = &wire.Tabs{
		Name:  "tab",
		Value: p.Tab,
		Items: []wire.Tab{
			{Value: tabFleet, Title: "多机总览", Badge: strconv.Itoa(len(statuses))},
			{Value: tabItems, Title: "可回收明细", Badge: strconv.Itoa(itemCount)},
			{Value: tabPlan, Title: "清理计划", Badge: strconv.Itoa(planCount)},
			{Value: tabSessions, Title: "会话与台账", Badge: strconv.Itoa(len(d.st.History))},
		},
		Action: wire.Action{ID: "tab"},
	}
	v.State = ext.EncodeState(p)

	switch p.Tab {
	case tabItems:
		v.Search = &wire.Search{Placeholder: "搜索分类、路径或说明", Value: p.Query, Action: wire.Action{ID: "search"}}
		v.Sections = d.itemsTab(p, selected, scan)
	case tabPlan:
		v.Sections = d.planTab(p, selected, scan)
	case tabSessions:
		v.Sections = d.sessionsAndHistoryTab(p, selected, scan)
	default:
		v.Sections = d.fleetTab(p, statuses, selected)
	}
	return v
}

// ---- Tab 1: Fleet Disk Overview ----

func (d draw) fleetTab(p page, statuses []MachineStatus, selected *Machine) []wire.Section {
	var totalReclaim, totalSafe, totalFreed uint64
	var warnCount, critCount int
	for _, ms := range statuses {
		totalReclaim += ms.ReclaimableBytes
		totalSafe += ms.SafeBytes
		switch ms.Level {
		case HealthCritical:
			critCount++
		case HealthWarning:
			warnCount++
		}
	}
	for _, h := range d.st.History {
		totalFreed += h.FreedBytes
	}

	alertText := "全部正常"
	alertTone := wire.TonePositive
	if critCount > 0 {
		alertText = fmt.Sprintf("%d 台红色告警", critCount)
		alertTone = wire.ToneDanger
	} else if warnCount > 0 {
		alertText = fmt.Sprintf("%d 台橙色预警", warnCount)
		alertTone = wire.ToneCaution
	}

	top := wire.Section{
		Title: "多机器磁盘水位与回收总览",
		Items: []wire.Component{
			{
				Type: wire.ComponentStatGrid,
				Stats: []wire.Stat{
					{Label: "纳管机器", Value: fmt.Sprintf("%d 台", len(statuses)), Detail: "本机 + SSH 远程节点"},
					{Label: "水位状态", Value: alertText, Tone: alertTone},
					{Label: "总可回收空间", Value: FormatBytes(totalReclaim), Detail: "安全项 " + FormatBytes(totalSafe), Tone: toneIf(totalReclaim > 0, wire.TonePositive)},
					{Label: "累计已释放", Value: FormatBytes(totalFreed), Detail: fmt.Sprintf("共 %d 次清理记录", len(d.st.History))},
				},
			},
			{
				Type:   wire.ComponentButton,
				Title:  "扫描全部机器",
				Action: &wire.Action{ID: "scan-all"},
			},
		},
	}

	var secs []wire.Section
	secs = append(secs, top)

	for _, ms := range statuses {
		m := ms.Machine
		badge := healthBadge(ms)
		ratio := math.Max(0, math.Min(1, ms.UsedPercent/100.0))
		pTone := progressTone(ms.Level)

		var subParts []string
		if m.IsLocal {
			subParts = append(subParts, "本机节点")
		} else {
			subParts = append(subParts, "SSH "+m.Host)
		}
		if m.Platform != "" {
			subParts = append(subParts, m.Platform)
		}
		if m.ScanMode != "" {
			subParts = append(subParts, m.ScanMode)
		}
		if ms.LastScannedAt != nil {
			subParts = append(subParts, "扫描于 "+d.when(*ms.LastScannedAt))
		} else {
			subParts = append(subParts, "尚未扫描")
		}

		sec := wire.Section{
			Title: m.Name,
			Items: []wire.Component{
				{
					Type:     wire.ComponentRow,
					Key:      "mach-" + m.ID,
					Icon:     machineIcon(ms.Level),
					Title:    m.Name,
					Subtitle: strings.Join(subParts, " · "),
					Badge:    badge,
					Action:   &wire.Action{ID: "select-machine", Values: map[string]string{"m": m.ID, "t": tabItems}},
				},
			},
		}

		if ms.TotalBytes > 0 {
			label := fmt.Sprintf("已用 %s / %s（可用 %s · 可回收 %s）",
				FormatBytes(ms.UsedBytes), FormatBytes(ms.TotalBytes), FormatBytes(ms.AvailableBytes), FormatBytes(ms.ReclaimableBytes))
			sec.Items = append(sec.Items, wire.Component{
				Type:  wire.ComponentProgress,
				Key:   "prog-" + m.ID,
				Label: label,
				Value: fmt.Sprintf("%.1f%%", ms.UsedPercent),
				Ratio: &ratio,
				Tone:  pTone,
			})
		} else if m.LastError != "" {
			sec.Items = append(sec.Items, wire.Component{
				Type:  wire.ComponentText,
				Style: wire.TextStyleSecondary,
				Text:  "上次探测异常：" + m.LastError,
			})
		} else {
			sec.Items = append(sec.Items, wire.Component{
				Type:  wire.ComponentText,
				Style: wire.TextStyleSecondary,
				Text:  "点击下方「扫描」探测根分区磁盘水位、可回收缓存与 Agent 会话分布。",
			})
		}

		if ms.CleanCount > 0 {
			sec.Footer = fmt.Sprintf("上次清理释放 %s · 累计清理 %d 次共释放 %s",
				FormatBytes(ms.LastFreedBytes), ms.CleanCount, FormatBytes(ms.TotalFreedBytes))
		}

		sec.Items = append(sec.Items,
			wire.Component{
				Type:   wire.ComponentButton,
				Key:    "btn-scan-" + m.ID,
				Title:  "扫描 " + m.Name,
				Action: &wire.Action{ID: "scan", Values: map[string]string{"m": m.ID}},
			},
			wire.Component{
				Type:   wire.ComponentButton,
				Key:    "btn-plan-" + m.ID,
				Title:  "生成清理计划",
				Action: &wire.Action{ID: "open-plan", Values: map[string]string{"m": m.ID}},
			},
		)
		if ms.SafeBytes > 0 {
			sec.Items = append(sec.Items, wire.Component{
				Type:  wire.ComponentButton,
				Key:   "btn-clean-" + m.ID,
				Title: fmt.Sprintf("一键安全清理 (%s)", FormatBytes(ms.SafeBytes)),
				Action: &wire.Action{
					ID:      "clean-safe",
					Values:  map[string]string{"m": m.ID},
					Confirm: fmt.Sprintf("对「%s」执行全部 safe 等级可重建缓存清理与 >7天冷会话归档（预计释放 %s）？运行中容器、活跃仓库与 7 天内会话绝不触碰。", m.Name, FormatBytes(ms.SafeBytes)),
				},
			})
		}
		secs = append(secs, sec)
	}

	manage := wire.Section{
		Title:  "机器节点管理",
		Footer: "自动同步 ~/.persona/config.json 与 ~/.ssh/config 中的机器节点，也支持手动新增或编辑 SSH 节点。",
		Items: []wire.Component{
			{
				Type:     wire.ComponentRow,
				Icon:     "settings",
				Title:    "编辑当前机器节点 (" + selected.Name + ")",
				Subtitle: cmp.Or(selected.Host, "localhost") + " · 来源 " + selected.Source,
				Action:   &wire.Action{ID: "edit-machine", Values: map[string]string{"m": selected.ID}},
			},
			{
				Type:   wire.ComponentButton,
				Title:  "手动添加 SSH 机器",
				Action: &wire.Action{ID: "new-machine"},
			},
		},
	}
	secs = append(secs, manage)
	return secs
}

// ---- Tab 2: Reclaimable Categories & Items Table ----

func (d draw) itemsTab(p page, m *Machine, scan *Scan) []wire.Section {
	ms := d.st.machineStatus(m)
	head := d.machineHeaderComponent(ms, scan)
	top := wire.Section{
		Items: []wire.Component{
			head,
			{
				Type:   wire.ComponentButton,
				Title:  "立即重新扫描 " + m.Name,
				Action: &wire.Action{ID: "scan", Values: map[string]string{"m": m.ID}},
			},
		},
	}
	if scan == nil {
		return []wire.Section{
			top,
			{
				Items: []wire.Component{
					{
						Type:   wire.ComponentEmpty,
						Icon:   "gauge",
						Title:  m.Name + " 尚未扫描",
						Detail: "点击立即扫描探测根分区容量及 Go、Node/Python、Docker、Rust/Xcode、系统日志与冷会话。",
						Button: &wire.EmptyButton{
							Title:  "扫描 " + m.Name,
							Action: wire.Action{ID: "scan", Values: map[string]string{"m": m.ID}},
						},
					},
				},
			},
		}
	}

	q := strings.ToLower(strings.TrimSpace(p.Query))
	var filtered []ScanItem
	for _, it := range scan.Items {
		if q != "" {
			hay := strings.ToLower(it.Title + " " + it.Path + " " + it.Category + " " + CategoryLabels[it.Category] + " " + it.Reason)
			if !strings.Contains(hay, q) {
				continue
			}
		}
		filtered = append(filtered, it)
	}

	if len(filtered) == 0 {
		return []wire.Section{
			top,
			{
				Items: []wire.Component{
					{
						Type:   wire.ComponentEmpty,
						Icon:   "check",
						Title:  "没有发现待清理缓存项",
						Detail: "当前机器非常干净，或搜索条件没有匹配项。",
					},
				},
			},
		}
	}

	// Category summary table
	catBytes := map[string]uint64{}
	catCounts := map[string]int{}
	for _, it := range filtered {
		catBytes[it.Category] += it.Bytes
		catCounts[it.Category]++
	}
	var catRows [][]string
	for _, cid := range CategoryIDs {
		if catBytes[cid] == 0 {
			continue
		}
		catRows = append(catRows, []string{
			CategoryLabels[cid],
			cid,
			strconv.Itoa(catCounts[cid]),
			FormatBytes(catBytes[cid]),
		})
	}

	catSec := wire.Section{
		Title: "可回收分类汇总（按分类一键清理）",
		Items: []wire.Component{
			{
				Type: wire.ComponentTable,
				Columns: []wire.TableColumn{
					{Title: "分类名称"},
					{Title: "标识"},
					{Title: "项数", Align: "end"},
					{Title: "可释放体积", Align: "end"},
				},
				Rows: catRows,
			},
		},
	}

	// Per-category quick clean rows
	for _, cid := range CategoryIDs {
		if catBytes[cid] == 0 {
			continue
		}
		catSec.Items = append(catSec.Items, wire.Component{
			Type:     wire.ComponentRow,
			Key:      "cat-clean-" + cid,
			Icon:     "folder",
			Title:    "清理分类：" + CategoryLabels[cid],
			Subtitle: fmt.Sprintf("%d 项 · 预计释放 %s", catCounts[cid], FormatBytes(catBytes[cid])),
			Badge:    &wire.Badge{Text: FormatBytes(catBytes[cid]), Tone: wire.TonePositive},
			Action: &wire.Action{
				ID:      "clean-category",
				Values:  map[string]string{"m": m.ID, "cat": cid},
				Confirm: fmt.Sprintf("在「%s」上清理分类「%s」（共 %d 项，预计释放 %s）？", m.Name, CategoryLabels[cid], catCounts[cid], FormatBytes(catBytes[cid])),
			},
		})
	}

	// Detailed items table sorted by volume
	var itemRows [][]string
	detailSec := wire.Section{
		Title:  "可回收分类明细表（按体积排序）",
		Footer: "安全保护红线：绝不触碰运行中的服务容器、活跃 git 仓库未提交改动、7 天内活跃的 Agent 会话或凭据配置。",
	}
	for _, it := range filtered {
		safetyTag := "safe"
		if it.Safety == SafetyReview {
			safetyTag = "review"
		}
		itemRows = append(itemRows, []string{
			clip(it.Title, 48),
			CategoryLabels[it.Category],
			clip(it.Path, 56),
			FormatBytes(it.Bytes),
			safetyTag,
		})
	}
	detailSec.Items = append(detailSec.Items, wire.Component{
		Type: wire.ComponentTable,
		Columns: []wire.TableColumn{
			{Title: "项目"},
			{Title: "所属分类"},
			{Title: "路径"},
			{Title: "体积", Align: "end"},
			{Title: "等级"},
		},
		Rows: itemRows,
	})

	for _, it := range filtered {
		bTone := wire.TonePositive
		bText := "safe · " + FormatBytes(it.Bytes)
		if it.Safety == SafetyReview {
			bTone = wire.ToneCaution
			bText = "review · " + FormatBytes(it.Bytes)
		}
		actVerb := "清理"
		if it.Action == "archive" {
			actVerb = "归档"
		}
		detailSec.Items = append(detailSec.Items, wire.Component{
			Type:     wire.ComponentRow,
			Key:      "item-" + it.ID,
			Title:    it.Title,
			Subtitle: fmt.Sprintf("%s · %s", it.Path, it.Reason),
			Badge:    &wire.Badge{Text: bText, Tone: bTone},
			Action: &wire.Action{
				ID:      "clean-item",
				Values:  map[string]string{"m": m.ID, "item": it.ID},
				Confirm: fmt.Sprintf("确认在「%s」上%s「%s」（路径 %s，预计释放 %s）？", m.Name, actVerb, it.Title, it.Path, FormatBytes(it.Bytes)),
			},
		})
	}

	return []wire.Section{top, catSec, detailSec}
}

// ---- Tab 3: Dry-Run Plan & Safe Clean ----

func (d draw) planTab(p page, m *Machine, scan *Scan) []wire.Section {
	ms := d.st.machineStatus(m)
	head := d.machineHeaderComponent(ms, scan)
	if scan == nil {
		return []wire.Section{
			{Items: []wire.Component{head}},
			{
				Items: []wire.Component{
					{
						Type:   wire.ComponentEmpty,
						Icon:   "list",
						Title:  "尚未生成清理计划",
						Detail: "请先扫描该机器以生成 Dry-Run 安全清理计划。",
						Button: &wire.EmptyButton{
							Title:  "扫描并生成计划",
							Action: wire.Action{ID: "scan", Values: map[string]string{"m": m.ID}},
						},
					},
				},
			},
		}
	}

	plan, _ := BuildCleanPlan(m, scan, nil, nil, true)
	afterSafeAvail := scan.Mount.AvailableBytes + scan.SafeBytes
	var secs []wire.Section

	summarySec := wire.Section{
		Title:  "安全清理计划预览 (Dry-Run Plan)",
		Footer: "安全等级说明：safe 为可无损自动重建的编译器/包缓存或 >7 天冷会话归档；review 为较大体积产物（如无头浏览器镜像），重新运行时按需下载。",
		Items: []wire.Component{
			head,
			{
				Type: wire.ComponentStatGrid,
				Stats: []wire.Stat{
					{Label: "当前可用空间", Value: FormatBytes(scan.Mount.AvailableBytes), Detail: scan.Mount.MountPoint},
					{Label: "Safe 预计释放", Value: FormatBytes(scan.SafeBytes), Tone: wire.TonePositive, Detail: "无损可重建项"},
					{Label: "清理后预计可用", Value: FormatBytes(afterSafeAvail), Tone: wire.TonePositive, Detail: fmt.Sprintf("%s → %s", FormatBytes(scan.Mount.AvailableBytes), FormatBytes(afterSafeAvail))},
					{Label: "Review 待确认项", Value: FormatBytes(scan.ReviewBytes), Tone: toneIf(scan.ReviewBytes > 0, wire.ToneCaution)},
				},
			},
		},
	}

	if scan.SafeBytes > 0 {
		summarySec.Items = append(summarySec.Items, wire.Component{
			Type:  wire.ComponentButton,
			Title: fmt.Sprintf("一键执行全部 Safe 清理计划 (+%s)", FormatBytes(scan.SafeBytes)),
			Action: &wire.Action{
				ID:      "clean-safe",
				Values:  map[string]string{"m": m.ID},
				Confirm: fmt.Sprintf("确认对「%s」执行 Dry-Run 计划中的全部 safe 项（预计可用空间 %s → %s，释放 +%s）？", m.Name, FormatBytes(scan.Mount.AvailableBytes), FormatBytes(afterSafeAvail), FormatBytes(scan.SafeBytes)),
			},
		})
	}

	secs = append(secs, summarySec)

	// Interactive category checklist form for customized clean execution
	formSec := wire.Section{
		Title:  "按分类勾选执行清理",
		Footer: "打开对应分类开关后点击「按勾选分类执行清理」，自动在清理前后通过 df 实测释放字节并计入历史台账。",
	}
	catTotals := map[string]uint64{}
	for _, it := range scan.Items {
		if !it.InUse {
			catTotals[it.Category] += it.Bytes
		}
	}
	hasAnyToggle := false
	for _, cid := range CategoryIDs {
		tot := catTotals[cid]
		if tot == 0 {
			continue
		}
		hasAnyToggle = true
		defaultVal := "true"
		if cid == CatRustXcodeBuild && scan.ReviewBytes > 0 && scan.SafeBytes == 0 {
			defaultVal = "false"
		}
		formSec.Items = append(formSec.Items, wire.Component{
			Type:  wire.ComponentField,
			Name:  "cat_" + cid,
			Label: fmt.Sprintf("%s (%s)", CategoryLabels[cid], FormatBytes(tot)),
			Kind:  wire.FieldKindToggle,
			Value: defaultVal,
		})
	}
	formSec.Items = append(formSec.Items, wire.Component{
		Type:  wire.ComponentField,
		Name:  "include_review",
		Label: "同时包含 review 等级项（如 Playwright 浏览器缓存）",
		Kind:  wire.FieldKindToggle,
		Value: "false",
	})
	if hasAnyToggle {
		formSec.Items = append(formSec.Items, wire.Component{
			Type:  wire.ComponentButton,
			Title: "按勾选分类执行清理",
			Action: &wire.Action{
				ID:      "clean-selected",
				Submit:  true,
				Values:  map[string]string{"m": m.ID},
				Confirm: fmt.Sprintf("确认对「%s」按所勾选的分类执行清理并记录 df 实测台账？", m.Name),
			},
		})
		secs = append(secs, formSec)
	}

	if plan != nil && len(plan.Items) > 0 {
		var rows [][]string
		for _, it := range plan.Items {
			rows = append(rows, []string{
				it.ID,
				CategoryLabels[it.Category],
				clip(it.Path, 70),
				it.Safety,
				FormatBytes(it.Bytes),
			})
		}
		secs = append(secs, wire.Section{
			Title: "Dry-Run 计划明细清单",
			Items: []wire.Component{
				{
					Type: wire.ComponentTable,
					Columns: []wire.TableColumn{
						{Title: "计划项 ID"},
						{Title: "分类"},
						{Title: "目标路径"},
						{Title: "等级"},
						{Title: "预计释放", Align: "end"},
					},
					Rows: rows,
				},
			},
		})
	}

	return secs
}

// ---- Tab 4: Coding Agent Session Tiering & Cleanup History Ledger ----

func (d draw) sessionsAndHistoryTab(p page, m *Machine, scan *Scan) []wire.Section {
	ms := d.st.machineStatus(m)
	var secs []wire.Section

	sess := ms.Sessions
	sessSec := wire.Section{
		Title:  "Coding Agent 会话冷热分层 (Session Tiering · " + m.Name + ")",
		Footer: "热会话（7 天内活跃修改）受红线保护绝不触碰；冷会话（>7 天未修改）支持一键归档压缩至 ~/.disklessly/session-archives。",
		Items: []wire.Component{
			{
				Type: wire.ComponentStatGrid,
				Stats: []wire.Stat{
					{Label: "热会话 (≤7天)", Value: fmt.Sprintf("%d 个", sess.HotCount), Detail: FormatBytes(sess.HotBytes) + " · 活跃保护中", Tone: wire.TonePositive},
					{Label: "冷会话 (>7天)", Value: fmt.Sprintf("%d 个", sess.ColdCount), Detail: FormatBytes(sess.ColdBytes) + " · 可归档压缩", Tone: toneIf(sess.ColdBytes > 0, wire.ToneCaution)},
					{Label: "已归档包", Value: fmt.Sprintf("%d 个", sess.ArchivedCount), Detail: FormatBytes(sess.ArchivedBytes) + " · 压缩归档"},
					{Label: "归档存储", Value: "tar.gz", Detail: cmp.Or(sess.ArchiveDir, "~/.disklessly/session-archives")},
				},
			},
		},
	}

	if len(sess.Tools) > 0 {
		var rows [][]string
		for _, tl := range sess.Tools {
			rows = append(rows, []string{
				tl.Label,
				tl.Directory,
				fmt.Sprintf("%d 个 (%s)", tl.HotCount, FormatBytes(tl.HotBytes)),
				fmt.Sprintf("%d 个 (%s)", tl.ColdCount, FormatBytes(tl.ColdBytes)),
				fmt.Sprintf("%d 个 (%s)", tl.ArchivedCount, FormatBytes(tl.ArchivedBytes)),
			})
		}
		sessSec.Items = append(sessSec.Items, wire.Component{
			Type: wire.ComponentTable,
			Columns: []wire.TableColumn{
				{Title: "Agent 工具"},
				{Title: "会话目录"},
				{Title: "热会话 (≤7d)", Align: "end"},
				{Title: "冷会话 (>7d)", Align: "end"},
				{Title: "已归档", Align: "end"},
			},
			Rows: rows,
		})
	} else {
		sessSec.Items = append(sessSec.Items, wire.Component{
			Type:  wire.ComponentText,
			Style: wire.TextStyleSecondary,
			Text:  "扫描机器后将在此显示 Claude Code 与 Codex 的冷热会话体积分布。",
		})
	}

	if sess.ColdBytes > 0 {
		sessSec.Items = append(sessSec.Items, wire.Component{
			Type:  wire.ComponentButton,
			Title: fmt.Sprintf("一键归档压缩冷会话 (%d 个 · %s)", sess.ColdCount, FormatBytes(sess.ColdBytes)),
			Action: &wire.Action{
				ID:      "archive-sessions",
				Values:  map[string]string{"m": m.ID},
				Confirm: fmt.Sprintf("将「%s」上超过 7 天的 %d 个冷会话（共 %s）归档压缩到 ~/.disklessly/session-archives？7 天内热会话保持原样。", m.Name, sess.ColdCount, FormatBytes(sess.ColdBytes)),
			},
		})
	}
	secs = append(secs, sessSec)

	// Cleanup History Ledger
	histSec := wire.Section{
		Title:  "历史清理台账 (Cleanup History Log)",
		Footer: "记录每次清理前后 df 实测可用空间对比（如 5.4 GiB → 18.0 GiB (+12.6 GiB)）。",
	}
	if len(d.st.History) == 0 {
		histSec.Items = []wire.Component{
			{
				Type:   wire.ComponentEmpty,
				Icon:   "clock",
				Title:  "尚无历史清理记录",
				Detail: "在多机总览或清理计划页执行安全清理后，每次实测释放的空间对比都会记录在这里。",
			},
		}
	} else {
		var hRows [][]string
		for _, h := range d.st.History[:min(len(d.st.History), 50)] {
			catText := strings.Join(h.Categories, ", ")
			if len(h.Categories) > 2 {
				catText = fmt.Sprintf("%s 等 %d 类", strings.Join(h.Categories[:2], ", "), len(h.Categories))
			}
			hRows = append(hRows, []string{
				h.CreatedAt.In(d.loc).Format("2006-01-02 15:04"),
				h.MachineName,
				catText,
				h.Summary,
				"+" + FormatBytes(h.FreedBytes),
			})
		}
		histSec.Items = append(histSec.Items, wire.Component{
			Type: wire.ComponentTable,
			Columns: []wire.TableColumn{
				{Title: "时间"},
				{Title: "机器"},
				{Title: "清理分类"},
				{Title: "可用空间变化 (df 实测)"},
				{Title: "实测释放", Align: "end"},
			},
			Rows: hRows,
		})
		for _, h := range d.st.History[:min(len(d.st.History), 20)] {
			histSec.Items = append(histSec.Items, wire.Component{
				Type:     wire.ComponentRow,
				Key:      "hist-" + h.ID,
				Icon:     "check",
				Title:    fmt.Sprintf("%s · %s", h.MachineName, h.Summary),
				Subtitle: fmt.Sprintf("%s · 清理 %d 项（%s）", d.when(h.CreatedAt), len(h.Items), strings.Join(h.Categories, "、")),
				Badge:    &wire.Badge{Text: "+" + FormatBytes(h.FreedBytes), Tone: wire.TonePositive},
			})
		}
	}
	secs = append(secs, histSec)
	return secs
}

// ---- Machine Form (Add / Edit SSH Machine) ----

func (d draw) machineForm(p page, m *Machine) *wire.View {
	title := "添加 SSH 远程机器"
	hostVal, nameVal, userVal, portVal, idFileVal := "", "", "", "", ""
	if m != nil {
		title = "编辑机器节点 · " + m.Name
		hostVal = m.Host
		nameVal = m.Name
		userVal = m.User
		if m.Port > 0 {
			portVal = strconv.Itoa(m.Port)
		}
		idFileVal = m.IdentityFile
	}

	var items []wire.Component
	if m == nil || !m.IsLocal {
		items = append(items, wire.Component{
			Type:        wire.ComponentField,
			Name:        "host",
			Label:       "SSH Host 别名或地址 *",
			Kind:        wire.FieldKindText,
			Value:       hostVal,
			Placeholder: "例如 opc、gcp、mm-2 或 dev.example.com",
		})
	}
	items = append(items,
		wire.Component{
			Type:        wire.ComponentField,
			Name:        "name",
			Label:       "显示名称",
			Kind:        wire.FieldKindText,
			Value:       nameVal,
			Placeholder: "例如 opc (Oracle Cloud ARM) 或 gcp (主力开发机)",
		},
	)
	if m == nil || !m.IsLocal {
		items = append(items,
			wire.Component{
				Type:        wire.ComponentField,
				Name:        "user",
				Label:       "SSH 用户名（可选，默认读 ~/.ssh/config）",
				Kind:        wire.FieldKindText,
				Value:       userVal,
				Placeholder: "例如 ubuntu、opc",
			},
			wire.Component{
				Type:        wire.ComponentField,
				Name:        "port",
				Label:       "SSH 端口（可选，默认 22）",
				Kind:        wire.FieldKindNumber,
				Value:       portVal,
				Placeholder: "22",
			},
			wire.Component{
				Type:        wire.ComponentField,
				Name:        "identity_file",
				Label:       "私钥路径（可选，默认读 ~/.ssh/config）",
				Kind:        wire.FieldKindText,
				Value:       idFileVal,
				Placeholder: "~/.ssh/id_ed25519",
			},
		)
	}
	items = append(items,
		wire.Component{
			Type:   wire.ComponentButton,
			Title:  "保存机器节点",
			Action: &wire.Action{ID: "save-machine", Submit: true},
		},
		wire.Component{
			Type:   wire.ComponentButton,
			Title:  "取消",
			Action: &wire.Action{ID: "cancel"},
		},
	)
	if m != nil && !m.IsLocal {
		items = append(items, wire.Component{
			Type:  wire.ComponentButton,
			Title: "移除此机器节点",
			Action: &wire.Action{
				ID:      "delete-machine",
				Values:  map[string]string{"m": m.ID},
				Confirm: fmt.Sprintf("确认从 Disklessly 机器管理中移除「%s」？", m.Name),
			},
		})
	}
	return &wire.View{
		Title:    title,
		State:    ext.EncodeState(p),
		Sections: []wire.Section{{Items: items}},
	}
}

// ---- Overview Card ----

func (a *app) card(_ context.Context, _ *ext.UIRequest) (*wire.View, error) {
	var items []wire.Component
	err := a.db().read(func(st *State) {
		statuses := st.statuses()
		var totalReclaim uint64
		for _, ms := range statuses {
			totalReclaim += ms.ReclaimableBytes
		}
		for _, ms := range statuses[:min(len(statuses), 4)] {
			sub := "尚未扫描"
			if ms.TotalBytes > 0 {
				sub = fmt.Sprintf("已用 %.0f%%（可用 %s）· 可释放 %s",
					ms.UsedPercent, FormatBytes(ms.AvailableBytes), FormatBytes(ms.ReclaimableBytes))
			} else if ms.Machine.LastError != "" {
				sub = "离线：" + clip(ms.Machine.LastError, 40)
			}
			items = append(items, wire.Component{
				Type:     wire.ComponentRow,
				Key:      "card-" + ms.Machine.ID,
				Icon:     machineIcon(ms.Level),
				Title:    ms.Machine.Name,
				Subtitle: sub,
				Badge:    healthBadge(ms),
			})
		}
		if len(st.History) > 0 {
			last := st.History[0]
			items = append(items, wire.Component{
				Type:  wire.ComponentText,
				Style: wire.TextStyleCaption,
				Text:  fmt.Sprintf("总可释放 %s · 最近清理 %s：%s", FormatBytes(totalReclaim), last.MachineName, last.Summary),
			})
		} else if totalReclaim > 0 {
			items = append(items, wire.Component{
				Type:  wire.ComponentText,
				Style: wire.TextStyleCaption,
				Text:  fmt.Sprintf("共 %d 台机器 · 总可安全回收 %s", len(statuses), FormatBytes(totalReclaim)),
			})
		}
	})
	if len(items) == 0 {
		items = []wire.Component{{
			Type:  wire.ComponentText,
			Style: wire.TextStyleSecondary,
			Text:  "Disklessly 机器磁盘管理：探测本机与 SSH 远程机器磁盘水位、清理缓存并归档冷会话。",
		}}
	}
	return &wire.View{Sections: []wire.Section{{Items: items}}}, err
}

// ---- UI Actions ----

func (a *app) action(ctx context.Context, r *ext.UIRequest) (*wire.ActionResult, error) {
	var p page
	if err := r.DecodeState(&p); err != nil {
		return nil, &ext.Error{Code: "invalid", Message: "invalid page state"}
	}
	res := &wire.ActionResult{}
	runner := a.run()

	switch r.Action {
	case "machine":
		if r.Values["machine"] == "+" {
			p.Mode = modeNewMachine
		} else {
			p.Machine = r.Values["machine"]
			p.Mode = modeHome
		}
	case "tab":
		p.Tab = r.Values["tab"]
		p.Mode = modeHome
	case "search":
		p.Query = strings.TrimSpace(r.Values["q"])
	case "select-machine":
		p.Machine = cmp.Or(r.Values["m"], p.Machine)
		p.Tab = cmp.Or(r.Values["t"], tabItems)
		p.Mode = modeHome
	case "open-plan":
		p.Machine = cmp.Or(r.Values["m"], p.Machine)
		p.Tab = tabPlan
		p.Mode = modeHome
	case "new-machine":
		p.Mode = modeNewMachine
	case "edit-machine":
		if m := r.Values["m"]; m != "" {
			p.Machine = m
		}
		p.Mode = modeEditMach
	case "cancel":
		p.Mode = modeHome
	default:
		err := a.db().update(func(st *State, now time.Time) error {
			return a.handleStateAction(ctx, runner, st, now, r, &p, res)
		})
		if err != nil {
			return nil, &ext.Error{Code: "invalid", Message: err.Error()}
		}
	}

	var err error
	res.View, err = a.render(ctx, &ext.UIRequest{UIParams: wire.UIParams{State: ext.EncodeState(p), TZ: r.TZ}})
	return res, err
}

func (a *app) handleStateAction(
	ctx context.Context,
	runner Runner,
	st *State,
	now time.Time,
	r *ext.UIRequest,
	p *page,
	res *wire.ActionResult,
) error {
	targetID := cmp.Or(r.Values["m"], p.Machine, "local")
	switch r.Action {
	case "scan":
		m := st.machine(targetID)
		if m == nil {
			return fmt.Errorf("找不到机器 %q", targetID)
		}
		sc, err := ScanMachine(ctx, runner, st, m, now)
		if err != nil {
			return err
		}
		p.Machine = m.ID
		res.Toast = fmt.Sprintf("已扫描 %s：可回收 %s", m.Name, FormatBytes(sc.ReclaimableBytes))

	case "scan-all":
		var scanned int
		var totalReclaim uint64
		for _, m := range st.Machines {
			if !m.Enabled {
				continue
			}
			if sc, err := ScanMachine(ctx, runner, st, m, now); err == nil && sc != nil {
				scanned++
				totalReclaim += sc.ReclaimableBytes
			}
		}
		res.Toast = fmt.Sprintf("已扫描 %d 台机器，总可回收 %s", scanned, FormatBytes(totalReclaim))

	case "clean-safe":
		m := st.machine(targetID)
		if m == nil {
			return fmt.Errorf("找不到机器 %q", targetID)
		}
		_, rec, err := ExecuteClean(ctx, runner, st, m, nil, nil, false, false, now)
		if err != nil {
			return err
		}
		p.Machine = m.ID
		res.Toast = fmt.Sprintf("已清理 %s：%s", m.Name, rec.Summary)

	case "clean-category":
		m := st.machine(targetID)
		if m == nil {
			return fmt.Errorf("找不到机器 %q", targetID)
		}
		cat := strings.TrimSpace(r.Values["cat"])
		if cat == "" {
			return fmt.Errorf("未指定清理分类")
		}
		_, rec, err := ExecuteClean(ctx, runner, st, m, []string{cat}, nil, true, false, now)
		if err != nil {
			return err
		}
		p.Machine = m.ID
		res.Toast = fmt.Sprintf("已清理 %s（%s）：%s", m.Name, CategoryLabels[cat], rec.Summary)

	case "clean-item":
		m := st.machine(targetID)
		if m == nil {
			return fmt.Errorf("找不到机器 %q", targetID)
		}
		itemID := strings.TrimSpace(r.Values["item"])
		if itemID == "" {
			return fmt.Errorf("未指定清理项 ID")
		}
		_, rec, err := ExecuteClean(ctx, runner, st, m, nil, []string{itemID}, true, false, now)
		if err != nil {
			return err
		}
		p.Machine = m.ID
		res.Toast = fmt.Sprintf("已完成：%s", rec.Summary)

	case "clean-selected":
		m := st.machine(targetID)
		if m == nil {
			return fmt.Errorf("找不到机器 %q", targetID)
		}
		var cats []string
		for _, cid := range CategoryIDs {
			if r.Values["cat_"+cid] == "true" {
				cats = append(cats, cid)
			}
		}
		if len(cats) == 0 {
			return fmt.Errorf("请至少勾选一个要清理的分类")
		}
		includeRev := r.Values["include_review"] == "true"
		_, rec, err := ExecuteClean(ctx, runner, st, m, cats, nil, includeRev, false, now)
		if err != nil {
			return err
		}
		p.Machine = m.ID
		p.Tab = tabSessions
		res.Toast = fmt.Sprintf("清理完成：%s", rec.Summary)

	case "archive-sessions":
		m := st.machine(targetID)
		if m == nil {
			return fmt.Errorf("找不到机器 %q", targetID)
		}
		_, rec, err := ExecuteClean(ctx, runner, st, m, []string{CatColdAgentSession}, nil, false, false, now)
		if err != nil {
			return err
		}
		p.Machine = m.ID
		res.Toast = fmt.Sprintf("冷会话已归档：%s", rec.Summary)

	case "save-machine":
		port, _ := strconv.Atoi(strings.TrimSpace(r.Values["port"]))
		in := MachineInput{
			Host:         r.Values["host"],
			Name:         r.Values["name"],
			User:         r.Values["user"],
			Port:         port,
			IdentityFile: r.Values["identity_file"],
			Source:       "manual",
		}
		if p.Mode == modeEditMach {
			in.ID = p.Machine
			if existing := st.machine(p.Machine); existing != nil && existing.IsLocal {
				in.Host = "localhost"
			}
		}
		m, err := st.upsertMachine(in, now)
		if err != nil {
			return err
		}
		p.Machine = m.ID
		p.Mode = modeHome
		res.Toast = "已保存机器节点「" + m.Name + "」"

	case "delete-machine":
		if err := st.deleteMachine(targetID); err != nil {
			return err
		}
		p.Machine = "local"
		p.Mode = modeHome
		res.Toast = "已移除机器节点"

	default:
		return fmt.Errorf("unknown action %q", r.Action)
	}
	return nil
}

// ---- UI Helpers ----

func (d draw) machineHeaderComponent(ms MachineStatus, scan *Scan) wire.Component {
	m := ms.Machine
	b := healthBadge(ms)
	facts := []wire.Fact{
		{Label: "主机", Value: cmp.Or(m.Host, "localhost")},
		{Label: "来源", Value: m.Source},
	}
	if m.Platform != "" {
		facts = append(facts, wire.Fact{Label: "平台", Value: m.Platform})
	}
	if scan != nil && scan.Mount.TotalBytes > 0 {
		facts = append(facts,
			wire.Fact{Label: "根分区容量", Value: FormatBytes(scan.Mount.TotalBytes)},
			wire.Fact{Label: "可用空间", Value: FormatBytes(scan.Mount.AvailableBytes)},
			wire.Fact{Label: "可回收缓存", Value: FormatBytes(scan.ReclaimableBytes)},
		)
	}
	if ms.CleanCount > 0 {
		facts = append(facts, wire.Fact{Label: "累计已释放", Value: FormatBytes(ms.TotalFreedBytes)})
	}
	sub := "本机或 SSH 远程节点"
	if scan != nil {
		sub = fmt.Sprintf("挂载点 %s (%s) · 扫描模式 %s · 更新于 %s",
			scan.Mount.MountPoint, scan.Mount.FSType, scan.ScanMode, d.when(scan.ScannedAt))
	}
	var badges []wire.Badge
	if b != nil {
		badges = append(badges, *b)
	}
	return wire.Component{
		Type:     wire.ComponentHeader,
		Title:    m.Name,
		Subtitle: sub,
		Badges:   badges,
		Facts:    facts,
	}
}

func healthBadge(ms MachineStatus) *wire.Badge {
	switch ms.Level {
	case HealthOffline:
		return &wire.Badge{Text: "离线", Tone: wire.ToneCaution}
	case HealthCritical:
		return &wire.Badge{Text: fmt.Sprintf("红色告警 %.0f%%", ms.UsedPercent), Tone: wire.ToneDanger}
	case HealthWarning:
		return &wire.Badge{Text: fmt.Sprintf("橙色预警 %.0f%%", ms.UsedPercent), Tone: wire.ToneCaution}
	default:
		if ms.TotalBytes > 0 {
			return &wire.Badge{Text: fmt.Sprintf("正常 %.0f%%", ms.UsedPercent), Tone: wire.TonePositive}
		}
		return &wire.Badge{Text: "待扫描", Tone: wire.ToneNeutral}
	}
}

func progressTone(level string) string {
	switch level {
	case HealthCritical:
		return wire.ToneDanger
	case HealthWarning:
		return wire.ToneCaution
	default:
		return wire.TonePositive
	}
}

func machineIcon(level string) string {
	switch level {
	case HealthCritical, HealthWarning, HealthOffline:
		return "warning"
	default:
		return "gauge"
	}
}

func toneIf(cond bool, t string) string {
	if cond {
		return t
	}
	return ""
}

func clip(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	r := []rune(s)
	return string(r[:maxRunes-1]) + "…"
}

func (d draw) when(t time.Time) string {
	if t.IsZero() {
		return "未知"
	}
	ago := d.now.Sub(t)
	switch {
	case ago < time.Minute:
		return "刚刚"
	case ago < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(ago.Minutes()))
	case ago < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(ago.Hours()))
	case ago < 7*24*time.Hour:
		return fmt.Sprintf("%d 天前", int(ago.Hours()/24))
	}
	return t.In(d.loc).Format(time.DateOnly)
}
