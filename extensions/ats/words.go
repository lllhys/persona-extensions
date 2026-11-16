package main

import (
	"fmt"
	"path"
	"strings"
)

// failureWords says why an attempt failed in the user's words. The error class is what the queue decides on;
// the user reads this instead. A failed acceptance and an agent that broke off share the class task_failed and
// are told apart by the verdict.
func failureWords(class, verdict string) string {
	switch strings.TrimSpace(class) {
	case "config_error":
		return "模型或配置不可用"
	case "auth_expired":
		return "Agent 登录失效"
	case "rate_limited":
		return "额度或限流"
	case "infra_error":
		return "电脑或环境出错"
	case "timeout":
		if verdict == VerdictFail {
			return "验收命令超时"
		}
		return "超时"
	case "lost":
		return "会话丢失"
	case "rejected":
		return "任务本身有问题（仓库或 ref 不对）"
	case "", "task_failed":
		if verdict == VerdictFail {
			return "没通过验收"
		}
		return "Agent 出错"
	}
	return "出错（" + class + "）"
}

// outcomeWords is one attempt's or one finished task's result in a few words.
func outcomeWords(state, verdict, class string) string {
	switch state {
	case StateSucceeded:
		if verdict == VerdictPass || verdict == "passed" {
			return "通过验收"
		}
		return "跑完了，没有验收命令"
	case StateFailed:
		return failureWords(class, verdict)
	case StateCanceled, StateCancelled:
		return "已取消"
	case StateExpired:
		return "已过期"
	case StateNeedsInput:
		return "等你处理"
	case StateRunning, StateLeased:
		return "在跑"
	case StateQueued:
		return "排队中"
	}
	return state
}

// phaseWords says what a running attempt is doing.
func phaseWords(phase string) string {
	switch phase {
	case PhaseWorking:
		return "Agent 在做"
	case PhaseAccepting:
		return "在跑验收命令"
	}
	return "在准备工作目录"
}

func priorityWords(p string) string { return strings.ToUpper(p) }

func permissionWords(profile string) string {
	if profile == PermissionDefault {
		return "可写"
	}
	return "只读"
}

// repoName is the last element of a repository's path: enough to tell repositories apart in a row.
func repoName(repo string) string {
	repo = strings.TrimRight(strings.TrimSpace(strings.ReplaceAll(repo, "\\", "/")), "/")
	if repo == "" {
		return ""
	}
	return path.Base(repo)
}

func shortSHA(sha string) string {
	sha = strings.TrimSpace(sha)
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// oneLine folds a text of several lines into one, cut to maxRunes.
func oneLine(s string, maxRunes int) string {
	return truncateRunes(strings.Join(strings.Fields(s), " "), maxRunes)
}

// lastReport is the task's newest evidence report, or nil.
func lastReport(t *Task) *EvidenceReport {
	if t == nil || len(t.Reports) == 0 {
		return nil
	}
	return &t.Reports[len(t.Reports)-1]
}

// lastErrorClass is the error class of the task's last failure: the task's own, or its last report's.
func lastErrorClass(t *Task) string {
	if c := strings.TrimSpace(t.FailureReason); c != "" {
		return c
	}
	if r := lastReport(t); r != nil {
		return strings.TrimSpace(r.ErrorClass)
	}
	return ""
}

// acceptanceOutput splits a report's message into its first line and the output tail under it. The dispatcher
// writes "验收命令退出码 N\n<tail>" for an acceptance command that ran.
func acceptanceOutput(message string) (head, tail string) {
	head, tail, _ = strings.Cut(strings.TrimSpace(message), "\n")
	return strings.TrimSpace(head), strings.TrimSpace(tail)
}

func attemptCount(n int) string { return fmt.Sprintf("第 %d 次", max(n, 1)) }
