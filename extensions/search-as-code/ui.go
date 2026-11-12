package main

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

const (
	pageUI       = "search-as-code"
	cardUI       = "search-as-code-card"
	maxShownHits = 25
)

const (
	modeHome       = ""
	modeHit        = "hit"
	modeRecipeEdit = "recipe-edit"
)

type page struct {
	Tab          string `json:"t,omitempty"`
	Scope        string `json:"s,omitempty"`
	Query        string `json:"q,omitempty"`
	RecipeID     string `json:"r,omitempty"`
	HitID        string `json:"h,omitempty"`
	EditRecipeID string `json:"er,omitempty"`
	Mode         string `json:"m,omitempty"`
}

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
	err := a.db().read(func(st *State) {
		v = a.drawer(st, r).view(p)
	})
	return v, err
}

func (a *app) drawer(st *State, r *ext.UIRequest) draw {
	loc, err := time.LoadLocation(r.TZ)
	if err != nil {
		loc = time.UTC
	}
	return draw{st: st, now: a.now(), loc: loc}
}

func (d draw) activeExecution(p page) *ExecutionResult {
	scope := cmp.Or(p.Scope, d.st.DefaultScope, "conversation")
	if !isValidScope(scope) {
		scope = "conversation"
	}
	if d.st.LastRun != nil && p.Query == d.st.LastRun.Program.Query &&
		(p.Scope == "" || p.Scope == d.st.LastRun.Program.Scope) &&
		(p.RecipeID == "" || p.RecipeID == d.st.LastRun.Program.RecipeID) {
		return d.st.LastRun
	}
	in := CompileInput{
		Query: p.Query,
		Scope: scope,
	}
	if p.RecipeID != "" {
		in.Recipe = d.st.recipe(p.RecipeID)
	}
	prog, err := CompileProgram(in)
	if err != nil {
		prog, _ = CompileProgram(CompileInput{Scope: scope})
	}
	return ExecuteProgram(d.st, prog, d.now)
}

func (d draw) view(p page) *wire.View {
	switch p.Mode {
	case modeHit:
		return d.hitDetailView(p)
	case modeRecipeEdit:
		return d.recipeFormView(p)
	default:
		return d.homeView(p)
	}
}

func (d draw) homeView(p page) *wire.View {
	scope := cmp.Or(p.Scope, d.st.DefaultScope, "conversation")
	if !isValidScope(scope) {
		scope = "conversation"
	}
	p.Scope = scope

	tab := cmp.Or(p.Tab, "results")
	if !slices.Contains([]string{"results", "code", "recipes", "sources"}, tab) {
		tab = "results"
	}
	p.Tab = tab

	res := d.activeExecution(p)
	recipes := d.st.allRecipes()

	v := &wire.View{
		Title: "代码化搜索",
		State: ext.EncodeState(p),
	}

	scopeChoices := make([]wire.Choice, len(scopes))
	for i, sc := range scopes {
		scopeChoices[i] = wire.Choice{Value: sc, Title: scopeLabels[sc]}
	}
	v.Filter = &wire.Filter{
		Name:    "scope",
		Title:   "检索范围",
		Choices: scopeChoices,
		Value:   scope,
		Action:  wire.Action{ID: "scope"},
	}

	v.Search = &wire.Search{
		Placeholder: "输入自然语言复杂查询、算子过滤（如 project:persona without:tools）或 |> 搜索代码…",
		Value:       p.Query,
		Action:      wire.Action{ID: "search"},
	}

	v.Tabs = &wire.Tabs{
		Name:  "tab",
		Value: tab,
		Items: []wire.Tab{
			{Value: "results", Title: "证据与结果", Badge: countBadge(res.TotalMatches)},
			{Value: "code", Title: "代码与轨迹", Badge: countBadge(len(res.StepTraces))},
			{Value: "recipes", Title: "搜索配方", Badge: countBadge(len(recipes))},
			{Value: "sources", Title: "会话与数据源"},
		},
		Action: wire.Action{ID: "tab"},
	}

	switch tab {
	case "code":
		v.Sections = d.codeAndTraceSections(p, res)
	case "recipes":
		v.Sections = d.recipesSections(p, recipes)
	case "sources":
		v.Sections = d.sourcesSections(p)
	default:
		v.Sections = d.resultsSections(p, res, recipes)
	}
	v.Sections = append(d.restoreSection(), v.Sections...)
	return v
}

