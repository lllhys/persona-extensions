package main

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// `disklessly mcp` serves the Disklessly fleet disk watermark, scan, safe cleanup and cold-session tiering tools
// to the assistant over stdio (Model Context Protocol, JSON-RPC 2.0, one message per line).
// Read-only tools are listed in persona-extension.json's `allow`; write tools require user confirmation.

const mcpProtocol = "2025-06-18"

type mcpServer struct {
	store  *store
	runner Runner
}

type tool struct {
	name, description string
	readOnly          bool
	destructive       bool
	props             map[string]any
	required          []string
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

var (
	machineArg = str("目标机器 ID 或 SSH Host 别名（如 local、armbox、devvm、mini-2）；不填默认 local（本机）")
)

var tools = []tool{
	{
		name:     "disklessly_machines_status",
		readOnly: true,
		props: map[string]any{
			"refresh": map[string]any{"type": "boolean", "description": "若为 true，同时重新扫描 ~/.persona/config.json 与 ~/.ssh/config 中新增的机器节点"},
		},
		description: "查看所有接入 Persona / Disklessly 的机器节点磁盘水位与可回收空间概览（根分区总容量、已用/可用空间、使用率预警等级 ok/warning/critical/offline、可回收缓存体积、Agent 会话冷热分布及累计释放空间统计）。",
	},
	{
		name:     "disklessly_scan",
		readOnly: true,
		props: map[string]any{
			"machine": machineArg,
			"cached":  map[string]any{"type": "boolean", "description": "若为 true 且已有扫描缓存则直接返回缓存，否则实时探测目标机器"},
		},
		description: "扫描指定机器（本机或 SSH 远程机器）的磁盘使用情况（df）并探测 6 大可回收分类明细（go-cache、node-python-cache、docker-unused、rust-xcode-build、tmp-logs、cold-agent-sessions）及 Dry-Run 安全清理计划，完全只读无损。",
	},
	{
		name:     "disklessly_history",
		readOnly: true,
		props: map[string]any{
			"machine": str("只看指定机器 ID 或 Host 的清理记录；不填返回全部机器记录"),
			"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "description": "返回条数，默认 50"},
		},
		description: "查看各机器历史清理记录台账（每次清理前后的 df 实测可用空间对比，如 5.4 GiB → 18.0 GiB (+12.6 GiB)）与累计释放空间。",
	},
	{
		name:     "disklessly_sessions_search",
		readOnly: true,
		required: []string{"query"},
		props: map[string]any{
			"query":   str("搜索词（空格分隔，全部命中；不区分大小写，中文按子串匹配）：标题、首条消息、项目、工作目录、关键词、会话 ID、工具或机器名"),
			"machine": str("只搜索这台机器（ID 或 SSH Host 别名）；不填搜索全部机器"),
			"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "description": "返回条数，默认 20"},
			"deep":    map[string]any{"type": "boolean", "description": "若为 true，还在机器上运行 disklessly sessions search 全文搜索会话摘要（较慢），结果按 ID 合并"},
		},
		description: "在已上云的 Coding Agent 冷会话（Claude Code / Codex）里查找对话：按本地轻量索引搜索，返回机器、工具、项目、日期、首条消息、位置（在云端 / 未知 / 云端缺失 / 本地）与恢复后的继续命令。位置「未知」不代表丢失。",
	},
	{
		name:     "disklessly_sessions_status",
		readOnly: true,
		props: map[string]any{
			"machine": str("只看这台机器；不填返回全部机器"),
			"refresh": map[string]any{"type": "boolean", "description": "若为 true，先在机器上重新读取 disklessly 会话索引并检查备份后端与归档分段"},
		},
		description: "查看各机器冷会话云端备份状态：备份后端是否可达、最近检查时间、已索引会话数、按位置（在云端 / 未知 / 云端缺失 / 本地）统计、缺失或未确认的归档分段。",
	},
	{
		name:     "disklessly_clean",
		readOnly: false,
		props: map[string]any{
			"machine": machineArg,
			"categories": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string", "enum": CategoryIDs},
				"description": "要清理的分类列表（go-cache、node-python-cache、docker-unused、rust-xcode-build、tmp-logs、cold-agent-sessions）；不填默认清理全部 safe 等级项（不含冷会话上云，冷会话需显式指定 cold-agent-sessions 或用 disklessly_sessions_offload）",
			},
			"items": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "指定要清理的单项 ID 列表（如 cache-gobuild；sessions-cold-claude 等任一冷会话项会让 disklessly 一次处理所有 Agent 的冷会话）",
			},
			"include_review": map[string]any{
				"type":        "boolean",
				"description": "是否包含 review 等级项（如 Playwright 浏览器缓存），默认 false 只清理 safe 等级项",
			},
			"dry_run": map[string]any{
				"type":        "boolean",
				"description": "若为 true 仅返回 Dry-Run 清理计划而不实际删除",
			},
			"exclude_sessions": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "归档冷会话时必须保留在本地、绝不上传或删除的会话（会话文件绝对路径，或会话 ID：Claude 文件名、Codex rollout 末尾 UUID），例如仍被引用的会话",
			},
		},
		description: "对指定机器执行安全清理计划（支持指定分类、单项或全部 safe 项；冷会话上云只在显式指定 cold-agent-sessions 时执行，会上传后删除本地副本），返回清理前后 df 实测释放空间并写入清理历史台账。绝不触碰运行中容器、活跃 git 仓库、7 天内热会话或凭据密钥。",
	},
	{
		name:     "disklessly_machine_upsert",
		readOnly: false,
		required: []string{"host"},
		props: map[string]any{
			"id":            str("机器节点 ID（可选，不填由 host 自动生成）"),
			"host":          str("SSH Host 别名或主机地址（如 armbox、devvm、mini-2、10.0.0.5）"),
			"name":          str("显示名称（如 armbox (ARM VM)）"),
			"user":          str("SSH 登录用户名（可选，默认读 ~/.ssh/config）"),
			"port":          map[string]any{"type": "integer", "minimum": 1, "maximum": 65535, "description": "SSH 端口，默认 22"},
			"identity_file": str("SSH 私钥路径（可选，默认读 ~/.ssh/config）"),
		},
		description: "新增或修改一台纳管的机器节点（支持 SSH 远程机器或更新本机显示名称）。",
	},
	{
		name:        "disklessly_machine_delete",
		readOnly:    false,
		destructive: true,
		required:    []string{"machine"},
		props: map[string]any{
			"machine": str("要移除的机器 ID 或 SSH Host 别名（本机 local 不可移除）"),
		},
		description: "移除一台纳管的远程机器节点。移除前请与用户确认。",
	},
	{
		name:        "disklessly_sessions_offload",
		readOnly:    false,
		destructive: true,
		required:    []string{"machine"},
		props: map[string]any{
			"machine": str("目标机器 ID 或 SSH Host 别名"),
			"dry_run": map[string]any{"type": "boolean", "description": "若为 true 只返回将上云的冷会话计划与当前被引用（会保留）的会话，不上传也不删除"},
			"exclude_sessions": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "额外必须保留在本地的会话（会话文件绝对路径或会话 ID）",
			},
		},
		description: "把指定机器上 7 天外的 Coding Agent 冷会话由 disklessly 上传到备份后端，读回校验后才删除本地副本。运行中进程（--resume / codex resume）、打开中的会话文件、herdr / Persona 面板引用的会话自动保留；无法列出进程或后端不可达时中止，不删除任何会话。执行前请与用户确认。",
	},
	{
		name:     "disklessly_sessions_restore",
		readOnly: false,
		required: []string{"machine", "id"},
		props: map[string]any{
			"machine": str("会话所在机器 ID 或 SSH Host 别名"),
			"id":      str("会话条目 ID（disklessly_sessions_search 返回的 id）或会话 ID"),
		},
		description: "把一个已上云的冷会话从备份后端恢复到原路径，并返回继续对话的命令（claude --resume <id> 或 codex resume <id>）。",
	},
}

