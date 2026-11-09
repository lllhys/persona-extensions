package main

import (
	"cmp"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// StepSpec is one executable operator in a Search-as-Code pipeline.
type StepSpec struct {
	Op   string            `json:"op"`
	Args map[string]string `json:"args,omitempty"`
}

// SearchProgram is the compiled executable Search-as-Code pipeline.
type SearchProgram struct {
	Title       string     `json:"title"`
	Query       string     `json:"query,omitempty"`
	RecipeID    string     `json:"recipe_id,omitempty"`
	Scope       string     `json:"scope"`
	Steps       []StepSpec `json:"steps"`
	CodeListing string     `json:"code_listing"`
}

// StepTrace records the execution metrics of a single pipeline step.
type StepTrace struct {
	Index       int    `json:"index"`
	Op          string `json:"op"`
	Code        string `json:"code"`
	InputCount  int    `json:"input_count"`
	OutputCount int    `json:"output_count"`
	DurationMs  int64  `json:"duration_ms"`
	DurationUs  int64  `json:"duration_us"`
	Note        string `json:"note,omitempty"`
}

// EvidenceSnippet is a highlighted turn or file line inside a SearchHit.
type EvidenceSnippet struct {
	Role string `json:"role"` // "User", "Assistant", "Error", "Tool", "File"
	Line int    `json:"line,omitempty"`
	Text string `json:"text"`
}

// CorrelatedRef links a SearchHit to another session or file discovered via `correlate`.
type CorrelatedRef struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Kind      string `json:"kind"`       // "session" or "file"
	SharedKey string `json:"shared_key"` // e.g., file path or project name
	Reason    string `json:"reason"`
}

// SearchHit is one ranked evidence card produced by a Search-as-Code program.
type SearchHit struct {
	ID         string            `json:"id"`
	SourceKind string            `json:"source_kind"` // "session" or "file"
	SessionID  string            `json:"session_id,omitempty"`
	Title      string            `json:"title"`
	Project    string            `json:"project"`
	CWD        string            `json:"cwd,omitempty"`
	Branch     string            `json:"branch,omitempty"`
	Harness    string            `json:"harness"`
	Model      string            `json:"model,omitempty"`
	UpdatedAt  time.Time         `json:"updated_at"`
	Score      float64           `json:"score"`
	TurnCount  int               `json:"turn_count,omitempty"`
	ToolCalls  int               `json:"tool_calls,omitempty"`
	ErrorCount int               `json:"error_count,omitempty"`
	ErrorKind  string            `json:"error_kind,omitempty"`
	Files      []string          `json:"files,omitempty"`
	Snippets   []EvidenceSnippet `json:"snippets"`
	Correlated []CorrelatedRef   `json:"correlated,omitempty"`
}

// GroupSummary holds aggregated counts produced by `group_by`.
type GroupSummary struct {
	Field      string   `json:"field"`
	Key        string   `json:"key"`
	Count      int      `json:"count"`
	ErrorCount int      `json:"error_count"`
	Samples    []string `json:"samples,omitempty"`
}

// ExecutionResult is the complete output of running a Search-as-Code program.
type ExecutionResult struct {
	Program         SearchProgram  `json:"program"`
	StepTraces      []StepTrace    `json:"step_traces"`
	Groups          []GroupSummary `json:"groups,omitempty"`
	Hits            []*SearchHit   `json:"hits"`
	TotalMatches    int            `json:"total_matches"`
	ScannedSessions int            `json:"scanned_sessions"`
	ScannedFiles    int            `json:"scanned_files"`
	TotalDurationMs int64          `json:"total_duration_ms"`
	RanAt           time.Time      `json:"ran_at"`
}

// CompileInput specifies either a raw Search-as-Code program, a recipe + query override, or a natural language query.
type CompileInput struct {
	Query        string
	Code         string
	Recipe       *Recipe
	Scope        string
	Project      string
	Harness      string
	Model        string
	Since        string
	ExcludeTools *bool
	Limit        int
}

var knownOps = []string{
	"scan_sessions",
	"scan_files",
	"where",
	"exclude_tools",
	"match_regex",
	"correlate",
	"dedup",
	"group_by",
	"rank",
	"limit",
}

