package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Restoring an archived (已上云) session runs the disklessly CLI on this computer:
// disklessly --json [--state-dir DIR] sessions restore <entryId>.

var restoreTimeout = 5 * time.Minute

const exitBackendUnreachable = 4

// RestoreResult is what a successful restore returns to the page and the MCP tool.
type RestoreResult struct {
	OK        bool   `json:"ok"`
	EntryID   string `json:"entry_id"`
	Path      string `json:"path"`
	SessionID string `json:"session_id,omitempty"`
	Resume    string `json:"resume,omitempty"`
}

// disklesslyAppCLI is the CLI inside the macOS app bundle, where the disklessly extension also looks.
var disklesslyAppCLI = "/Applications/Disklessly.app/Contents/MacOS/disklessly"

// restoreKillGrace is how long a restore waits for the CLI's pipes to close after it was killed.
var restoreKillGrace = 5 * time.Second

// findDisklessly locates the disklessly CLI: DISKLESSLY_BIN, PATH, ~/.disklessly/bin, ~/.local/bin,
// /usr/local/bin, /opt/homebrew/bin, then the macOS app bundle.
func findDisklessly() (string, error) {
	if b := strings.TrimSpace(os.Getenv("DISKLESSLY_BIN")); b != "" {
		if fi, err := os.Stat(b); err == nil && !fi.IsDir() {
			return b, nil
		}
		return "", fmt.Errorf("DISKLESSLY_BIN=%s 不存在", b)
	}
	if p, err := exec.LookPath("disklessly"); err == nil {
		return p, nil
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		for _, p := range []string{
			filepath.Join(home, ".disklessly", "bin", "disklessly"),
			filepath.Join(home, ".local", "bin", "disklessly"),
			"/usr/local/bin/disklessly",
			"/opt/homebrew/bin/disklessly",
			disklesslyAppCLI,
		} {
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
				return p, nil
			}
		}
	}
	return "", errors.New("未找到 disklessly 命令行工具（可设置 DISKLESSLY_BIN）")
}

// errRestoreBusy: the entry is being restored already (by the page or the MCP server, which are separate processes).
var errRestoreBusy = errors.New("该会话正在恢复中，请稍候")

// lockRestore takes an exclusive, non-blocking per-entry lock in lockDir (the extension's data directory; the
// system temp directory when empty). The kernel drops the lock when the process exits.
func lockRestore(lockDir, entryID string) (unlock func(), err error) {
	dir := cmp.Or(lockDir, os.TempDir())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "restore-"+entryID+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errRestoreBusy
		}
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// restoreArchived restores an entry of this computer's text index to its original path, holding the entry's
// restore lock in lockDir so the same entry is never restored twice at once.
func restoreArchived(ctx context.Context, st *State, entryID, lockDir string) (*RestoreResult, error) {
	entryID = strings.TrimSpace(entryID)
	if !validEntryID.MatchString(entryID) {
		return nil, fmt.Errorf("无效的 entry_id %q", entryID)
	}
	rec := findArchivedSession(st, entryID)
	if rec == nil {
		return nil, fmt.Errorf("本机没有已上云会话 %s（可能已恢复，或来自其他设备）", entryID)
	}
	bin, err := findDisklessly()
	if err != nil {
		return nil, err
	}
	unlock, err := lockRestore(lockDir, entryID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	// Restored meanwhile (by a restore that held the lock just before)?
	if findArchivedSession(st, entryID) == nil {
		return nil, fmt.Errorf("会话 %s 已恢复到 %s", entryID, rec.FilePath)
	}
	args := []string{"--json"}
	if rec.StateDir != "" && !isDisklesslyDefaultStateDir(rec.StateDir) {
		args = append(args, "--state-dir", rec.StateDir)
	}
	args = append(args, "sessions", "restore", entryID)

	ctx, cancel := context.WithTimeout(ctx, restoreTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	// The CLI may be a wrapper script: on timeout kill its whole process group, and stop waiting for pipes
	// that a stray grandchild still holds once restoreKillGrace has passed.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = restoreKillGrace
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()

	var reply struct {
		OK        bool   `json:"ok"`
		Path      string `json:"path"`
		ID        string `json:"id"`
		SessionID string `json:"sessionId"`
		Error     *struct {
			Code    any    `json:"code"`
			Message string `json:"message"`
			Name    string `json:"name"`
		} `json:"error"`
	}
	parsed := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &reply) == nil

	var exitErr *exec.ExitError
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		return nil, fmt.Errorf("恢复超时（超过 %s），稍后再试", restoreTimeout)
	case errors.As(runErr, &exitErr) && exitErr.ExitCode() == exitBackendUnreachable:
		return nil, errors.New("云端不可达，稍后再试")
	case runErr == nil && parsed && reply.OK:
		sid := cmp.Or(reply.SessionID, rec.SessionID)
		return &RestoreResult{
			OK:        true,
			EntryID:   cmp.Or(reply.ID, entryID),
			Path:      cmp.Or(reply.Path, rec.FilePath),
			SessionID: sid,
			Resume:    resumeCommand(rec.Harness, sid),
		}, nil
	case parsed && reply.Error != nil && reply.Error.Message != "":
		return nil, fmt.Errorf("恢复失败：%s", reply.Error.Message)
	case runErr != nil:
		msg := strings.TrimSpace(stderr.String())
		if r := []rune(msg); len(r) > 300 {
			msg = string(r[len(r)-300:])
		}
		return nil, fmt.Errorf("恢复失败：%v %s", runErr, msg)
	default:
		return nil, errors.New("恢复失败：disklessly 没有返回可识别的结果")
	}
}

// restoreConfirm is the question asked before restoring an archived session.
func restoreConfirm(path, harness, sessionID string) string {
	q := "从云端恢复到原路径 " + cmp.Or(path, "（原路径）") + "？"
	if r := resumeCommand(harness, sessionID); r != "" {
		q += "恢复后可运行 " + r
	}
	return q
}