func (d draw) resultsSections(p page, res *ExecutionResult, recipes []*Recipe) []wire.Section {
	var secs []wire.Section

	// 1. Execution overview + generated Search-as-Code preview
	stats := []wire.Stat{
		{Label: "命中结果", Value: strconv.Itoa(res.TotalMatches), Tone: toneIf(res.TotalMatches > 0, wire.TonePositive)},
		{Label: "扫描会话", Value: strconv.Itoa(res.ScannedSessions) + coldNote(res.ScannedArchived, res.ScannedUnconfirmed, res.ArchivedOmitted)},
		{Label: "算子步骤", Value: fmt.Sprintf("%d 步", len(res.StepTraces))},
		{Label: "执行耗时", Value: fmt.Sprintf("%d ms", res.TotalDurationMs)},
	}
	overview := wire.Section{
		Title: res.Program.Title,
		Items: []wire.Component{
			{Type: wire.ComponentStatGrid, Key: "res-stats", Stats: stats},
			{
				Type:     wire.ComponentCode,
				Key:      "res-code-preview",
				Path:     "search_program.sac",
				Language: "elixir",
				Text:     res.Program.CodeListing,
			},
		},
	}
	secs = append(secs, overview)

	// 2. Quick recipe chips/rows when no manual query is active
	if p.Query == "" {
		recSec := wire.Section{
			Title:  "常用复杂搜索配方（一键运行）",
			Footer: "每个配方都是一段可组合的多步 Search-as-Code 流水线：支持过滤工具噪音、跨会话文件关联与按项目/模型聚合。",
		}
		for _, r := range recipes[:min(len(recipes), 4)] {
			badgeText := scopeLabels[r.Scope]
			if p.RecipeID == r.ID {
				badgeText = "当前运行"
			}
			recSec.Items = append(recSec.Items, wire.Component{
				Type:     wire.ComponentRow,
				Key:      "quick-rec-" + r.ID,
				Icon:     cmp.Or(r.Icon, "code"),
				Title:    r.Name,
				Subtitle: r.Description,
				Badge:    &wire.Badge{Text: badgeText, Tone: toneIf(p.RecipeID == r.ID, wire.TonePositive)},
				Action:   &wire.Action{ID: "run-recipe", Values: map[string]string{"id": r.ID}},
			})
		}
		secs = append(secs, recSec)
	}

	// 3. Group aggregations (from group_by)
	if len(res.Groups) > 0 {
		grpStats := make([]wire.Stat, 0, min(len(res.Groups), wire.StatsMost))
		for _, g := range res.Groups[:min(len(res.Groups), wire.StatsMost)] {
			val := fmt.Sprintf("%d 条", g.Count)
			tone := wire.ToneNeutral
			if g.ErrorCount > 0 {
				val += fmt.Sprintf(" (%d 异常)", g.ErrorCount)
				tone = wire.ToneCaution
			}
			grpStats = append(grpStats, wire.Stat{
				Label: clipRunes(g.Key, 20),
				Value: val,
				Tone:  tone,
			})
		}
		secs = append(secs, wire.Section{
			Title: fmt.Sprintf("聚合分布（group_by %s）", res.Groups[0].Field),
			Items: []wire.Component{{Type: wire.ComponentStatGrid, Key: "grp-stats", Stats: grpStats}},
		})
	}

	// 4. Ranked Evidence Hits
	if len(res.Hits) == 0 {
		secs = append(secs, wire.Section{
			Title: "匹配结果",
			Items: []wire.Component{{
				Type:   wire.ComponentEmpty,
				Icon:   "search",
				Title:  "没有匹配的会话或代码片段",
				Detail: "试试切换顶部「检索范围」（如切到「全部」或「纯对话」），调整关键词，或在「会话与数据源」中检查扫描目录。",
				Button: &wire.EmptyButton{
					Title:  "重置搜索条件",
					Action: wire.Action{ID: "reset"},
				},
			}},
		})
		return secs
	}

	hitSec := wire.Section{
		Title: fmt.Sprintf("结构化证据卡片 · %d 条", len(res.Hits)),
	}
	shownHits := res.Hits[:min(len(res.Hits), maxShownHits)]
	for i, h := range shownHits {
		icon := "chat"
		if h.SourceKind == "file" {
			icon = "file"
		} else if h.ErrorCount > 0 {
			icon = "warning"
		}
		metaParts := []string{cmp.Or(h.Project, "workspace"), h.Harness}
		if h.Model != "" {
			metaParts = append(metaParts, h.Model)
		}
		if h.Branch != "" {
			metaParts = append(metaParts, "⎇ "+h.Branch)
		}
		if h.TurnCount > 0 {
			metaParts = append(metaParts, fmt.Sprintf("%d 轮对话", h.TurnCount))
		}
		archived := h.Location == locationArchived
		cold := archived || h.Location == locationUnconfirmed
		if cold && h.ErrorCount > 0 {
			metaParts = append(metaParts, fmt.Sprintf("%d 异常", h.ErrorCount))
		}
		badge := &wire.Badge{
			Text: fmt.Sprintf("%.1f 分", h.Score),
			Tone: wire.ToneNeutral,
		}
		if h.ErrorCount > 0 {
			badge = &wire.Badge{
				Text: fmt.Sprintf("%d 异常", h.ErrorCount),
				Tone: wire.ToneDanger,
			}
		}
		if cold {
			icon = "cloud"
			badge = &wire.Badge{Text: h.Location, Tone: wire.ToneCaution}
		}
		restoring := archived && d.st.restoreRunning(h.EntryID)
		if restoring {
			badge = &wire.Badge{Text: "恢复中", Tone: wire.ToneCaution}
		}
		hitPage := p
		hitPage.Mode = modeHit
		hitPage.HitID = h.ID
		row := wire.Component{
			Type:     wire.ComponentRow,
			Key:      fmt.Sprintf("hit-row-%d", i),
			Icon:     icon,
			Title:    clipRunes(h.Title, 120),
			Subtitle: strings.Join(metaParts, " · "),
			Value:    d.formatTime(h.UpdatedAt),
			Badge:    badge,
			Action:   pushPage(hitPage, h.Title),
		}
		if archived && h.EntryID != "" && !restoring {
			row.Actions = []wire.RowAction{{Title: "恢复", Icon: "refresh", Swipe: true, Action: *restoreAction(h.EntryID, h.OriginalPath, h.Harness, h.SessionID)}}
		}
		hitSec.Items = append(hitSec.Items, row)
		for j, sn := range h.Snippets[:min(len(h.Snippets), 2)] {
			prefix := fmt.Sprintf("[%s", sn.Role)
			if sn.Line > 0 {
				prefix += fmt.Sprintf(" L%d", sn.Line)
			}
			prefix += "] "
			hitSec.Items = append(hitSec.Items, wire.Component{
				Type:  wire.ComponentText,
				Key:   fmt.Sprintf("hit-snip-%d-%d", i, j),
				Style: wire.TextStyleSecondary,
				Text:  prefix + sn.Text,
			})
		}
		if len(h.Correlated) > 0 {
			var corrNames []string
			for _, cr := range h.Correlated[:min(len(h.Correlated), 2)] {
				corrNames = append(corrNames, fmt.Sprintf("%s（%s）", clipRunes(cr.Title, 28), cr.Reason))
			}
			hitSec.Items = append(hitSec.Items, wire.Component{
				Type:  wire.ComponentText,
				Key:   fmt.Sprintf("hit-corr-%d", i),
				Style: wire.TextStyleCaption,
				Text:  "关联上下文：" + strings.Join(corrNames, " ； "),
			})
		}
	}
	if len(res.Hits) > maxShownHits {
		hitSec.Footer = fmt.Sprintf("显示前 %d 条最高分证据（共 %d 条）。可在搜索框或流水线中使用 limit(n=...) 调整。", maxShownHits, len(res.Hits))
	}
	secs = append(secs, hitSec)
	return secs
}