// CompileProgram compiles either explicit Search-as-Code pipeline code or a natural-language query into a validated SearchProgram.
func CompileProgram(in CompileInput) (SearchProgram, error) {
	scope := cmp.Or(strings.TrimSpace(in.Scope), "conversation")
	if !isValidScope(scope) {
		scope = "conversation"
	}
	code := strings.TrimSpace(in.Code)
	recipeID := ""
	title := ""

	if in.Recipe != nil && code == "" {
		code = in.Recipe.Code
		recipeID = in.Recipe.ID
		title = in.Recipe.Name
		if in.Scope == "" && isValidScope(in.Recipe.Scope) {
			scope = in.Recipe.Scope
		}
	}

	// If explicit pipeline code is given (or comes from a recipe), parse it and inject query/filter overrides if provided.
	if code != "" || looksLikePipelineCode(in.Query) {
		if code == "" {
			code = strings.TrimSpace(in.Query)
		}
		steps, err := parsePipelineCode(code)
		if err != nil {
			return SearchProgram{}, err
		}
		steps = applyOverrides(steps, in, scope)
		if err := validateSteps(steps); err != nil {
			return SearchProgram{}, err
		}
		for _, s := range steps {
			if s.Op == "scan_sessions" && isValidScope(s.Args["scope"]) {
				scope = s.Args["scope"]
			}
		}
		if title == "" {
			if in.Query != "" && !looksLikePipelineCode(in.Query) {
				title = "搜索 · " + clipRunes(in.Query, 40)
			} else {
				title = "代码化搜索流水线"
			}
		}
		return SearchProgram{
			Title:       title,
			Query:       strings.TrimSpace(in.Query),
			RecipeID:    recipeID,
			Scope:       scope,
			Steps:       steps,
			CodeListing: formatPipelineCode(steps),
		}, nil
	}

	// Otherwise compile natural language query + inline tokens into a multi-step Search-as-Code pipeline.
	return compileNaturalLanguage(in, scope)
}

func looksLikePipelineCode(q string) bool {
	q = strings.TrimSpace(q)
	if strings.Contains(q, "|>") {
		return true
	}
	for _, op := range knownOps {
		if strings.HasPrefix(q, op+"(") {
			return true
		}
	}
	return false
}

var callStepRe = regexp.MustCompile(`^([a-z_]+)\s*\((.*)\)$`)
var argPairRe = regexp.MustCompile(`([a-z_]+)\s*=\s*("[^"]*"|'[^']*'|[^\s,]+)`)

func parsePipelineCode(code string) ([]StepSpec, error) {
	var rawSteps []string
	for _, line := range strings.Split(code, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		parts := strings.Split(line, "|>")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				rawSteps = append(rawSteps, p)
			}
		}
	}
	if len(rawSteps) == 0 {
		return nil, fmt.Errorf("未包含任何算子步骤")
	}
	if len(rawSteps) > maxSteps {
		return nil, fmt.Errorf("算子步骤不能超过 %d 步（当前 %d 步）", maxSteps, len(rawSteps))
	}

	var steps []StepSpec
	for i, raw := range rawSteps {
		m := callStepRe.FindStringSubmatch(raw)
		if m == nil {
			return nil, fmt.Errorf("第 %d 步 %q 语法无效，应为 op(key=\"value\") 格式", i+1, raw)
		}
		op := m[1]
		if !slices.Contains(knownOps, op) {
			return nil, fmt.Errorf("第 %d 步包含未知算子 %q（可用算子：%s）", i+1, op, strings.Join(knownOps, ", "))
		}
		argsBody := strings.TrimSpace(m[2])
		args := map[string]string{}
		if argsBody != "" {
			matches := argPairRe.FindAllStringSubmatch(argsBody, -1)
			if len(matches) == 0 {
				// Allow single positional argument for convenience, e.g. limit(10) or match_regex("foo")
				val := strings.Trim(argsBody, `"' `)
				switch op {
				case "limit":
					args["n"] = val
				case "match_regex", "scan_files":
					args["pattern"] = val
				case "group_by":
					args["field"] = val
				case "correlate", "dedup", "rank":
					args["by"] = val
				case "scan_sessions":
					args["query"] = val
				default:
					return nil, fmt.Errorf("第 %d 步 %q 参数格式错误，请使用 key=\"value\"", i+1, raw)
				}
			} else {
				for _, am := range matches {
					k := am[1]
					v := strings.TrimSpace(am[2])
					v = strings.Trim(v, `"'`)
					args[k] = v
				}
			}
		}
		steps = append(steps, StepSpec{Op: op, Args: args})
	}
	return steps, nil
}

