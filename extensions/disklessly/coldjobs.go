package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
)

// The hub gives a UI request about 12 seconds, and anything on the Agent 对话 page that talks to
// a machine over SSH can take minutes (each refresh script has a three-minute budget, a restore
// downloads from the backend, an offload scans and uploads). Those actions therefore start a
// background job and return at once. A job works on a private copy of its machine's state
// (scratch) without holding the store lock, so renders and other machines are never blocked,
// and merges the result back under the lock when it ends. One job runs per machine at a time;
// at most coldParallel run at once. Job state lives in memory only: it says what is running
// and how the last job of each machine ended.

const coldParallel = 4

// Job kinds.
const (
	jobRefresh = "refresh"
	jobRestore = "restore"
	jobOffload = "offload"
	jobInstall = "install"
	jobBrew    = "brew"
	jobScan    = "scan"
	jobClean   = "clean"
)

// brewTimeout bounds a Homebrew install: tap, download and verify.
const brewTimeout = 15 * time.Minute

// coldJob is the running or last finished job of one machine.
type coldJob struct {
	Kind     string
	Label    string // what is being done, shown while it runs
	Started  time.Time
	Finished time.Time // zero while running
	Note     string    // the outcome
	Err      string    // why it failed, for people
	Raw      string    // what the machine or the CLI said, for 详情
}

func (j coldJob) running() bool { return j.Finished.IsZero() }

type coldJobs struct {
	mu   sync.Mutex
	jobs map[string]*coldJob // by machine id
	sem  chan struct{}
	wg   sync.WaitGroup
}

// jobSnapshot copies the job table for one render.
func (a *app) jobSnapshot() map[string]coldJob {
	a.jobs.mu.Lock()
	defer a.jobs.mu.Unlock()
	out := make(map[string]coldJob, len(a.jobs.jobs))
	for id, j := range a.jobs.jobs {
		out[id] = *j
	}
	return out
}

// waitJobs waits until no job runs (tests, shutdown).
func (a *app) waitJobs() { a.jobs.wg.Wait() }

// scratch is a private copy of one machine's state that a job works on outside the store lock.
type scratch struct {
	st   *State
	m    *Machine
	orig Machine
	scan *Scan
	cold *ColdCatalog
	// coldBefore is an independent baseline: cold may itself be edited in place by the job, so pointer comparison
	// cannot distinguish a real change from an untouched scratch copy.
	coldBefore *ColdCatalog
}

func scratchOf(st *State, m *Machine) *scratch {
	mc := *m
	sc := &scratch{st: &State{Machines: []*Machine{&mc}, Scans: map[string]*Scan{}}, m: &mc, orig: *m}
	if s := st.Scans[m.ID]; s != nil {
		sc.st.Scans[m.ID] = s
		sc.scan = s
	}
	if c := st.cold(m.ID); c != nil {
		sc.cold = cloneCold(c)
		sc.coldBefore = cloneCold(c)
		sc.st.setCold(m.ID, sc.cold)
	}
	return sc
}

// apply merges what the job changed into the current state: the machine's probe fields, its
// scan, its cold index and new history records. A machine removed meanwhile is left out.
func (sc *scratch) apply(st *State) {
	m := st.machine(sc.orig.ID)
	if m == nil || m.ID != sc.orig.ID {
		return
	}
	n := sc.m
	if n.Reachable != sc.orig.Reachable || n.Platform != sc.orig.Platform || n.BinaryVersion != sc.orig.BinaryVersion ||
		n.ScanMode != sc.orig.ScanMode || n.LastError != sc.orig.LastError || !n.UpdatedAt.Equal(sc.orig.UpdatedAt) {
		m.Reachable, m.Platform, m.BinaryVersion, m.ScanMode, m.LastError, m.UpdatedAt =
			n.Reachable, n.Platform, n.BinaryVersion, n.ScanMode, n.LastError, n.UpdatedAt
	}
	if s := sc.st.Scans[m.ID]; s != nil && s != sc.scan {
		if st.Scans == nil {
			st.Scans = map[string]*Scan{}
		}
		st.Scans[m.ID] = s
	}
	if c := sc.st.Cold[m.ID]; !reflect.DeepEqual(c, sc.coldBefore) {
		if c == nil {
			st.dropCold(m.ID)
		} else {
			st.setCold(m.ID, c)
		}
	}
	if len(sc.st.History) > 0 {
		st.History = append(slices.Clone(sc.st.History), st.History...)
		if len(st.History) > maxHistoryItems {
			st.History = st.History[:maxHistoryItems]
		}
	}
}