func (d draw) codeAndTraceSections(p page, res *ExecutionResult) []wire.Section {
	var secs []wire.Section

	// 1. Step Execution Trace Table
	cols := []wire.TableColumn{
		{Title: "#"},
		{Title: "算子调用"},
		{Title: "输入 → 输出", Align: "end"},
		{Title: "耗时", Align: "end"},
		{Title: "执行轨迹说明"},
	}
	rows := make([][]string, 0, len(res.StepTraces))
	for _, tr := range res.StepTraces {
		durStr := fmt.Sprintf("%d µs", tr.DurationUs)
		if tr.DurationMs >= 1 {
			durStr = fmt.Sprintf("%d ms", tr.DurationMs)
		}
		rows = append(rows, []string{
			strconv.Itoa(tr.Index),
			clipRunes(tr.Code, 80),
			fmt.Sprintf("%d → %d", tr.InputCount, tr.OutputCount),
			durStr,
			clipRunes(tr.Note, 80),
		})
	}
	secs = append(secs, wire.Section{
		Title:  "算子执行轨迹（Step Trace）",
		Footer: "每一步算子在本地沙箱中顺序流式执行，记录输入集规模、过滤后输出集规模与单步耗时。",
		Items: []wire.Component{
			{
				Type:     wire.ComponentCode,
				Key:      "trace-code-full",
				Path:     "search_program.sac",
				Language: "elixir",
				Text:     res.Program.CodeListing,
			},
			{
				Type:    wire.ComponentTable,
				Key:     "trace-table",
				Columns: cols,
				Rows:    rows,
			},
		},
	})

	// 2. Interactive Search-as-Code Pipeline Runner & Recipe Saver
	secs = append(secs, wire.Section{
		Title:  "编写或调整 Search-as-Code 流水线",
		Footer: "支持算子：scan_sessions、scan_files、where、exclude_tools、match_regex、correlate、dedup、group_by、rank、limit，使用 |> 串联。",
		Items: []wire.Component{
			{
				Type:        wire.ComponentField,
				Name:        "custom_code",
				Label:       "Search-as-Code 程序代码",
				Kind:        wire.FieldKindText,
				Value:       res.Program.CodeListing,
				Placeholder: `scan_sessions(query="timeout", scope="conversation") |> exclude_tools() |> correlate(by="file") |> group_by(field="project") |> rank(by="relevance_freshness") |> limit(n=20)`,
			},
			{
				Type:   wire.ComponentButton,
				Title:  "运行此 Search-as-Code 程序",
				Action: &wire.Action{ID: "run-code", Submit: true},
			},
			{
				Type:   wire.ComponentButton,
				Title:  "保存为可复用搜索配方",
				Action: pushPage(page{Scope: p.Scope, Query: res.Program.CodeListing, Mode: modeRecipeEdit}, "保存搜索配方"),
			},
		},
	})

	// 3. Recent Runs History
	if len(d.st.History) > 0 {
		histSec := wire.Section{Title: "最近执行的搜索程序"}
		for i, h := range d.st.History[:min(len(d.st.History), 8)] {
			histSec.Items = append(histSec.Items, wire.Component{
				Type:     wire.ComponentRow,
				Key:      fmt.Sprintf("hist-%d", i),
				Icon:     "clock",
				Title:    clipRunes(h.Title, 80),
				Subtitle: fmt.Sprintf("%s · %d 步算子 · %d 条命中 · %d ms", scopeLabels[h.Scope], h.StepCount, h.TotalMatches, h.TotalDurationMs),
				Value:    d.formatTime(h.RanAt),
				Action:   &wire.Action{ID: "rerun-history", Values: map[string]string{"i": strconv.Itoa(i)}},
			})
		}
		secs = append(secs, histSec)
	}

	return secs
}

func (d draw) recipesSections(p page, recipes []*Recipe) []wire.Section {
	listSec := wire.Section{
		Title:  "已保存的复杂搜索代码配方",
		Footer: "点按任意配方即可运行对应的多步 Search-as-Code 程序；也可以新建或编辑自定义配方。",
	}
	for _, r := range recipes {
		tag := "自定义配方"
		if r.Builtin {
			tag = "预置配方"
		}
		listSec.Items = append(listSec.Items,
			wire.Component{
				Type:     wire.ComponentRow,
				Key:      "rec-row-" + r.ID,
				Icon:     cmp.Or(r.Icon, "code"),
				Title:    r.Name,
				Subtitle: fmt.Sprintf("%s · 默认范围：%s · %s", r.ID, scopeLabels[r.Scope], r.Description),
				Badge:    &wire.Badge{Text: tag, Tone: toneIf(r.Builtin, wire.TonePositive)},
				Action:   &wire.Action{ID: "run-recipe", Values: map[string]string{"id": r.ID}},
			},
			wire.Component{
				Type:     wire.ComponentCode,
				Key:      "rec-code-" + r.ID,
				Path:     r.ID + ".sac",
				Language: "elixir",
				Text:     r.Code,
			},
			wire.Component{
				Type:  wire.ComponentButton,
				Key:   "rec-edit-" + r.ID,
				Title: "编辑配方「" + r.Name + "」",
				Action: pushPage(page{
					Tab:          "recipes",
					Scope:        p.Scope,
					EditRecipeID: r.ID,
					Mode:         modeRecipeEdit,
				}, "编辑配方"),
			},
		)
	}
	listSec.Items = append(listSec.Items, wire.Component{
		Type:   wire.ComponentButton,
		Key:    "btn-new-recipe",
		Title:  "＋ 新建 Search-as-Code 配方",
		Action: pushPage(page{Tab: "recipes", Scope: p.Scope, Mode: modeRecipeEdit}, "新建搜索配方"),
	})
	return []wire.Section{listSec}
}