func validateSteps(steps []StepSpec) error {
	if len(steps) == 0 {
		return fmt.Errorf("搜索流水线不能为空")
	}
	hasSource := false
	for i, s := range steps {
		switch s.Op {
		case "scan_sessions", "scan_files":
			hasSource = true
			if s.Op == "scan_sessions" {
				if sc := s.Args["scope"]; sc != "" && !isValidScope(sc) {
					return fmt.Errorf("第 %d 步 scan_sessions 的 scope=%q 无效", i+1, sc)
				}
			}
		case "match_regex":
			pat := s.Args["pattern"]
			if pat == "" {
				return fmt.Errorf("第 %d 步 match_regex 缺少 pattern 参数", i+1)
			}
			if _, err := regexp.Compile("(?i)" + pat); err != nil {
				return fmt.Errorf("第 %d 步 match_regex 正则无效：%v", i+1, err)
			}
		case "limit":
			if nStr := s.Args["n"]; nStr != "" {
				n, err := strconv.Atoi(nStr)
				if err != nil || n < 1 || n > maxLimitCap {
					return fmt.Errorf("第 %d 步 limit(n=%s) 必须是 1 到 %d 之间的整数", i+1, nStr, maxLimitCap)
				}
			}
		}
	}
	if !hasSource {
		return fmt.Errorf("搜索流水线必须包含至少一个数据源算子（scan_sessions 或 scan_files）")
	}
	return nil
}

func applyOverrides(steps []StepSpec, in CompileInput, scope string) []StepSpec {
	out := make([]StepSpec, len(steps))
	q := strings.TrimSpace(in.Query)
	if looksLikePipelineCode(q) {
		q = ""
	}
	for i, s := range steps {
		args := map[string]string{}
		for k, v := range s.Args {
			args[k] = v
		}
		if s.Op == "scan_sessions" {
			if q != "" {
				args["query"] = q
			}
			if in.Scope != "" && isValidScope(in.Scope) {
				args["scope"] = in.Scope
			} else if args["scope"] == "" {
				args["scope"] = scope
			}
			if in.Project != "" {
				args["project"] = in.Project
			}
			if in.Harness != "" {
				args["harness"] = in.Harness
			}
			if in.Model != "" {
				args["model"] = in.Model
			}
			if in.Since != "" {
				args["since"] = in.Since
			}
		}
		if s.Op == "limit" && in.Limit > 0 {
			args["n"] = strconv.Itoa(min(in.Limit, maxLimitCap))
		}
		out[i] = StepSpec{Op: s.Op, Args: args}
	}
	return out
}

func compileNaturalLanguage(in CompileInput, scope string) (SearchProgram, error) {
	rawQuery := strings.TrimSpace(in.Query)
	project := strings.TrimSpace(in.Project)
	harness := strings.TrimSpace(in.Harness)
	model := strings.TrimSpace(in.Model)
	since := strings.TrimSpace(in.Since)
	regexPat := ""
	groupField := "project"
	correlateBy := "file"
	fileGlob := ""
	includeFiles := false
	excludeTools := scope == "conversation"
	if in.ExcludeTools != nil {
		excludeTools = *in.ExcludeTools
	}

	var freeWords []string
	for _, tok := range strings.Fields(rawQuery) {
		k, v, hasColon := strings.Cut(tok, ":")
		if hasColon && v != "" {
			switch strings.ToLower(k) {
			case "scope":
				if isValidScope(strings.ToLower(v)) {
					scope = strings.ToLower(v)
					if scope == "conversation" && in.ExcludeTools == nil {
						excludeTools = true
					}
					continue
				}
			case "project", "proj", "repo":
				project = v
				continue
			case "harness", "agent":
				harness = strings.ToLower(v)
				continue
			case "model":
				model = v
				continue
			case "since", "after":
				since = v
				continue
			case "regex", "re":
				regexPat = v
				continue
			case "group", "group_by":
				groupField = strings.ToLower(v)
				continue
			case "correlate", "join":
				correlateBy = strings.ToLower(v)
				continue
			case "file", "glob", "path":
				fileGlob = v
				includeFiles = true
				continue
			case "without", "exclude":
				if strings.EqualFold(v, "tools") || strings.EqualFold(v, "tool") {
					excludeTools = true
					continue
				}
			}
		}
		freeWords = append(freeWords, tok)
	}

	cleanQuery := strings.Join(freeWords, " ")
	lowQuery := strings.ToLower(cleanQuery)

	// Detect error triage intent from natural language when scope was not explicitly restricted
	rankBy := "relevance_freshness"
	if scope == "errors" || strings.Contains(lowQuery, "报错") || strings.Contains(lowQuery, "异常") ||
		strings.Contains(lowQuery, "失败") || strings.Contains(lowQuery, "error") || strings.Contains(lowQuery, "panic") {
		rankBy = "errors_first"
	}

	scanArgs := map[string]string{
		"query": cleanQuery,
		"scope": scope,
	}
	if project != "" {
		scanArgs["project"] = project
	}
	if harness != "" {
		scanArgs["harness"] = harness
	}
	if model != "" {
		scanArgs["model"] = model
	}
	if since != "" {
		scanArgs["since"] = since
	}

	steps := []StepSpec{{Op: "scan_sessions", Args: scanArgs}}

	if includeFiles {
		fArgs := map[string]string{"pattern": cleanQuery}
		if fileGlob != "" {
			fArgs["glob"] = fileGlob
		}
		steps = append(steps, StepSpec{Op: "scan_files", Args: fArgs})
	}

	if excludeTools {
		steps = append(steps, StepSpec{Op: "exclude_tools"})
	}

	if regexPat != "" {
		if _, err := regexp.Compile("(?i)" + regexPat); err != nil {
			return SearchProgram{}, fmt.Errorf("regex:%s 正则表达式无效：%v", regexPat, err)
		}
		steps = append(steps, StepSpec{Op: "match_regex", Args: map[string]string{"pattern": regexPat}})
	}

	steps = append(steps,
		StepSpec{Op: "correlate", Args: map[string]string{"by": correlateBy}},
		StepSpec{Op: "group_by", Args: map[string]string{"field": groupField}},
		StepSpec{Op: "rank", Args: map[string]string{"by": rankBy}},
	)

	lim := defaultLim
	if in.Limit > 0 {
		lim = min(in.Limit, maxLimitCap)
	}
	steps = append(steps, StepSpec{Op: "limit", Args: map[string]string{"n": strconv.Itoa(lim)}})

	title := "全量会话检索"
	if cleanQuery != "" {
		title = "搜索 · " + clipRunes(cleanQuery, 40)
	} else if scope == "errors" {
		title = "报错与异常会话检索"
	}

	return SearchProgram{
		Title:       title,
		Query:       rawQuery,
		Scope:       scope,
		Steps:       steps,
		CodeListing: formatPipelineCode(steps),
	}, nil
}

