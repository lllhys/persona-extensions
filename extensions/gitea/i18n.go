package main

import "strings"

type texts struct {
	reviews, open, noPRs, back, more, missingConfig, openPR, approve, comment string
	author, description, comments, noComments, review, body, save             string
}

var locales = map[string]*texts{
	"en": {
		reviews: "Pull request reviews", open: "Open PRs", noPRs: "No open pull requests in this repository.", back: "Back", more: "Load more", missingConfig: "Set the Gitea instance URL and personal access token in Settings › Extensions first.",
		openPR: "Open in Gitea", approve: "Approve", comment: "Add comment", author: "Author", description: "Description", comments: "Discussion", noComments: "No comments.", review: "Review", body: "Comment", save: "Post comment",
	},
	"ja": {
		reviews: "プルリクエストレビュー", open: "オープン PR", noPRs: "このリポジトリにオープンなプルリクエストはありません。", back: "戻る", more: "さらに読み込む", missingConfig: "設定 › 拡張機能で Gitea インスタンス URL と個人アクセストークンを設定してください。",
		openPR: "Gitea で開く", approve: "承認", comment: "コメントを追加", author: "作成者", description: "説明", comments: "ディスカッション", noComments: "コメントはありません。", review: "確認", body: "コメント", save: "コメントを投稿",
	},
	"zh-Hans": {
		reviews: "代码评审", open: "打开的 PR", noPRs: "这个仓库里没有打开的 PR。", back: "返回", more: "加载更多", missingConfig: "先在「设置 › 扩展」中填写 Gitea 实例地址和个人访问令牌。",
		openPR: "在 Gitea 中打开", approve: "批准", comment: "添加评论", author: "作者", description: "描述", comments: "讨论", noComments: "暂无评论。", review: "预览", body: "评论", save: "发表评论",
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