func (d draw) sourcesSections(p page) []wire.Section {
	_, stats := loadSessions(d.st)

	statItems := []wire.Stat{
		{Label: "已索引会话", Value: strconv.Itoa(stats.TotalSessions), Tone: toneIf(stats.TotalSessions > 0, wire.TonePositive)},
		{Label: "对话与事件轮次", Value: strconv.Itoa(stats.TotalTurns)},
		{Label: "含异常会话", Value: strconv.Itoa(stats.ErrorSessions), Tone: toneIf(stats.ErrorSessions > 0, wire.ToneCaution)},
		{Label: "会话源目录数", Value: strconv.Itoa(len(stats.SessionDirs))},
		{Label: locationArchived + "会话", Value: strconv.Itoa(stats.ArchivedSessions)},
	}
	if stats.UnconfirmedSessions > 0 {
		statItems = append(statItems, wire.Stat{Label: locationUnconfirmed, Value: strconv.Itoa(stats.UnconfirmedSessions), Tone: wire.ToneCaution})
	}
	if stats.ArchivedOmitted > 0 {
		statItems = append(statItems, wire.Stat{Label: "超出上限未扫描", Value: strconv.Itoa(stats.ArchivedOmitted), Tone: wire.ToneCaution})
	}

	overview := wire.Section{
		Title: "本机 Coding Agent 会话与代码库索引状态",
		Items: []wire.Component{
			{Type: wire.ComponentStatGrid, Key: "src-stats", Stats: statItems},
		},
	}
	for i, dir := range stats.SessionDirs {
		overview.Items = append(overview.Items, wire.Component{
			Type:     wire.ComponentRow,
			Key:      fmt.Sprintf("sdir-%d", i),
			Icon:     "folder",
			Title:    shortenHome(dir),
			Subtitle: "Agent 会话日志目录 (*.jsonl)",
			Badge:    &wire.Badge{Text: "CASS 扫描源"},
		})
	}
	for i, dir := range stats.ColdTextDirs {
		overview.Items = append(overview.Items, wire.Component{
			Type:     wire.ComponentRow,
			Key:      fmt.Sprintf("cdir-%d", i),
			Icon:     "cloud",
			Title:    shortenHome(dir),
			Subtitle: "disklessly 已上云会话的本机纯文本索引 (*.jsonl)",
			Badge:    &wire.Badge{Text: locationArchived},
		})
	}
	for i, dir := range stats.RepoDirs {
		overview.Items = append(overview.Items, wire.Component{
			Type:     wire.ComponentRow,
			Key:      fmt.Sprintf("rdir-%d", i),
			Icon:     "code",
			Title:    shortenHome(dir),
			Subtitle: "本地代码与文档仓库目录 (scan_files)",
			Badge:    &wire.Badge{Text: "代码源"},
		})
	}

	// Breakdown by Harness and Model
	var distRows [][]string
	for h, cnt := range stats.ByHarness {
		distRows = append(distRows, []string{"Harness", h, strconv.Itoa(cnt)})
	}
	for m, cnt := range stats.ByModel {
		distRows = append(distRows, []string{"Model", m, strconv.Itoa(cnt)})
	}
	for proj, cnt := range stats.ByProject {
		distRows = append(distRows, []string{"Project", proj, strconv.Itoa(cnt)})
	}
	slices.SortFunc(distRows, func(a, b []string) int {
		if c := cmp.Compare(a[0], b[0]); c != 0 {
			return c
		}
		return cmp.Compare(a[1], b[1])
	})

	secs := []wire.Section{overview}
	if len(distRows) > 0 {
		secs = append(secs, wire.Section{
			Title: "Harness / 模型 / 项目分布",
			Items: []wire.Component{{
				Type:    wire.ComponentTable,
				Key:     "src-dist-table",
				Columns: []wire.TableColumn{{Title: "维度"}, {Title: "名称"}, {Title: "会话数", Align: "end"}},
				Rows:    distRows[:min(len(distRows), 20)],
			}},
		})
	}

	cfgSec := wire.Section{
		Title:  "配置检索目录",
		Footer: "默认自动扫描 ~/.claude/projects、~/.codex/sessions 与 disklessly 的已上云会话索引（<状态目录>/sessions/text）；你可以在此附加自定义会话目录、索引目录或本地代码仓库目录。",
		Items: []wire.Component{
			{
				Type:        wire.ComponentField,
				Name:        "session_dirs",
				Label:       "额外会话目录（用逗号或分号分隔）",
				Kind:        wire.FieldKindText,
				Value:       shortenHomeList(d.st.SessionDirs),
				Placeholder: "~/.claude/projects, ~/.codex/sessions",
			},
			{
				Type:        wire.ComponentField,
				Name:        "repo_dirs",
				Label:       "本地代码与文档目录（供 scan_files 检索）",
				Kind:        wire.FieldKindText,
				Value:       shortenHomeList(d.st.RepoDirs),
				Placeholder: "~/workspace/persona-hub",
			},
			{
				Type:        wire.ComponentField,
				Name:        "cold_text_dirs",
				Label:       "额外已上云会话索引目录（disklessly 的 sessions/text，用逗号或分号分隔）",
				Kind:        wire.FieldKindText,
				Value:       shortenHomeList(d.st.ColdTextDirs),
				Placeholder: "~/.local/share/disklessly/sessions/text",
			},
			{
				Type:   wire.ComponentButton,
				Title:  "保存检索目录设置",
				Action: &wire.Action{ID: "save-sources", Submit: true},
			},
		},
	}
	secs = append(secs, cfgSec)
	return secs
}