func formatPipelineCode(steps []StepSpec) string {
	lines := make([]string, len(steps))
	for i, s := range steps {
		prefix := "|> "
		if i == 0 {
			prefix = ""
		}
		lines[i] = prefix + formatStepCall(s)
	}
	return strings.Join(lines, "\n")
}

func formatStepCall(s StepSpec) string {
	if len(s.Args) == 0 {
		return s.Op + "()"
	}
	keys := make([]string, 0, len(s.Args))
	for k := range s.Args {
		keys = append(keys, k)
	}
	// Deterministic argument order with query and scope first.
	slices.SortFunc(keys, func(a, b string) int {
		order := func(k string) int {
			switch k {
			case "query", "pattern":
				return 0
			case "scope", "glob":
				return 1
			case "project", "by", "field", "n":
				return 2
			default:
				return 3
			}
		}
		if oa, ob := order(a), order(b); oa != ob {
			return cmp.Compare(oa, ob)
		}
		return cmp.Compare(a, b)
	})
	parts := make([]string, len(keys))
	for i, k := range keys {
		v := s.Args[k]
		if k == "n" {
			parts[i] = fmt.Sprintf("%s=%s", k, v)
		} else {
			parts[i] = fmt.Sprintf("%s=%q", k, v)
		}
	}
	return fmt.Sprintf("%s(%s)", s.Op, strings.Join(parts, ", "))
}

