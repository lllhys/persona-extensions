package main

import "strings"

type texts struct {
	issues, noIssues, projects, noProjects, create, back, more, missingConfig string
	summary, description, comments, noComments, review, project, reporter     string
}

var locales = map[string]*texts{
	"en": {
		issues: "My unresolved YouTrack issues", noIssues: "No unresolved issues assigned to you.", projects: "Projects", noProjects: "No active projects are visible.", create: "Create issue", back: "Back", more: "Load more", missingConfig: "Set the YouTrack instance URL and permanent token in Settings › Extensions first.",
		summary: "Summary", description: "Description", comments: "Comments", noComments: "No comments.", review: "Review", project: "Project", reporter: "Reporter",
	},
	"ja": {
		issues: "自分の未解決 YouTrack 課題", noIssues: "自分に割り当てられた未解決の課題はありません。", projects: "プロジェクト", noProjects: "アクティブなプロジェクトはありません。", create: "課題を作成", back: "戻る", more: "さらに読み込む", missingConfig: "設定 › 拡張機能で YouTrack インスタンス URL と永続トークンを設定してください。",
		summary: "概要", description: "説明", comments: "コメント", noComments: "コメントはありません。", review: "確認", project: "プロジェクト", reporter: "報告者",
	},
	"zh-Hans": {
		issues: "我的 YouTrack 未解决工作项", noIssues: "没有分配给你的未解决工作项。", projects: "项目", noProjects: "没有可见的活跃项目。", create: "创建工作项", back: "返回", more: "加载更多", missingConfig: "先在「设置 › 扩展」中填写 YouTrack 实例地址和永久令牌。",
		summary: "摘要", description: "描述", comments: "评论", noComments: "暂无评论。", review: "预览", project: "项目", reporter: "报告人",
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