func (d draw) hitDetailView(p page) *wire.View {
	res := d.activeExecution(p)
	var hit *SearchHit
	for _, h := range res.Hits {
		if h.ID == p.HitID {
			hit = h
			break
		}
	}
	sessions, _ := loadSessions(d.st)
	var sess *SessionRecord
	for _, s := range sessions {
		if "session:"+s.SessionID == p.HitID || s.SessionID == strings.TrimPrefix(p.HitID, "session:") {
			sess = s
			break
		}
	}
	if hit == nil && sess == nil {
		return &wire.View{
			Title: "证据详情",
			State: ext.EncodeState(p),
			Sections: []wire.Section{{Items: []wire.Component{{
				Type:   wire.ComponentEmpty,
				Icon:   "search",
				Title:  "未找到该条命中记录",
				Detail: "返回上一页重新执行搜索。",
			}}}},
		}
	}

	title := ""
	project := ""
	harness := ""
	model := ""
	branch := ""
	cwd := ""
	updatedAt := d.now
	var files []string
	var correlated []CorrelatedRef
	var snippets []EvidenceSnippet

	if hit != nil {
		title = hit.Title
		project = hit.Project
		harness = hit.Harness
		model = hit.Model
		branch = hit.Branch
		cwd = hit.CWD
		updatedAt = hit.UpdatedAt
		files = hit.Files
		correlated = hit.Correlated
		snippets = hit.Snippets
	}
	if sess != nil {
		if title == "" {
			title = sess.Title
		}
		project = cmp.Or(project, sess.Project)
		harness = cmp.Or(harness, sess.Harness)
		model = cmp.Or(model, sess.Model)
		branch = cmp.Or(branch, sess.Branch)
		cwd = cmp.Or(cwd, sess.CWD)
		updatedAt = sess.UpdatedAt
		if len(files) == 0 {
			files = sess.Files
		}
	}

	head := wire.Component{
		Type:     wire.ComponentHeader,
		Title:    clipRunes(title, 160),
		Subtitle: fmt.Sprintf("%s · %s · 更新于 %s", cmp.Or(project, "workspace"), harness, d.formatTime(updatedAt)),
		Facts: []wire.Fact{
			{Label: "项目", Value: cmp.Or(project, "workspace")},
			{Label: "Harness", Value: cmp.Or(harness, "claude-code")},
			{Label: "模型", Value: cmp.Or(model, "default")},
		},
	}
	if branch != "" {
		head.Facts = append(head.Facts, wire.Fact{Label: "分支", Value: branch})
	}
	if sess != nil {
		head.Facts = append(head.Facts,
			wire.Fact{Label: "对话轮次", Value: strconv.Itoa(sess.TurnCount)},
			wire.Fact{Label: "工具调用", Value: strconv.Itoa(sess.ToolCalls)},
		)
		if sess.ErrorCount > 0 {
			head.Badges = []wire.Badge{{Text: fmt.Sprintf("%d 处异常", sess.ErrorCount), Tone: wire.ToneDanger}}
		}
		if sess.Archived {
			head.Badges = append(head.Badges, wire.Badge{Text: sessionLocation(sess), Tone: wire.ToneCaution})
			head.Facts = append(head.Facts, wire.Fact{Label: "原路径", Value: cmp.Or(sess.FilePath, "未知")})
			if !sess.ArchivedAt.IsZero() {
				head.Facts = append(head.Facts, wire.Fact{Label: "上云时间", Value: d.formatTime(sess.ArchivedAt)})
			}
		}
	}

	secs := []wire.Section{{Items: []wire.Component{head}}}
	switch {
	case sess != nil && sess.Archived && sess.Unconfirmed:
		secs = append(secs, wire.Section{
			Title: "此会话" + locationUnconfirmed,
			Items: []wire.Component{{
				Type: wire.ComponentText,
				Key:  "unconfirmed",
				Text: "本机有 disklessly 写下的纯文本索引，但上传没有完成（索引里没有云端分段与上云时间），云端可能没有这个会话的副本，因此不提供恢复。可在终端运行 disklessly sessions status 查看。",
			}},
		})
	case sess != nil && sess.Archived:
		sec := wire.Section{
			Title:  "此会话已上云",
			Footer: "本机原文件已由 disklessly 移到云端，这里检索的是本机保存的纯文本索引（仅用户与助手消息）。恢复后原文件回到原路径，可继续会话。",
		}
		if d.st.restoreRunning(sess.EntryID) {
			sec.Items = []wire.Component{{Type: wire.ComponentText, Key: "restoring", Text: "正在恢复，完成后在首页显示结果。"}}
		} else {
			sec.Items = []wire.Component{{
				Type:   wire.ComponentButton,
				Key:    "restore-session",
				Title:  "恢复",
				Action: restoreAction(sess.EntryID, sess.FilePath, sess.Harness, sess.SessionID),
			}}
		}
		secs = append(secs, sec)
	}

	if len(snippets) > 0 {
		snipSec := wire.Section{Title: "命中证据片段（带角色与行号归属）"}
		for i, sn := range snippets {
			snipSec.Items = append(snipSec.Items, wire.Component{
				Type:     wire.ComponentRow,
				Key:      fmt.Sprintf("det-snip-%d", i),
				Icon:     roleIcon(sn.Role),
				Title:    fmt.Sprintf("%s · 第 %d 行", sn.Role, max(sn.Line, 1)),
				Subtitle: sn.Text,
			})
		}
		secs = append(secs, snipSec)
	}

	if sess != nil && len(sess.Turns) > 0 {
		turnSec := wire.Section{
			Title:  "会话上下文时间线（Without tools 降噪视图）",
			Footer: "默认过滤繁杂的中间工具调用 JSON，保留 User 提问、Assistant 核心回答与 Error 异常现场。",
		}
		shown := 0
		for i, tr := range sess.Turns {
			if tr.IsTool && !tr.IsError {
				continue
			}
			sn := toSnippet(tr)
			turnSec.Items = append(turnSec.Items, wire.Component{
				Type:     wire.ComponentRow,
				Key:      fmt.Sprintf("turn-%d", i),
				Icon:     roleIcon(sn.Role),
				Title:    fmt.Sprintf("%s (L%d)", sn.Role, tr.Line),
				Subtitle: clipRunes(tr.Text, 300),
				Badge:    badgeIfError(tr.IsError, tr.ErrorKind),
			})
			if shown++; shown >= 20 {
				break
			}
		}
		if len(turnSec.Items) > 0 {
			secs = append(secs, turnSec)
		}
	}

	if len(files) > 0 {
		fileSec := wire.Section{
			Title:  "涉及的代码文件（点击跨会话溯源该文件）",
			Footer: "点按任意文件名即可生成并执行针对该文件的跨会话 Search-as-Code 检索。",
		}
		for i, fp := range files[:min(len(files), 10)] {
			fileSec.Items = append(fileSec.Items, wire.Component{
				Type:     wire.ComponentRow,
				Key:      fmt.Sprintf("file-%d", i),
				Icon:     "file",
				Title:    fp,
				Subtitle: "点按检索所有修改或讨论过该文件的会话",
				Action:   &wire.Action{ID: "pivot-query", Values: map[string]string{"q": filepath.Base(fp)}},
			})
		}
		secs = append(secs, fileSec)
	}

	if len(correlated) > 0 {
		corrSec := wire.Section{Title: "跨会话关联（correlate 算子发现）"}
		for i, cr := range correlated {
			corrPage := p
			corrPage.HitID = cr.ID
			corrSec.Items = append(corrSec.Items, wire.Component{
				Type:     wire.ComponentRow,
				Key:      fmt.Sprintf("corr-%d", i),
				Icon:     "branch",
				Title:    clipRunes(cr.Title, 100),
				Subtitle: cr.Reason,
				Action:   pushPage(corrPage, cr.Title),
			})
		}
		secs = append(secs, corrSec)
	}

	return &wire.View{
		Title:    clipRunes(title, 80),
		State:    ext.EncodeState(p),
		Sections: secs,
	}
}