// ExecuteProgram runs a compiled SearchProgram against the local session transcripts and repository files.
func ExecuteProgram(st *State, prog SearchProgram, now time.Time) *ExecutionResult {
	startAll := time.Now()
	sessions, stats := loadSessions(st)
	var repoDirs []string
	if st != nil {
		repoDirs = st.RepoDirs
	}

	var working []*SearchHit
	var traces []StepTrace
	var groups []GroupSummary
	scannedFiles := 0

	for idx, step := range prog.Steps {
		stepStart := time.Now()
		inCount := len(working)
		if step.Op == "scan_sessions" && inCount == 0 {
			inCount = len(sessions)
		}
		note := ""

		switch step.Op {
		case "scan_sessions":
			hits := evalScanSessions(sessions, step.Args, now)
			working = append(working, hits...)
			q := step.Args["query"]
			sc := cmp.Or(step.Args["scope"], "conversation")
			note = fmt.Sprintf("扫描 %d 个会话，范围 %s", len(sessions), sc)
			if q != "" {
				note += fmt.Sprintf("，查询 %q", clipRunes(q, 28))
			}

		case "scan_files":
			inCount = len(repoDirs)
			fHits, fScanned := scanRepoFiles(repoDirs, step.Args["pattern"], step.Args["glob"])
			scannedFiles += fScanned
			working = append(working, fHits...)
			note = fmt.Sprintf("扫描 %d 个本地代码/文档文件", fScanned)

		case "where":
			working = evalWhere(working, sessions, step.Args, now)
			note = "按附加条件过滤命中集"

		case "exclude_tools":
			working = evalExcludeTools(working, sessions, prog.Query)
			note = "剥离 tool_use / tool_result 噪音，仅保留对话与有效异常证据"

		case "match_regex":
			pat := step.Args["pattern"]
			working = evalMatchRegex(working, pat)
			note = fmt.Sprintf("正则过滤 /%s/i", clipRunes(pat, 32))

		case "correlate":
			by := cmp.Or(step.Args["by"], "file")
			corrCount := evalCorrelate(working, sessions, by)
			note = fmt.Sprintf("按 %s 跨会话关联，发现 %d 条上下文引用", by, corrCount)

		case "dedup":
			by := cmp.Or(step.Args["by"], "session")
			working = evalDedup(working, by)
			note = fmt.Sprintf("按 %s 去重合并", by)

		case "group_by":
			field := cmp.Or(step.Args["field"], "project")
			groups = evalGroupBy(working, field)
			note = fmt.Sprintf("按 %s 聚合为 %d 个分组", field, len(groups))

		case "rank":
			by := cmp.Or(step.Args["by"], "relevance_freshness")
			evalRank(working, by, now)
			note = fmt.Sprintf("按 %s 综合打分排序", by)

		case "limit":
			n, err := strconv.Atoi(step.Args["n"])
			if err != nil || n <= 0 {
				n = defaultLim
			}
			if len(working) > n {
				working = working[:n]
			}
			note = fmt.Sprintf("截取 Top %d 条结果", n)
		}

		dur := time.Since(stepStart)
		traces = append(traces, StepTrace{
			Index:       idx + 1,
			Op:          step.Op,
			Code:        formatStepCall(step),
			InputCount:  inCount,
			OutputCount: len(working),
			DurationMs:  max(dur.Milliseconds(), 0),
			DurationUs:  max(dur.Microseconds(), 1),
			Note:        note,
		})
	}

	if len(groups) == 0 && len(working) > 0 {
		groups = evalGroupBy(working, "project")
	}

	totalDur := time.Since(startAll)
	return &ExecutionResult{
		Program:         prog,
		StepTraces:      traces,
		Groups:          groups,
		Hits:            working,
		TotalMatches:    len(working),
		ScannedSessions: stats.TotalSessions,
		ScannedFiles:    scannedFiles,
		TotalDurationMs: max(totalDur.Milliseconds(), 1),
		RanAt:           now.UTC(),
	}
}

func evalScanSessions(sessions []*SessionRecord, args map[string]string, now time.Time) []*SearchHit {
	query := strings.TrimSpace(args["query"])
	scope := cmp.Or(strings.TrimSpace(args["scope"]), "conversation")
	project := strings.ToLower(strings.TrimSpace(args["project"]))
	harness := strings.ToLower(strings.TrimSpace(args["harness"]))
	model := strings.ToLower(strings.TrimSpace(args["model"]))
	sinceTime := parseSince(args["since"], now)
	terms := splitQueryTerms(query)

	var hits []*SearchHit
	for _, s := range sessions {
		if project != "" && !strings.Contains(strings.ToLower(s.Project), project) && !strings.Contains(strings.ToLower(s.CWD), project) {
			continue
		}
		if harness != "" && !strings.Contains(strings.ToLower(s.Harness), harness) {
			continue
		}
		if model != "" && !strings.Contains(strings.ToLower(s.Model), model) {
			continue
		}
		if !sinceTime.IsZero() && s.UpdatedAt.Before(sinceTime) {
			continue
		}
		if scope == "errors" && s.ErrorCount == 0 {
			continue
		}

		var snippets []EvidenceSnippet
		score := 0.0
		firstErrKind := ""

		titleMatched := len(terms) > 0 && matchesAnyTerm(s.Title+" "+s.Project+" "+s.Branch, terms)
		if titleMatched {
			score += 4.0
		}

		for _, tr := range s.Turns {
			if !turnFitsScope(tr, scope) {
				continue
			}
			if tr.IsError && firstErrKind == "" {
				firstErrKind = tr.ErrorKind
			}
			if len(terms) == 0 {
				if len(snippets) < 3 {
					snippets = append(snippets, toSnippet(tr))
				}
				score += 1.0
				continue
			}
			termHits := countTermMatches(tr.Text, terms)
			if termHits > 0 {
				weight := float64(termHits) * 2.5
				if !tr.IsTool {
					weight *= 1.4
				}
				if tr.IsError {
					weight += 2.0
				}
				score += weight
				if len(snippets) < 4 {
					snippets = append(snippets, toSnippet(tr))
				}
			}
		}

		if len(terms) > 0 && len(snippets) == 0 && !titleMatched {
			continue
		}
		if len(snippets) == 0 {
			for _, tr := range s.Turns {
				if turnFitsScope(tr, scope) {
					snippets = append(snippets, toSnippet(tr))
					if len(snippets) >= 2 {
						break
					}
				}
			}
		}
		if len(snippets) == 0 {
			continue
		}
		if scope == "errors" {
			slices.SortStableFunc(snippets, func(a, b EvidenceSnippet) int {
				scoreRole := func(r string) int {
					if r == "Error" {
						return 0
					}
					return 1
				}
				return cmp.Compare(scoreRole(a.Role), scoreRole(b.Role))
			})
		}

		hits = append(hits, &SearchHit{
			ID:         "session:" + s.SessionID,
			SourceKind: "session",
			SessionID:  s.SessionID,
			Title:      s.Title,
			Project:    s.Project,
			CWD:        s.CWD,
			Branch:     s.Branch,
			Harness:    s.Harness,
			Model:      s.Model,
			UpdatedAt:  s.UpdatedAt,
			Score:      score,
			TurnCount:  s.TurnCount,
			ToolCalls:  s.ToolCalls,
			ErrorCount: s.ErrorCount,
			ErrorKind:  firstErrKind,
			Files:      slices.Clone(s.Files),
			Snippets:   snippets,
		})
	}
	return hits
}

