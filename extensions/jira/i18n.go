package main

import "strings"

type texts struct {
	issues, recent, search, create, review, confirm, back, open                               string
	project, status, assignee, priority, description, comments, noIssues, missingConfig, more string
}

var locales = map[string]*texts{
	"en": {
		issues: "My Jira issues", recent: "Recently updated", search: "Search issues", create: "New issue", review: "Review and create", confirm: "Confirm create", back: "Back", open: "Open in Jira",
		project: "Project", status: "Status", assignee: "Assignee", priority: "Priority", description: "Description", comments: "Comments", noIssues: "No unresolved assigned issues.", missingConfig: "Set the Jira instance URL and personal access token in Settings › Extensions first.", more: "Load more",
	},
	"ja": {
		issues: "自分の Jira 課題", recent: "最近の更新", search: "課題を検索", create: "新しい課題", review: "確認して作成", confirm: "作成を確認", back: "戻る", open: "Jira で開く",
		project: "プロジェクト", status: "ステータス", assignee: "担当者", priority: "優先度", description: "説明", comments: "コメント", noIssues: "未解決の担当課題はありません。", missingConfig: "設定 › 拡張機能で Jira インスタンス URL と個人アクセストークンを設定してください。", more: "さらに読み込む",
	},
	"zh-Hans": {
		issues: "我的 Jira 工作项", recent: "最近更新", search: "搜索工作项", create: "新建工作项", review: "复核并创建", confirm: "确认创建", back: "返回", open: "在 Jira 中打开",
		project: "项目", status: "状态", assignee: "负责人", priority: "优先级", description: "描述", comments: "评论", noIssues: "没有分配给你的未完成工作项。", missingConfig: "先在「设置 › 扩展」中填写 Jira 实例地址和个人访问令牌。", more: "加载更多",
	},
}

func textsFor(locale string) *texts {
	if t := locales[locale]; t != nil {
		return t
	}
	if i := strings.IndexAny(locale, "-_"); i > 0 {
		if t := locales[locale[:i]]; t != nil {
			return t
		}
	}
	return locales["en"]
}