// jobWork runs a job on its scratch state and returns the outcome to show.
type jobWork func(ctx context.Context, sc *scratch, now time.Time) (string, error)

// startJob starts a background job on machine id unless one is already running there; then it
// returns that job and started false.
func (a *app) startJob(id, kind, label string, work jobWork) (started bool, cur coldJob, err error) {
	a.jobs.mu.Lock()
	if a.jobs.jobs == nil {
		a.jobs.jobs = map[string]*coldJob{}
		a.jobs.sem = make(chan struct{}, coldParallel)
	}
	prev := a.jobs.jobs[id]
	if prev != nil && prev.running() {
		cur = *prev
		a.jobs.mu.Unlock()
		return false, cur, nil
	}
	job := &coldJob{Kind: kind, Label: label, Started: a.now()}
	a.jobs.jobs[id] = job
	sem := a.jobs.sem
	a.jobs.wg.Add(1)
	a.jobs.mu.Unlock()

	var sc *scratch
	err = a.db().read(func(st *State) {
		if m := st.machine(id); m != nil && m.ID == id {
			sc = scratchOf(st, m)
		}
	})
	if err == nil && sc == nil {
		err = fmt.Errorf("找不到机器 %q", id)
	}
	if err != nil {
		a.jobs.mu.Lock()
		if prev != nil {
			a.jobs.jobs[id] = prev
		} else {
			delete(a.jobs.jobs, id)
		}
		a.jobs.mu.Unlock()
		a.jobs.wg.Done()
		return false, coldJob{}, err
	}
	a.jobs.mu.Lock()
	cur = *job
	a.jobs.mu.Unlock()
	go func() {
		defer a.jobs.wg.Done()
		sem <- struct{}{}
		defer func() { <-sem }()
		note, werr := work(context.Background(), sc, a.now().UTC())
		uerr := a.db().update(func(st *State, _ time.Time) error {
			sc.apply(st)
			return nil
		})
		a.jobs.mu.Lock()
		job.Finished = a.now()
		job.Note = note
		switch {
		case werr != nil:
			job.Err, job.Raw = humanizeErr(werr)
		case uerr != nil:
			job.Err = "保存结果失败：" + uerr.Error()
		}
		a.jobs.mu.Unlock()
		if hub := a.hubRef(); hub != nil {
			_ = hub.Invalidate("")
		}
	}()
	return true, cur, nil
}

// startRefresh refreshes machine id's cold index in the background.
func (a *app) startRefresh(id string) (bool, coldJob, error) {
	runner := a.run()
	return a.startJob(id, jobRefresh, "正在读取会话索引与备份状态", func(ctx context.Context, sc *scratch, now time.Time) (string, error) {
		cat, err := RefreshColdIndex(ctx, runner, sc.st, sc.m, now)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("已读取 %d 个会话", len(cat.Entries)), nil
	})
}

// busyNote says that machine name already has a job running.
func busyNote(name string, j coldJob) string {
	return fmt.Sprintf("「%s」%s，稍候页面会自动更新", name, cmp.Or(j.Label, "正在处理"))
}