func turnFitsScope(tr SessionTurn, scope string) bool {
	switch scope {
	case "conversation":
		return !tr.IsTool && (tr.Role == "user" || tr.Role == "assistant")
	case "user":
		return !tr.IsTool && tr.Role == "user"
	case "assistant":
		return !tr.IsTool && tr.Role == "assistant"
	case "errors":
		return tr.IsError || tr.Role == "error"
	default: // "all"
		return true
	}
}

func toSnippet(tr SessionTurn) EvidenceSnippet {
	roleLabel := "Assistant"
	switch {
	case tr.Role == "error" || (tr.IsError && tr.IsTool):
		roleLabel = "Error"
	case tr.Role == "user":
		roleLabel = "User"
	case tr.Role == "assistant":
		roleLabel = "Assistant"
	case tr.IsTool || tr.Role == "tool":
		roleLabel = "Tool"
	case tr.IsError:
		roleLabel = "Error"
	}
	return EvidenceSnippet{
		Role: roleLabel,
		Line: tr.Line,
		Text: clipRunes(oneLine(tr.Text), 180),
	}
}

func evalWhere(hits []*SearchHit, sessions []*SessionRecord, args map[string]string, now time.Time) []*SearchHit {
	project := strings.ToLower(strings.TrimSpace(args["project"]))
	harness := strings.ToLower(strings.TrimSpace(args["harness"]))
	model := strings.ToLower(strings.TrimSpace(args["model"]))
	role := strings.ToLower(strings.TrimSpace(args["role"]))
	hasErr := strings.ToLower(strings.TrimSpace(args["has_error"]))
	sinceTime := parseSince(args["since"], now)

	var out []*SearchHit
	for _, h := range hits {
		if project != "" && !strings.Contains(strings.ToLower(h.Project), project) {
			continue
		}
		if harness != "" && !strings.Contains(strings.ToLower(h.Harness), harness) {
			continue
		}
		if model != "" && !strings.Contains(strings.ToLower(h.Model), model) {
			continue
		}
		if !sinceTime.IsZero() && h.UpdatedAt.Before(sinceTime) {
			continue
		}
		if hasErr == "true" && h.ErrorCount == 0 {
			continue
		}
		if hasErr == "false" && h.ErrorCount > 0 {
			continue
		}
		if role != "" {
			var kept []EvidenceSnippet
			for _, sn := range h.Snippets {
				if strings.EqualFold(sn.Role, role) {
					kept = append(kept, sn)
				}
			}
			if len(kept) == 0 {
				continue
			}
			h.Snippets = kept
		}
		out = append(out, h)
	}
	return out
}

