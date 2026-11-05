# 组件展示（showcase）

扩展界面的每一种组件、每一种状态各一页，给写扩展的人看效果，也给各端（网页、安卓、iOS、Mac）做截图对齐（nwverse/persona#1852）。纯本地数据，不出网、不要密钥，`permissions` 为空。

| 贡献 | 是什么 |
| --- | --- |
| `ui` `showcase`（`nav_page`） | 首页是目录，每种组件一行，点进去（push）是那一页 |
| `ui` `showcase-card`（`overview_card`） | 总览上的卡片：并排的数 + 小柱状图，点开首页 |
| `ui` `showcase-widget`（`widget`） | 小组件 / 表盘：small、medium、large、三种 accessory，各画各的 |
| `shortcuts` `showcase-echo`、`showcase-count` | 快捷指令：原样说回一句话；数到一个数并回一张结果卡片（数字、选择、开关三种参数） |

页面：文字、数字、进度、行（角标、图标 / 头像 / 缩略图）、按钮（危险的先确认）、链接、输入（五种 + 提交）、图表（柱状、叠起来、折线、负数）、
并排的数（1–6 个）、分段选择、Markdown、表格（窄表、8 列宽表）、空状态、加载中、分隔线、列表（搜索、筛选、加载更多，60 条每次 20 条；
行 → 详情 → 编辑 → 保存后返回）、invalidate（先回加载中，3 秒后发 `ui.invalidate` 让 App 重画）。UI 版本 3 以前的 App：列表换成输入框和按钮，
push 由 SDK 换成「在原地画新页」，每页末尾有「回到目录」。

## 快照：各端截图的输入

`snapshots/<页>.json` 是每一页画出来的 View（UI 版本 3），各端的截图测试直接读它们画，不用连 hub；persona 主仓库的
`schema/extension/examples/showcase/` 有同样的一份。改了页面之后：

```sh
go test ./extensions/showcase -run TestSnapshots -update   # 重新生成快照
go test ./extensions/showcase                              # 每页都过 schema 校验、快照和页面一致、每种组件都出现过
```

再把 `snapshots/` 拷到主仓库的 `schema/extension/examples/showcase/`（主仓库的测试会校验它们）。

一致性测试：`personad extension test extensions/showcase`。