func (d draw) recipeFormView(p page) *wire.View {
	var existing *Recipe
	if p.EditRecipeID != "" {
		existing = d.st.recipe(p.EditRecipeID)
	}
	idVal := ""
	nameVal := ""
	descVal := ""
	scopeVal := cmp.Or(p.Scope, "conversation")
	codeVal := strings.TrimSpace(p.Query)
	if codeVal == "" || !looksLikePipelineCode(codeVal) {
		codeVal = "scan_sessions(query=\"\", scope=\"conversation\")\n|> exclude_tools()\n|> correlate(by=\"file\")\n|> group_by(field=\"project\")\n|> rank(by=\"relevance_freshness\")\n|> limit(n=25)"
	}
	title := "新建搜索配方"
	if existing != nil {
		idVal = existing.ID
		nameVal = existing.Name
		descVal = existing.Description
		scopeVal = existing.Scope
		codeVal = existing.Code
		title = "编辑配方 · " + existing.Name
	}

	scopeChoices := make([]wire.Choice, len(scopes))
	for i, sc := range scopes {
		scopeChoices[i] = wire.Choice{Value: sc, Title: scopeLabels[sc]}
	}

	items := []wire.Component{
		{
			Type:        wire.ComponentField,
			Name:        "id",
			Label:       "配方 ID（小写字母、数字、连字符）",
			Kind:        wire.FieldKindText,
			Value:       idVal,
			Placeholder: "例如 quota-debugging",
		},
		{
			Type:        wire.ComponentField,
			Name:        "name",
			Label:       "名称 *",
			Kind:        wire.FieldKindText,
			Value:       nameVal,
			Placeholder: "例如 配额与网关报错排查",
		},
		{
			Type:        wire.ComponentField,
			Name:        "description",
			Label:       "说明",
			Kind:        wire.FieldKindText,
			Value:       descVal,
			Placeholder: "一句话说明该搜索配方的用途",
		},
		{
			Type:    wire.ComponentField,
			Name:    "recipe_scope",
			Label:   "默认检索范围",
			Kind:    wire.FieldKindChoice,
			Choices: scopeChoices,
			Value:   scopeVal,
		},
		{
			Type:        wire.ComponentField,
			Name:        "code",
			Label:       "Search-as-Code 流水线代码 *",
			Kind:        wire.FieldKindText,
			Value:       codeVal,
			Placeholder: `scan_sessions(query="", scope="conversation") |> exclude_tools() |> limit(n=25)`,
		},
		{
			Type:   wire.ComponentButton,
			Title:  "保存配方",
			Action: &wire.Action{ID: "save-recipe", Submit: true},
		},
	}

	if existing != nil && !existing.Builtin {
		items = append(items, wire.Component{
			Type:  wire.ComponentButton,
			Title: "删除此配方",
			Role:  wire.ButtonRoleDestructive,
			Action: &wire.Action{
				ID:      "delete-recipe",
				Values:  map[string]string{"id": existing.ID},
				Confirm: "确认删除搜索配方「" + existing.Name + "」？",
			},
		})
	}

	return &wire.View{
		Title:    title,
		State:    ext.EncodeState(p),
		Sections: []wire.Section{{Title: "Search-as-Code 配方定义", Items: items}},
	}
}