func evalExcludeTools(hits []*SearchHit, sessions []*SessionRecord, query string) []*SearchHit {
	byID := map[string]*SessionRecord{}
	for _, s := range sessions {
		byID[s.SessionID] = s
	}
	terms := splitQueryTerms(query)
	var out []*SearchHit
	for _, h := range hits {
		if h.SourceKind != "session" {
			out = append(out, h)
			continue
		}
		var kept []EvidenceSnippet
		for _, sn := range h.Snippets {
			if sn.Role != "Tool" {
				kept = append(kept, sn)
			}
		}
		if len(kept) == 0 {
			if s := byID[h.SessionID]; s != nil {
				for _, tr := range s.Turns {
					if tr.IsTool && !tr.IsError {
						continue
					}
					if len(terms) == 0 || countTermMatches(tr.Text, terms) > 0 {
						kept = append(kept, toSnippet(tr))
						if len(kept) >= 3 {
							break
						}
					}
				}
			}
		}
		if len(kept) == 0 {
			continue
		}
		h.Snippets = kept
		out = append(out, h)
	}
	return out
}

func evalMatchRegex(hits []*SearchHit, pattern string) []*SearchHit {
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return hits
	}
	var out []*SearchHit
	for _, h := range hits {
		var matchedSnippets []EvidenceSnippet
		for _, sn := range h.Snippets {
			if re.MatchString(sn.Text) {
				matchedSnippets = append(matchedSnippets, sn)
			}
		}
		if len(matchedSnippets) > 0 || re.MatchString(h.Title) || re.MatchString(h.Project) {
			if len(matchedSnippets) > 0 {
				h.Snippets = matchedSnippets
			}
			h.Score += 3.0
			out = append(out, h)
		}
	}
	return out
}

func evalCorrelate(hits []*SearchHit, sessions []*SessionRecord, by string) int {
	totalLinks := 0
	for _, h := range hits {
		var refs []CorrelatedRef
		seen := map[string]bool{h.ID: true, h.SessionID: true}

		for _, other := range sessions {
			if seen[other.SessionID] || seen["session:"+other.SessionID] {
				continue
			}
			switch by {
			case "project":
				if h.Project != "" && strings.EqualFold(h.Project, other.Project) {
					refs = append(refs, CorrelatedRef{
						ID:        "session:" + other.SessionID,
						Title:     other.Title,
						Kind:      "session",
						SharedKey: h.Project,
						Reason:    "同一项目 " + h.Project,
					})
					seen[other.SessionID] = true
					seen["session:"+other.SessionID] = true
				}
			case "branch":
				if h.Branch != "" && strings.EqualFold(h.Branch, other.Branch) {
					refs = append(refs, CorrelatedRef{
						ID:        "session:" + other.SessionID,
						Title:     other.Title,
						Kind:      "session",
						SharedKey: h.Branch,
						Reason:    "同一分支 " + h.Branch,
					})
					seen[other.SessionID] = true
					seen["session:"+other.SessionID] = true
				}
			default: // "file"
				sharedFile := findSharedFile(h.Files, other.Files)
				if sharedFile != "" {
					refs = append(refs, CorrelatedRef{
						ID:        "session:" + other.SessionID,
						Title:     other.Title,
						Kind:      "session",
						SharedKey: sharedFile,
						Reason:    "共同涉及文件 " + sharedFile,
					})
					seen[other.SessionID] = true
					seen["session:"+other.SessionID] = true
				} else if h.Project != "" && strings.EqualFold(h.Project, other.Project) && len(refs) == 0 {
					refs = append(refs, CorrelatedRef{
						ID:        "session:" + other.SessionID,
						Title:     other.Title,
						Kind:      "session",
						SharedKey: h.Project,
						Reason:    "同项目关联会话 " + h.Project,
					})
					seen[other.SessionID] = true
					seen["session:"+other.SessionID] = true
				}
			}
			if len(refs) >= 4 {
				break
			}
		}

		// Also correlate across hits (e.g. session <-> repo file)
		for _, otherHit := range hits {
			if seen[otherHit.ID] {
				continue
			}
			if shared := findSharedFile(h.Files, otherHit.Files); shared != "" {
				refs = append(refs, CorrelatedRef{
					ID:        otherHit.ID,
					Title:     otherHit.Title,
					Kind:      otherHit.SourceKind,
					SharedKey: shared,
					Reason:    "关联代码文件 " + shared,
				})
				seen[otherHit.ID] = true
			}
			if len(refs) >= 5 {
				break
			}
		}

		h.Correlated = refs
		if len(refs) > 0 {
			h.Score += float64(len(refs)) * 1.2
			totalLinks += len(refs)
		}
	}
	return totalLinks
}

func findSharedFile(a, b []string) string {
	for _, fa := range a {
		baseA := filepath.Base(fa)
		for _, fb := range b {
			if strings.EqualFold(fa, fb) || (len(baseA) > 4 && strings.EqualFold(baseA, filepath.Base(fb))) {
				return fa
			}
		}
	}
	return ""
}