func (t tool) schema() map[string]any {
	s := map[string]any{"type": "object", "properties": t.props, "additionalProperties": false}
	if len(t.required) > 0 {
		s["required"] = t.required
	}
	return map[string]any{
		"name":        t.name,
		"description": t.description,
		"inputSchema": s,
		"annotations": map[string]any{
			"readOnlyHint":    t.readOnly,
			"destructiveHint": t.destructive,
		},
	}
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (m *mcpServer) serve(ctx context.Context, in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	enc := json.NewEncoder(out)
	for sc.Scan() && ctx.Err() == nil {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		resp := map[string]any{"jsonrpc": "2.0"}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			resp["id"], resp["error"] = nil, rpcError{-32700, "parse error"}
		} else if len(msg.ID) == 0 || string(msg.ID) == "null" {
			continue
		} else {
			res, rerr := m.handle(ctx, msg.Method, msg.Params)
			resp["id"] = msg.ID
			if rerr != nil {
				resp["error"] = rerr
			} else {
				resp["result"] = res
			}
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return sc.Err()
}

func (m *mcpServer) handle(ctx context.Context, method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(params, &p)
		return map[string]any{
			"protocolVersion": cmp.Or(p.ProtocolVersion, mcpProtocol),
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "disklessly", "version": about.Version},
			"instructions": "Disklessly 机器磁盘与冷会话管理：先用 disklessly_machines_status 查看所有纳管机器的水位和可回收空间，" +
				"再用 disklessly_scan 扫描目标机器查看 Dry-Run 清理计划与冷会话分布；需要执行清理时调用 disklessly_clean；" +
				"找回以前的 Agent 对话用 disklessly_sessions_search（位置「未知」不代表丢失），恢复用 disklessly_sessions_restore，冷会话上云用 disklessly_sessions_offload。",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		list := make([]map[string]any, len(tools))
		for i, t := range tools {
			list[i] = t.schema()
		}
		return map[string]any{"tools": list}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{-32602, "invalid params"}
		}
		out, err := m.call(ctx, p.Name, p.Arguments)
		if errors.Is(err, errNoTool) {
			return nil, &rpcError{-32602, err.Error()}
		}
		if err != nil {
			return map[string]any{"content": []map[string]any{{"type": "text", "text": err.Error()}}, "isError": true}, nil
		}
		text, _ := json.MarshalIndent(out, "", "  ")
		return map[string]any{"content": []map[string]any{{"type": "text", "text": string(text)}}, "structuredContent": out}, nil
	}
	return nil, &rpcError{-32601, "method not found: " + method}
}