func (a *app) action(ctx context.Context, r *ext.UIRequest) (*wire.ActionResult, error) {
	var p page
	if err := r.DecodeState(&p); err != nil {
		return nil, &ext.Error{Code: "invalid", Message: "invalid page state"}
	}
	res := &wire.ActionResult{}

	if r.Action == "restore-session" {
		return a.runRestore(ctx, r, p)
	}

	err := a.db().update(func(st *State, now time.Time) error {
		switch r.Action {
		case "scope":
			sc := strings.TrimSpace(r.Values["scope"])
			if !isValidScope(sc) {
				return fmt.Errorf("无效的检索范围 %q", sc)
			}
			p.Scope = sc
			p.RecipeID = ""
			prog, err := CompileProgram(CompileInput{Query: p.Query, Scope: p.Scope})
			if err != nil {
				return err
			}
			st.recordRun(ExecuteProgram(st, prog, now), now)

		case "clear-restores":
			st.Restores = slices.DeleteFunc(st.Restores, func(n RestoreNotice) bool { return !n.Running })

		case "tab":
			p.Tab = strings.TrimSpace(r.Values["tab"])
			if q, ok := r.Values["q"]; ok {
				p.Query = strings.TrimSpace(q)
			}

		case "search":
			p.Query = strings.TrimSpace(r.Values["q"])
			p.RecipeID = ""
			p.Tab = "results"
			prog, err := CompileProgram(CompileInput{Query: p.Query, Scope: cmp.Or(p.Scope, st.DefaultScope)})
			if err != nil {
				return err
			}
			p.Scope = prog.Scope
			st.recordRun(ExecuteProgram(st, prog, now), now)

		case "reset":
			p.Query = ""
			p.RecipeID = ""
			p.Scope = cmp.Or(st.DefaultScope, "conversation")
			p.Tab = "results"
			prog, _ := CompileProgram(CompileInput{Scope: p.Scope})
			st.recordRun(ExecuteProgram(st, prog, now), now)

		case "run-recipe":
			id := strings.TrimSpace(r.Values["id"])
			rec := st.recipe(id)
			if rec == nil {
				return fmt.Errorf("找不到搜索配方 %q", id)
			}
			p.RecipeID = rec.ID
			p.Scope = rec.Scope
			p.Tab = "results"
			prog, err := CompileProgram(CompileInput{Recipe: rec, Query: p.Query, Scope: rec.Scope})
			if err != nil {
				return err
			}
			st.recordRun(ExecuteProgram(st, prog, now), now)
			res.Toast = "已执行配方「" + rec.Name + "」"

		case "run-code":
			code := strings.TrimSpace(r.Values["custom_code"])
			prog, err := CompileProgram(CompileInput{Code: code, Scope: cmp.Or(p.Scope, st.DefaultScope)})
			if err != nil {
				return err
			}
			p.Query = prog.CodeListing
			p.Scope = prog.Scope
			p.RecipeID = ""
			p.Tab = "results"
			execRes := ExecuteProgram(st, prog, now)
			st.recordRun(execRes, now)
			res.Toast = fmt.Sprintf("执行完成：%d 步算子，命中 %d 条", len(execRes.StepTraces), execRes.TotalMatches)

		case "rerun-history":
			idx, err := strconv.Atoi(r.Values["i"])
			if err != nil || idx < 0 || idx >= len(st.History) {
				return fmt.Errorf("无效的历史记录索引")
			}
			h := st.History[idx]
			prog, err := CompileProgram(CompileInput{Code: h.Code, Scope: h.Scope})
			if err != nil {
				return err
			}
			p.Query = h.Query
			p.Scope = h.Scope
			p.RecipeID = h.RecipeID
			p.Tab = "results"
			st.recordRun(ExecuteProgram(st, prog, now), now)
			res.Toast = "已重跑历史搜索程序"

		case "pivot-query":
			q := strings.TrimSpace(r.Values["q"])
			p.Mode = modeHome
			p.HitID = ""
			p.Query = q
			p.RecipeID = ""
			p.Tab = "results"
			prog, err := CompileProgram(CompileInput{Query: q, Scope: cmp.Or(p.Scope, st.DefaultScope)})
			if err != nil {
				return err
			}
			st.recordRun(ExecuteProgram(st, prog, now), now)
			res.Pop = true
			res.Toast = "已关联检索：" + q

		case "save-recipe":
			rec, err := st.upsertRecipe(RecipeInput{
				ID:          cmp.Or(strings.TrimSpace(r.Values["id"]), p.EditRecipeID),
				Name:        r.Values["name"],
				Description: r.Values["description"],
				Scope:       r.Values["recipe_scope"],
				Code:        r.Values["code"],
			}, now)
			if err != nil {
				return err
			}
			p.Mode = modeHome
			p.EditRecipeID = ""
			p.Tab = "recipes"
			res.Pop = true
			res.Toast = "已保存配方「" + rec.Name + "」"

		case "delete-recipe":
			id := cmp.Or(strings.TrimSpace(r.Values["id"]), p.EditRecipeID)
			if err := st.deleteRecipe(id); err != nil {
				return err
			}
			p.Mode = modeHome
			p.EditRecipeID = ""
			p.Tab = "recipes"
			res.Pop = true
			res.Toast = "已删除配方"

		case "save-sources":
			st.SessionDirs = splitDirs(r.Values["session_dirs"])
			st.RepoDirs = splitDirs(r.Values["repo_dirs"])
			st.ColdTextDirs = splitDirs(r.Values["cold_text_dirs"])
			res.Toast = "已更新检索目录"

		case "open":
			// Handled by Push on client

		default:
			return fmt.Errorf("unknown action %q", r.Action)
		}
		return nil
	})
	if err != nil {
		return nil, &ext.Error{Code: "invalid", Message: err.Error()}
	}
	if res.Pop {
		return res, nil
	}
	res.View, err = a.render(ctx, &ext.UIRequest{UIParams: wire.UIParams{State: ext.EncodeState(p), TZ: r.TZ}})
	return res, err
}

func (a *app) card(_ context.Context, r *ext.UIRequest) (*wire.View, error) {
	var items []wire.Component
	err := a.db().read(func(st *State) {
		d := a.drawer(st, r)
		res := d.activeExecution(page{})
		for i, rec := range st.allRecipes()[:min(len(st.allRecipes()), 3)] {
			items = append(items, wire.Component{
				Type:     wire.ComponentRow,
				Key:      fmt.Sprintf("card-rec-%d", i),
				Icon:     cmp.Or(rec.Icon, "code"),
				Title:    rec.Name,
				Subtitle: fmt.Sprintf("%s · %s", scopeLabels[rec.Scope], clipRunes(rec.Description, 44)),
			})
		}
		if res != nil && res.ScannedSessions > 0 {
			items = append(items, wire.Component{
				Type:  wire.ComponentText,
				Key:   "card-summary",
				Style: wire.TextStyleCaption,
				Text:  fmt.Sprintf("已索引 %d 个本机 Agent 会话%s · 最近流水线命中 %d 条（%d ms）", res.ScannedSessions, archivedNote(res.ScannedArchived), res.TotalMatches, res.TotalDurationMs),
			})
		}
	})
	return &wire.View{Sections: []wire.Section{{Items: items}}}, err
}