func evalDedup(hits []*SearchHit, by string) []*SearchHit {
	seen := map[string]bool{}
	var out []*SearchHit
	for _, h := range hits {
		var key string
		switch by {
		case "title":
			key = strings.ToLower(strings.TrimSpace(h.Title))
		case "file":
			if len(h.Files) > 0 {
				key = strings.ToLower(h.Files[0])
			} else {
				key = h.ID
			}
		default:
			key = cmp.Or(h.SessionID, h.ID)
		}
		if key == "" {
			key = h.ID
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, h)
	}
	return out
}

func evalGroupBy(hits []*SearchHit, field string) []GroupSummary {
	type bucket struct {
		key     string
		count   int
		errors  int
		samples []string
	}
	var order []string
	byKey := map[string]*bucket{}

	for _, h := range hits {
		var key string
		switch field {
		case "harness":
			key = cmp.Or(h.Harness, "unknown")
		case "model":
			key = cmp.Or(h.Model, "unknown")
		case "error_kind":
			key = cmp.Or(h.ErrorKind, "no_error")
		case "role":
			if len(h.Snippets) > 0 {
				key = h.Snippets[0].Role
			} else {
				key = "Conversation"
			}
		default:
			field = "project"
			key = cmp.Or(h.Project, "workspace")
		}
		b, ok := byKey[key]
		if !ok {
			b = &bucket{key: key}
			byKey[key] = b
			order = append(order, key)
		}
		b.count++
		if h.ErrorCount > 0 {
			b.errors++
		}
		if len(b.samples) < 3 {
			b.samples = append(b.samples, clipRunes(h.Title, 48))
		}
	}

	out := make([]GroupSummary, 0, len(order))
	for _, k := range order {
		b := byKey[k]
		out = append(out, GroupSummary{
			Field:      field,
			Key:        b.key,
			Count:      b.count,
			ErrorCount: b.errors,
			Samples:    b.samples,
		})
	}
	slices.SortFunc(out, func(a, b GroupSummary) int {
		if c := cmp.Compare(b.Count, a.Count); c != 0 {
			return c
		}
		return cmp.Compare(a.Key, b.Key)
	})
	return out
}

func evalRank(hits []*SearchHit, by string, now time.Time) {
	for _, h := range hits {
		hoursAgo := max(now.Sub(h.UpdatedAt).Hours(), 0)
		freshnessBoost := 4.0 / (1.0 + hoursAgo/24.0)
		switch by {
		case "freshness":
			h.Score = freshnessBoost*10.0 + h.Score*0.2
		case "errors_first":
			h.Score = h.Score + float64(h.ErrorCount)*5.0 + freshnessBoost
		default: // "relevance_freshness"
			h.Score = math.Round((h.Score+freshnessBoost)*100) / 100
		}
	}
	slices.SortFunc(hits, func(a, b *SearchHit) int {
		if c := cmp.Compare(b.Score, a.Score); c != 0 {
			return c
		}
		return b.UpdatedAt.Compare(a.UpdatedAt)
	})
}

func splitQueryTerms(q string) []string {
	var out []string
	for _, w := range strings.Fields(strings.ToLower(strings.TrimSpace(q))) {
		if _, _, ok := strings.Cut(w, ":"); ok {
			continue
		}
		w = strings.Trim(w, `"'(),.;!?`)
		if w != "" && !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	return out
}

func countTermMatches(text string, terms []string) int {
	if len(terms) == 0 {
		return 1
	}
	low := strings.ToLower(text)
	matched := 0
	for _, t := range terms {
		if strings.Contains(low, t) {
			matched++
		}
	}
	return matched
}

func matchesAnyTerm(text string, terms []string) bool {
	return countTermMatches(text, terms) > 0
}

func matchesAllTerms(text string, terms []string) bool {
	if len(terms) == 0 {
		return true
	}
	return countTermMatches(text, terms) == len(terms)
}

func parseSince(raw string, now time.Time) time.Time {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return time.Time{}
	}
	if strings.HasSuffix(raw, "d") {
		if days, err := strconv.Atoi(strings.TrimSuffix(raw, "d")); err == nil && days > 0 {
			return now.Add(-time.Duration(days) * 24 * time.Hour)
		}
	}
	if strings.HasSuffix(raw, "h") {
		if hrs, err := strconv.Atoi(strings.TrimSuffix(raw, "h")); err == nil && hrs > 0 {
			return now.Add(-time.Duration(hrs) * time.Hour)
		}
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