var errNoTool = errors.New("unknown tool")

func (m *mcpServer) run() Runner {
	if m.runner == nil {
		return DefaultRunner
	}
	return m.runner
}

func (m *mcpServer) call(ctx context.Context, name string, raw json.RawMessage) (out any, err error) {
	args := func(v any) error {
		if len(raw) == 0 || string(raw) == "null" {
			return nil
		}
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		return dec.Decode(v)
	}

	switch name {
	case "disklessly_machines_status":
		var in struct {
			Refresh bool `json:"refresh"`
		}
		if err := args(&in); err != nil {
			return nil, err
		}
		if in.Refresh {
			_ = m.store.update(func(st *State, now time.Time) error {
				st.DiscoverMachines(m.store.homeDir, now)
				return nil
			})
		}
		err = m.store.read(func(st *State) {
			statuses := st.statuses()
			var totalReclaim, totalSafe, totalFreed uint64
			for _, s := range statuses {
				totalReclaim += s.ReclaimableBytes
				totalSafe += s.SafeBytes
			}
			for _, h := range st.History {
				totalFreed += h.FreedBytes
			}
			out = map[string]any{
				"machines":              statuses,
				"machine_count":         len(statuses),
				"total_reclaimable":     totalReclaim,
				"total_reclaimable_str": FormatBytes(totalReclaim),
				"total_safe_bytes":      totalSafe,
				"total_safe_str":        FormatBytes(totalSafe),
				"total_freed_bytes":     totalFreed,
				"total_freed_str":       FormatBytes(totalFreed),
				"categories":            CategoryLabels,
			}
		})
		return out, err

	case "disklessly_scan":
		var in struct {
			Machine string `json:"machine"`
			Cached  bool   `json:"cached"`
		}
		if err := args(&in); err != nil {
			return nil, err
		}
		target := cmp.Or(strings.TrimSpace(in.Machine), "local")
		err = m.store.update(func(st *State, now time.Time) error {
			mach := st.machine(target)
			if mach == nil {
				return fmt.Errorf("找不到机器 %q（可用 disklessly_machines_status 查看或用 disklessly_machine_upsert 添加）", target)
			}
			var scan *Scan
			if in.Cached && st.Scans[mach.ID] != nil {
				scan = st.Scans[mach.ID]
			} else {
				var serr error
				scan, serr = ScanMachine(ctx, m.run(), st, mach, now)
				if serr != nil {
					return serr
				}
			}
			plan, _ := BuildCleanPlan(mach, scan, nil, nil, false)
			out = map[string]any{
				"machine":      st.machineStatus(mach),
				"scan":         scan,
				"dry_run_plan": plan,
			}
			return nil
		})
		return out, err

	case "disklessly_history":
		var in struct {
			Machine string `json:"machine"`
			Limit   int    `json:"limit"`
		}
		if err := args(&in); err != nil {
			return nil, err
		}
		limit := cmp.Or(in.Limit, 50)
		if limit < 1 || limit > 200 {
			limit = 50
		}
		err = m.store.read(func(st *State) {
			var list []*CleanRecord
			var totalFreed uint64
			var targetID string
			if in.Machine != "" {
				if mach := st.machine(in.Machine); mach != nil {
					targetID = mach.ID
				} else {
					targetID = in.Machine
				}
			}
			for _, rec := range st.History {
				if targetID != "" && rec.MachineID != targetID && !strings.EqualFold(rec.Host, in.Machine) {
					continue
				}
				totalFreed += rec.FreedBytes
				if len(list) < limit {
					list = append(list, rec)
				}
			}
			if list == nil {
				list = []*CleanRecord{}
			}
			out = map[string]any{
				"history":         list,
				"count":           len(list),
				"total_freed":     totalFreed,
				"total_freed_str": FormatBytes(totalFreed),
			}
		})
		return out, err

	case "disklessly_clean":
		var in struct {
			Machine       string   `json:"machine"`
			Categories    []string `json:"categories"`
			Items         []string `json:"items"`
			IncludeReview bool     `json:"include_review"`
			DryRun        bool     `json:"dry_run"`
			// ExcludeSessions are transcripts (absolute paths or session ids) still referenced
			// elsewhere; cold-session archiving skips them (disklessly --exclude-from).
			ExcludeSessions []string `json:"exclude_sessions"`
		}
		if err := args(&in); err != nil {
			return nil, err
		}
		target := cmp.Or(strings.TrimSpace(in.Machine), "local")
		err = m.store.update(func(st *State, now time.Time) error {
			mach := st.machine(target)
			if mach == nil {
				return fmt.Errorf("找不到机器 %q", target)
			}
			plan, rec, cerr := ExecuteClean(ctx, m.run(), st, mach, in.Categories, in.Items, in.IncludeReview, in.DryRun, in.ExcludeSessions, now)
			if cerr != nil {
				return cerr
			}
			out = map[string]any{
				"dry_run": in.DryRun,
				"plan":    plan,
				"record":  rec,
				"machine": st.machineStatus(mach),
			}
			return nil
		})
		return out, err

	case "disklessly_sessions_search":
		var in struct {
			Query   string `json:"query"`
			Machine string `json:"machine"`
			Limit   int    `json:"limit"`
			Deep    bool   `json:"deep"`
		}
		if err := args(&in); err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.Query) == "" {
			return nil, errors.New("query 不能为空")
		}
		limit := cmp.Or(in.Limit, 20)
		if limit < 1 || limit > 200 {
			limit = 20
		}
		var ferr error
		err = m.store.read(func(st *State) {
			if in.Machine != "" && st.machine(in.Machine) == nil {
				ferr = fmt.Errorf("找不到机器 %q", in.Machine)
				return
			}
			now := m.store.now().UTC()
			var hits []ColdHit
			var errs []string
			if in.Deep {
				hits, errs = DeepSearchCold(ctx, m.run(), st, in.Query, in.Machine, limit, now)
			} else {
				hits = SearchCold(st, in.Query, in.Machine, limit, now)
			}
			list := make([]map[string]any, 0, len(hits))
			for _, h := range hits {
				list = append(list, map[string]any{"session": h, "resume": resumeHint(h.ColdEntry)})
			}
			res := map[string]any{"query": in.Query, "count": len(list), "results": list}
			if len(errs) > 0 {
				res["deep_errors"] = errs
			}
			out = res
		})
		return out, cmp.Or(err, ferr)

	case "disklessly_sessions_status":
		var in struct {
			Machine string `json:"machine"`
			Refresh bool   `json:"refresh"`
		}
		if err := args(&in); err != nil {
			return nil, err
		}
		summarize := func(st *State, now time.Time) error {
			var list []map[string]any
			for _, mach := range st.Machines {
				if in.Machine != "" {
					if mm := st.machine(in.Machine); mm == nil || mm.ID != mach.ID {
						continue
					}
				} else if !mach.Enabled {
					continue
				}
				if in.Refresh {
					_, _ = RefreshColdIndex(ctx, m.run(), st, mach, now)
				}
				list = append(list, coldStatusOf(st, mach, now))
			}
			if in.Machine != "" && len(list) == 0 {
				return fmt.Errorf("找不到机器 %q", in.Machine)
			}
			out = map[string]any{"machines": list}
			return nil
		}
		if in.Refresh {
			return out, m.store.update(summarize)
		}
		var serr error
		err = m.store.read(func(st *State) { serr = summarize(st, m.store.now().UTC()) })
		return out, cmp.Or(err, serr)

	case "disklessly_sessions_offload":
		var in struct {
			Machine         string   `json:"machine"`
			DryRun          bool     `json:"dry_run"`
			ExcludeSessions []string `json:"exclude_sessions"`
		}
		if err := args(&in); err != nil {
			return nil, err
		}
		err = m.store.update(func(st *State, now time.Time) error {
			mach := st.machine(in.Machine)
			if mach == nil || strings.TrimSpace(in.Machine) == "" {
				return fmt.Errorf("找不到机器 %q", in.Machine)
			}
			if in.DryRun {
				refsOut, rerr := m.run()(ctx, mach, ReferencedSessionsScript, 60*time.Second)
				if rerr != nil {
					return rerr
				}
				refs, rerr := parseReferences(refsOut)
				if rerr != nil {
					return rerr
				}
				plan, _, perr := OffloadSessions(ctx, m.run(), st, mach, true, in.ExcludeSessions, now)
				if perr != nil {
					return perr
				}
				out = map[string]any{"dry_run": true, "plan": plan, "referenced": refs, "excluded": cleanSessionExcludes(in.ExcludeSessions)}
				return nil
			}
			plan, rec, oerr := OffloadSessions(ctx, m.run(), st, mach, false, in.ExcludeSessions, now)
			if oerr != nil {
				return oerr
			}
			out = map[string]any{"dry_run": false, "plan": plan, "record": rec, "cloud": coldStatusOf(st, mach, now)}
			return nil
		})
		return out, err

	case "disklessly_sessions_restore":
		var in struct {
			Machine string `json:"machine"`
			ID      string `json:"id"`
		}
		if err := args(&in); err != nil {
			return nil, err
		}
		err = m.store.update(func(st *State, now time.Time) error {
			mach := st.machine(in.Machine)
			if mach == nil || strings.TrimSpace(in.Machine) == "" {
				return fmt.Errorf("找不到机器 %q", in.Machine)
			}
			e, hint, rerr := RestoreCold(ctx, m.run(), st, mach, in.ID, now)
			if rerr != nil {
				return rerr
			}
			out = map[string]any{"restored": e, "path": e.Path, "resume": hint}
			return nil
		})
		return out, err

	case "disklessly_machine_upsert":
		var in struct {
			ID           string `json:"id"`
			Host         string `json:"host"`
			Name         string `json:"name"`
			User         string `json:"user"`
			Port         int    `json:"port"`
			IdentityFile string `json:"identity_file"`
		}
		if err := args(&in); err != nil {
			return nil, err
		}
		err = m.store.update(func(st *State, now time.Time) error {
			mach, uerr := st.upsertMachine(MachineInput{
				ID:           in.ID,
				Host:         in.Host,
				Name:         in.Name,
				User:         in.User,
				Port:         in.Port,
				IdentityFile: in.IdentityFile,
				Source:       "manual",
			}, now)
			if uerr != nil {
				return uerr
			}
			out = map[string]any{"machine": st.machineStatus(mach)}
			return nil
		})
		return out, err

	case "disklessly_machine_delete":
		var in struct {
			Machine string `json:"machine"`
		}
		if err := args(&in); err != nil {
			return nil, err
		}
		err = m.store.update(func(st *State, now time.Time) error {
			if derr := st.deleteMachine(in.Machine); derr != nil {
				return derr
			}
			out = map[string]any{"deleted": in.Machine}
			return nil
		})
		return out, err
	}
	return nil, fmt.Errorf("%w: %s", errNoTool, name)
}

// coldStatusOf summarizes machine mach's cold index for the assistant (without the entries).
func coldStatusOf(st *State, mach *Machine, now time.Time) map[string]any {
	cat := st.cold(mach.ID)
	res := map[string]any{"machine_id": mach.ID, "machine_name": cmp.Or(mach.Name, mach.Host)}
	if cat == nil {
		res["checked"] = false
		res["note"] = "尚未读取云端索引（disklessly_sessions_status refresh=true 或扫描机器）"
		return res
	}
	counts := map[string]int{}
	for _, e := range cat.Entries {
		counts[cat.location(e, mach, now)]++
	}
	res["checked"] = !cat.RefreshedAt.IsZero()
	res["backend"] = cat.Backend
	res["refreshed_at"] = cat.RefreshedAt
	res["indexed"] = len(cat.Entries)
	res["total"] = cat.Total
	res["capped"] = cat.Capped
	res["locations"] = counts
	res["missing_segments"] = cat.Missing
	res["unchecked_segments"] = cat.Unchecked
	res["timed_out"] = cat.TimedOut
	res["archived_bytes"] = cat.ArchivedBytes
	if cat.Error != "" {
		res["error"] = cat.Error
	}
	return res
}