func restoreAction(entryID, path, harness, sessionID string) *wire.Action {
	return &wire.Action{
		ID:      "restore-session",
		Values:  map[string]string{"entry": entryID},
		Confirm: restoreConfirm(path, harness, sessionID),
	}
}

// runRestore starts restoring an archived session through the disklessly CLI in the background and returns at
// once: a restore can take minutes, longer than the hub waits for a page action. The outcome is stored in the
// state (which refreshes the page), and a successful restore re-runs the last search so the hit shows as local.
func (a *app) runRestore(ctx context.Context, r *ext.UIRequest, p page) (*wire.ActionResult, error) {
	entryID := strings.TrimSpace(r.Values["entry"])
	res := &wire.ActionResult{}
	var started bool
	var dirs []string
	err := a.db().update(func(st *State, now time.Time) error {
		if st.restoreRunning(entryID) {
			res.Toast = errRestoreBusy.Error()
			return nil
		}
		rec := findArchivedSession(st, entryID)
		if rec == nil {
			res.Toast = fmt.Sprintf("本机没有已上云会话 %s（可能已恢复，或来自其他设备）", entryID)
			return nil
		}
		st.setRestore(RestoreNotice{EntryID: entryID, Title: rec.Title, Path: rec.FilePath, Running: true, StartedAt: now.UTC()})
		dirs, started = slices.Clone(st.ColdTextDirs), true
		res.Toast = "正在从云端恢复到 " + rec.FilePath + "，完成后在本页显示结果"
		return nil
	})
	if err != nil {
		return nil, err
	}
	if started {
		db := a.db()
		a.restores.Add(1)
		go func() {
			defer a.restores.Done()
			out, rerr := restoreArchived(context.Background(), &State{ColdTextDirs: dirs}, entryID, db.dir)
			_ = db.update(func(st *State, now time.Time) error {
				n := RestoreNotice{EntryID: entryID, StartedAt: now.UTC(), At: now.UTC()}
				for _, o := range st.Restores {
					if o.EntryID == entryID {
						n.Title, n.Path, n.StartedAt = o.Title, o.Path, o.StartedAt
					}
				}
				if rerr != nil {
					n.Message = rerr.Error()
				} else {
					n.OK, n.Path, n.Resume = true, cmp.Or(out.Path, n.Path), out.Resume
					if st.LastRun != nil {
						st.recordRun(ExecuteProgram(st, st.LastRun.Program, now), now)
					}
				}
				st.setRestore(n)
				return nil
			})
		}()
	}
	res.View, err = a.render(ctx, &ext.UIRequest{UIParams: wire.UIParams{State: ext.EncodeState(p), TZ: r.TZ}})
	return res, err
}

// restoreSection shows the page's recent restores.
func (d draw) restoreSection() []wire.Section {
	if len(d.st.Restores) == 0 {
		return nil
	}
	sec := wire.Section{Title: "会话恢复"}
	for i, n := range d.st.Restores {
		row := wire.Component{
			Type:     wire.ComponentRow,
			Key:      fmt.Sprintf("restore-%d", i),
			Title:    clipRunes(cmp.Or(n.Title, n.EntryID), 120),
			Subtitle: n.Path,
		}
		switch {
		case n.Running:
			row.Icon, row.Badge = "refresh", &wire.Badge{Text: "恢复中", Tone: wire.ToneCaution}
			row.Value = d.formatTime(n.StartedAt)
		case n.OK:
			row.Icon, row.Badge = "check", &wire.Badge{Text: "已恢复", Tone: wire.TonePositive}
			row.Value = d.formatTime(n.At)
			if n.Resume != "" {
				row.Subtitle = n.Path + " · 可运行 " + n.Resume
			}
		default:
			row.Icon, row.Badge = "warning", &wire.Badge{Text: "恢复失败", Tone: wire.ToneDanger}
			row.Value = d.formatTime(n.At)
			row.Subtitle = n.Message
		}
		sec.Items = append(sec.Items, row)
	}
	sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentButton, Key: "clear-restores", Title: "清除记录", Action: &wire.Action{ID: "clear-restores"}})
	return []wire.Section{sec}
}

func pushPage(p page, title string) *wire.Action {
	return &wire.Action{
		ID:   "open",
		Push: &wire.Push{State: ext.EncodeState(p), Title: clipRunes(title, 60)},
	}
}

func archivedNote(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("（含 %d 个%s）", n, locationArchived)
}

func countBadge(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}

func toneIf(cond bool, t string) string {
	if cond {
		return t
	}
	return wire.ToneNeutral
}

func roleIcon(role string) string {
	switch role {
	case "Error":
		return "warning"
	case "User":
		return "person"
	case "File":
		return "file"
	case "Tool":
		return "code"
	default:
		return "chat"
	}
}

func badgeIfError(isErr bool, kind string) *wire.Badge {
	if !isErr {
		return nil
	}
	return &wire.Badge{Text: cmp.Or(kind, "error"), Tone: wire.ToneDanger}
}

func shortenHome(p string) string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" && strings.HasPrefix(p, home) {
		rel := strings.TrimPrefix(p, home)
		if rel == "" {
			return "~"
		}
		if strings.HasPrefix(rel, "/") {
			return "~" + rel
		}
	}
	if idx := strings.Index(p, "/.claude/projects"); idx >= 0 {
		return "~" + p[idx:]
	}
	if idx := strings.Index(p, "/.codex/sessions"); idx >= 0 {
		return "~" + p[idx:]
	}
	if idx := strings.Index(p, "/workspace/"); idx >= 0 {
		return "~" + p[idx:]
	}
	return p
}

func shortenHomeList(dirs []string) string {
	out := make([]string, len(dirs))
	for i, d := range dirs {
		out[i] = shortenHome(d)
	}
	return strings.Join(out, ", ")
}

func (d draw) formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	local := t.In(d.loc)
	nowLocal := d.now.In(d.loc)
	if local.Year() == nowLocal.Year() && local.YearDay() == nowLocal.YearDay() {
		return local.Format("15:04")
	}
	if local.Year() == nowLocal.Year() {
		return local.Format("01-02 15:04")
	}
	return local.Format("2006-01-02")
}