// coldAction starts a cold-session action that talks to machines and returns the toast; the
// work goes on in the background. machine "" or "all" means every enabled machine (refresh only).
func (a *app) coldAction(_ context.Context, action, machine, id string) (string, error) {
	runner := a.run()
	type target struct{ id, name string }
	var targets []target
	var entry *ColdEntry
	var offer installOffer
	var srcName, brew string
	err := a.db().read(func(st *State) {
		if action == "cold-refresh" && (machine == "" || machine == allMachines) {
			for _, m := range st.Machines {
				if m.Enabled {
					targets = append(targets, target{m.ID, machineLabel(m)})
				}
			}
			return
		}
		m := st.machine(cmp.Or(machine, "local"))
		if m == nil || machine == allMachines {
			return
		}
		targets = []target{{m.ID, machineLabel(m)}}
		switch action {
		case "cold-restore":
			if cat := st.cold(m.ID); cat != nil {
				if i := slices.IndexFunc(cat.Entries, func(e ColdEntry) bool { return e.ID == id || e.SessionID == id }); i >= 0 {
					e := cat.Entries[i]
					entry = &e
				}
			}
		case "cold-install":
			offer = installSourceFor(st, m)
			if offer.src != nil {
				srcName = machineLabel(offer.src)
			}
		case "cold-brew":
			if cat := st.cold(m.ID); cat != nil {
				brew = cat.Brew
			}
		}
	})
	if err != nil {
		return "", err
	}
	if len(targets) == 0 {
		if action == "cold-refresh" {
			return "没有启用的机器", nil
		}
		return "", fmt.Errorf("找不到机器 %q", machine)
	}

	t := targets[0]
	switch action {
	case "cold-refresh":
		var started, busy []string
		for _, t := range targets {
			ok, _, err := a.startRefresh(t.id)
			switch {
			case err != nil:
				busy = append(busy, t.name+"："+err.Error())
			case ok:
				started = append(started, t.name)
			default:
				busy = append(busy, t.name)
			}
		}
		if len(started) == 0 {
			return "已在读取：" + clip(strings.Join(busy, "、"), 80) + "，稍候页面会自动更新", nil
		}
		toast := fmt.Sprintf("已开始读取 %d 台机器的会话索引与备份状态，完成后页面自动更新", len(started))
		if len(busy) > 0 {
			toast += "；" + clip(strings.Join(busy, "、"), 60) + " 已在进行中"
		}
		return toast, nil

	case "cold-offload", "archive-sessions":
		ok, cur, err := a.startJob(t.id, jobOffload, "正在上传冷会话", func(ctx context.Context, sc *scratch, now time.Time) (string, error) {
			_, rec, err := OffloadSessions(ctx, runner, sc.st, sc.m, false, nil, now)
			if err != nil {
				return "", err
			}
			return "冷会话已上云：" + rec.Summary, nil
		})
		if err != nil {
			return "", err
		}
		if !ok {
			return busyNote(t.name, cur), nil
		}
		return "已开始上传「" + t.name + "」的冷会话（读回校验后才删本地），完成后在机器状态里显示结果", nil

	case "cold-restore":
		id = strings.TrimSpace(id)
		if id == "" || strings.HasPrefix(id, "-") || strings.ContainsAny(id, "\r\n\x00") {
			return "", fmt.Errorf("无效的会话 ID %q", id)
		}
		if entry != nil && !entry.Local {
			// Refused at once, as RestoreCold would: another device's row belongs to that device.
			return "", fmt.Errorf("会话 %q 来自另一台设备（%s），原路径属于那台设备；请在原设备上恢复，或在终端运行 disklessly sessions restore %s --to <本机路径>",
				cmp.Or(entry.Title, entry.ID), cmp.Or(entry.DeviceID, "未知设备"), entry.ID)
		}
		title := id
		if entry != nil {
			title = cmp.Or(entry.Title, entry.SessionID, entry.ID)
		}
		ok, cur, err := a.startJob(t.id, jobRestore, "正在恢复会话「"+clipRunes(title, 30)+"」", func(ctx context.Context, sc *scratch, now time.Time) (string, error) {
			e, hint, err := RestoreCold(ctx, runner, sc.st, sc.m, id, now)
			if err != nil {
				return "", err
			}
			note := "已恢复到 " + e.Path
			if hint != "" {
				note += "，可运行 " + hint
			}
			return note, nil
		})
		if err != nil {
			return "", err
		}
		if !ok {
			return busyNote(t.name, cur), nil
		}
		return "已开始恢复「" + clipRunes(title, 30) + "」，完成后在机器状态里显示结果", nil

	case "cold-install":
		if offer.src == nil {
			return "", errors.New(cmp.Or(offer.reason, "没有可以复制的 disklessly CLI"))
		}
		if want := strings.TrimSpace(id); want != "" && want != offer.src.ID {
			return "", fmt.Errorf("可复制的来源已经变了（现在是 %s），请重新确认", srcName)
		}
		return a.startInstall(t.id, t.name, offer)

	case "cold-brew":
		if brew == "" {
			return "", fmt.Errorf("「%s」上没有找到 Homebrew，没法用 Homebrew 安装；先点「重新检查 %s」，或按页面上的命令手动安装", t.name, t.name)
		}
		ok, cur, err := a.startJob(t.id, jobBrew, "正在用 Homebrew 安装 disklessly", func(ctx context.Context, sc *scratch, now time.Time) (string, error) {
			return brewInstall(ctx, runner, sc, now)
		})
		if err != nil {
			return "", err
		}
		if !ok {
			return busyNote(t.name, cur), nil
		}
		return "已开始在「" + t.name + "」上用 Homebrew 安装 disklessly，装好后自动重新检查", nil
	}
	return "", fmt.Errorf("unknown action %q", action)
}

