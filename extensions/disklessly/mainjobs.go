package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The 机器磁盘 page's scans and cleanups run as background jobs like the cold-session ones: a
// scan may take 90 seconds and a cleanup minutes, while a UI request has about 12.

// startScan scans machine id in the background.
func (a *app) startScan(id string) (bool, coldJob, error) {
	runner := a.run()
	return a.startJob(id, jobScan, "正在扫描磁盘与可回收缓存", func(ctx context.Context, sc *scratch, now time.Time) (string, error) {
		s, err := ScanMachine(ctx, runner, sc.st, sc.m, now)
		if err != nil {
			return "", err
		}
		if s.Partial {
			return fmt.Sprintf("水位已更新：根分区已用 %.0f%%；缓存太多，没在 %d 秒内统计完，可回收空间只算了一部分", usedPercent(s), int(probeTimeout/time.Second)), nil
		}
		return fmt.Sprintf("扫描完成：根分区已用 %.0f%%，可回收 %s", usedPercent(s), FormatBytes(s.ReclaimableBytes)), nil
	})
}

func usedPercent(s *Scan) float64 {
	if s == nil || s.Mount.TotalBytes == 0 {
		return 0
	}
	return float64(s.Mount.UsedBytes) * 100 / float64(s.Mount.TotalBytes)
}

// mainJob starts the scan or cleanup that action r asks for and returns the toast.
func (a *app) mainJob(r *ext.UIRequest, p *page) (string, error) {
	runner := a.run()
	if r.Action == "scan-all" {
		type target struct{ id, name string }
		var targets []target
		if err := a.db().read(func(st *State) {
			for _, m := range st.Machines {
				if m.Enabled {
					targets = append(targets, target{m.ID, machineLabel(m)})
				}
			}
		}); err != nil {
			return "", err
		}
		var started, busy []string
		for _, t := range targets {
			ok, _, err := a.startScan(t.id)
			switch {
			case err != nil:
				busy = append(busy, t.name+"（"+userMessage(err)+"）")
			case ok:
				started = append(started, t.name)
			default:
				busy = append(busy, t.name)
			}
		}
		if len(started) == 0 {
			if len(busy) == 0 {
				return "没有启用的机器", nil
			}
			return "这些机器正在处理别的任务：" + clip(strings.Join(busy, "、"), 80) + "，稍候页面会自动更新", nil
		}
		toast := fmt.Sprintf("已开始扫描 %d 台机器，完成后页面自动更新", len(started))
		if len(busy) > 0 {
			toast += "；" + clip(strings.Join(busy, "、"), 60) + " 正在处理别的任务"
		}
		return toast, nil
	}

	targetID := r.Values["m"]
	if targetID == "" {
		targetID = p.Machine
	}
	if targetID == "" {
		targetID = "local"
	}
	var id, name string
	if err := a.db().read(func(st *State) {
		if m := st.machine(targetID); m != nil {
			id, name = m.ID, machineLabel(m)
		}
	}); err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("找不到机器 %q", targetID)
	}
	p.Machine = id

	if r.Action == "scan" {
		ok, cur, err := a.startScan(id)
		if err != nil {
			return "", err
		}
		if !ok {
			return busyNote(name, cur), nil
		}
		return "已开始扫描「" + name + "」，完成后页面自动更新", nil
	}

	var cats, items []string
	includeReview := false
	label := "正在执行一键安全清理"
	switch r.Action {
	case "clean-category":
		c := strings.TrimSpace(r.Values["cat"])
		if c == "" {
			return "", fmt.Errorf("未指定清理分类")
		}
		cats, includeReview = []string{c}, true
		label = "正在清理「" + CategoryLabels[c] + "」"
	case "clean-item":
		it := strings.TrimSpace(r.Values["item"])
		if it == "" {
			return "", fmt.Errorf("未指定清理项 ID")
		}
		items, includeReview = []string{it}, true
		label = "正在清理一项"
	case "clean-selected":
		for _, cid := range CategoryIDs {
			if r.Values["cat_"+cid] == "true" {
				cats = append(cats, cid)
			}
		}
		if len(cats) == 0 {
			return "", fmt.Errorf("请至少勾选一个要清理的分类")
		}
		includeReview = r.Values["include_review"] == "true"
		label = fmt.Sprintf("正在按勾选的 %d 个分类清理", len(cats))
	}
	ok, cur, err := a.startJob(id, jobClean, label, func(ctx context.Context, sc *scratch, now time.Time) (string, error) {
		_, rec, err := ExecuteClean(ctx, runner, sc.st, sc.m, cats, items, includeReview, false, nil, now)
		if err != nil {
			return "", err
		}
		return "清理完成，可用空间 " + rec.Summary, nil
	})
	if err != nil {
		return "", err
	}
	if !ok {
		return busyNote(name, cur), nil
	}
	return "已开始清理「" + name + "」，完成后在机器下方显示结果并记入台账", nil
}

// busy reports a job running on machine m.
func (d draw) busy(m *Machine) bool {
	j, ok := d.jobs[m.ID]
	return ok && j.running()
}

// machineJobItems draws, under a machine on the 机器磁盘 page, its running job, how its last
// scan or cleanup ended and, after a failed probe, the problem with its command and 详情.
func (d draw) machineJobItems(m *Machine) []wire.Component {
	var items []wire.Component
	j, hasJob := d.jobs[m.ID]
	if hasJob && j.running() {
		return []wire.Component{{Type: wire.ComponentText, Style: wire.TextStyleSecondary,
			Text: fmt.Sprintf("%s…（%s开始，完成后页面自动更新）", j.Label, d.when(j.Started))}}
	}
	var raw string
	if hasJob && (j.Kind == jobScan || j.Kind == jobClean) {
		if j.Err != "" && m.LastError != "" && j.Kind == jobScan {
			// The failed scan is the problem drawn below; say it once.
		} else {
			items = append(items, d.jobOutcome(j)...)
			raw = j.Raw
		}
	}
	if m.LastError != "" {
		p := sshProblem(m, m.LastError)
		// Here the button that tries again is 扫描.
		p.Reason, p.Next = strings.ReplaceAll(p.Reason, "「重试」", "「扫描」"), strings.ReplaceAll(p.Next, "「重试」", "「扫描」")
		if sandboxed() && p.Cause == causeSandbox {
			// The banner at the top already says it and gives the command.
			items = append(items, wire.Component{Type: wire.ComponentText, Style: wire.TextStyleSecondary, Text: clip("上次扫描没有成功："+p.Reason, 300)})
		} else {
			items = append(items, wire.Component{Type: wire.ComponentText, Style: wire.TextStyleSecondary, Text: clip("上次扫描没有成功："+p.text(), 400)})
			items = append(items, commandItem(detailsKey("scan", m.ID), p.Command)...)
		}
		raw = strings.TrimSpace(p.Raw + "\n" + raw)
	}
	return append(items, d.detailsItems(detailsKey("scan", m.ID), raw)...)
}
