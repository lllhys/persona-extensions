package main

import "strings"

type texts struct {
	issues, noIssues, projects, noProjects, create, back, more, missingConfig string
	subject, description, journals, noJournals, review, project               string
}

var locales = map[string]*texts{
	"en": {
		issues: "My open Redmine issues", noIssues: "No open issues assigned to you.", projects: "Projects", noProjects: "No visible projects.", create: "Create issue", back: "Back", more: "Load more", missingConfig: "Set the Redmine instance URL and API key in Settings › Extensions first.",
		subject: "Subject", description: "Description", journals: "Journals", noJournals: "No journal notes.", review: "Review", project: "Project",
	},
	"ja": {
		issues: "自分の未解決 Redmine 課題", noIssues: "自分に割り当てられた未解決の課題はありません。", projects: "プロジェクト", noProjects: "表示できるプロジェクトはありません。", create: "課題を作成", back: "戻る", more: "さらに読み込む", missingConfig: "設定 › 拡張機能で Redmine インスタンス URL と API キーを設定してください。",
		subject: "件名", description: "説明", journals: "履歴", noJournals: "履歴メモはありません。", review: "確認", project: "プロジェクト",
	},
	"zh-Hans": {
		issues: "我的 Redmine 开放工作项", noIssues: "没有分配给你的开放工作项。", projects: "项目", noProjects: "没有可见的项目。", create: "创建工作项", back: "返回", more: "加载更多", missingConfig: "先在「设置 › 扩展」中填写 Redmine 实例地址和 API key。",
		subject: "主题", description: "描述", journals: "处理记录", noJournals: "暂无处理记录。", review: "预览", project: "项目",
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