// startInstall copies the CLI from offer.src to machine id in the background, then refreshes it.
func (a *app) startInstall(id, name string, offer installOffer) (string, error) {
	runner, streamer := a.run(), a.stream()
	src := *offer.src
	srcName := machineLabel(&src)
	ver := cmp.Or(shortVersion(offer.cli.Version), "disklessly")
	ok, cur, err := a.startJob(id, jobInstall, "正在从 "+srcName+" 复制安装 disklessly "+ver, func(ctx context.Context, sc *scratch, now time.Time) (string, error) {
		return copyInstallWork(ctx, runner, streamer, &src, offer.cli, sc, now)
	})
	if err != nil {
		return "", err
	}
	if !ok {
		return busyNote(name, cur), nil
	}
	return "已开始从「" + srcName + "」复制安装到「" + name + "」，完成后自动重新检查", nil
}

// copyInstallWork copies cli from src to sc's machine, then re-reads that machine's cold index.
func copyInstallWork(ctx context.Context, runner Runner, streamer StreamRunner, src *Machine, cli ColdCLI, sc *scratch, now time.Time) (string, error) {
	srcName := machineLabel(src)
	ver := cmp.Or(shortVersion(cli.Version), "disklessly")
	o, err := copyInstall(ctx, streamer, src, sc.m, cli.Home)
	if err != nil {
		return "", fmt.Errorf("从 %s 复制安装失败：%w", srcName, err)
	}
	note := "已从 " + srcName + " 复制安装 " + cmp.Or(o.version, ver)
	if o.prev != "" {
		note += "，原来的保留在 " + o.prev
	}
	if o.shadow != "" {
		note += "；但这台电脑仍先找到 " + o.shadow + "，请删除它或调整 PATH 后再检查"
	}
	_, _ = RefreshColdIndex(ctx, runner, sc.st, sc.m, now)
	return note, nil
}

// brewInstall installs or upgrades the CLI with Homebrew on sc's machine, then re-reads it.
func brewInstall(ctx context.Context, runner Runner, sc *scratch, now time.Time) (string, error) {
	name := machineLabel(sc.m)
	out, err := runner(ctx, sc.m, brewScript(), brewTimeout)
	o := parseBrew(out)
	switch {
	case err != nil && !o.ok && o.step == "":
		return "", err
	case !o.ok:
		return "", brewFailure(name, o)
	}
	ver := "disklessly " + cmp.Or(shortVersion(o.version), "（版本未知）")
	note := "已用 Homebrew " + brewVerb(o.op) + " " + ver
	if o.same {
		note = "Homebrew 里的 " + ver + " 已是最新，没有重新下载"
	}
	if o.keg != "" {
		note += "（" + o.keg + "）"
	}
	if o.bin != "" && !strings.Contains(o.binTo, "/Cellar/"+brewFormula+"/") && !strings.Contains(o.binTo, "/opt/"+brewFormula+"/") {
		// Homebrew's bin/disklessly is someone else's (the Disklessly app's engine, say): a
		// terminal with Homebrew on PATH runs that one; the extension's lookup prefers the keg.
		other := o.bin
		if o.binTo != "" {
			other += " → " + o.binTo
		}
		if v := shortVersion(o.binVer); v != "" {
			other += "（disklessly " + v + "）"
		}
		note += "；终端里（PATH 含 Homebrew 的 bin）用的仍是 " + other + "，扩展用的是 Homebrew 装的这个；想让终端也用它，运行 brew link --overwrite " + brewFQ
	}
	switch o.shadow {
	case "":
	case "?":
		note += "；但检查脚本在 PATH 和常见位置都找不到它，把 Homebrew 的 bin 目录加进这台电脑的 PATH 后点「重新检查」"
	default:
		note += "；但这台电脑仍先找到 " + o.shadow + "（另一份 disklessly），删除它或把 Homebrew 的目录排到 PATH 前面后点「重新检查」"
	}
	_, _ = RefreshColdIndex(ctx, runner, sc.st, sc.m, now)
	return note, nil
}
