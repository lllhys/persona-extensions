# telegram：你自己的 Telegram 账号

用你自己的 Telegram 账号接进 Persona（Persona #1704：从 personad 自带的连接器搬到这里，行为不变）。
传输是 [gotd](https://github.com/gotd/td) 的 MTProto：hub 所在的机器经 TCP 直连 Telegram 的数据中心，不经任何 HTTP 网关。

| 扩展点 | 贡献 | 做什么 |
| --- | --- | --- |
| `connectors` | `telegram`（status、tools、actions、events、inbox、archive、login、threads） | 登录（设置 › 连接器 › Telegram）；收件箱（私聊、@我 / 回复我）；知识库归档；App 里的 Telegram 会话页（threads：分页、图片和头像经 hub 代理，按 DC 取）；助手的三个只读工具 `telegram_chats` / `telegram_messages` / `telegram_search` |
| `connectors` 的 `cli` | `tg` | 助手的命令行：连接器登录着时，跑助手的 node 在它的 PATH 上放 `tg`（`personad tool --cli telegram`）。`telegram_cli` 只解析命令行、不碰 Telegram；读写命令调的是不在模型工具列表里的 `telegram_list_chats` 等（`unlisted`），写操作照旧过 hub 的闸门 `connector_gate`，默认在手机上确认 |
| `assistant` | 技能 `telegram` | 告诉助手怎么用 `tg`（`skills/telegram/SKILL.md`）。hub 还自带同名技能时用 hub 的 |

## 登录和密钥

- api_id、api_hash 和登录后的会话是一份加密的连接器密钥，存在 hub 的数据库里（连接器 id `telegram`），不在扩展目录、不在 hub.json、不编进程序；
  App 里只显示 api_id，api_hash 打码，日志里不出现这两个值。
- 连接器 id 和原来自带的一样，所以从自带的换成扩展时不用重新登录：装上扩展，hub 先停掉自带的那个再起扩展（Persona #1780，同一个会话不会两边同时连），
  扩展拿到的是同一份密钥。

## 收件箱的分拣

- 设置项 `triage_rules`（分拣规则，扩展的标准写法）：
  - 空着时，私聊和 @你 一律「需要你」，和自带的连接器一样，不经过模型；
  - 填了，hub 的分拣模型按它给 Telegram 的条目分「需要你 / 知悉即可 / 噪音」。
- 每个条目都带兜底 `extra.rule_tier: action`（能力 `fallback`）：模型不可用时条目照样进来，算「需要你」。
  所以装这个扩展不要求先配『信息筛选』模型。

## 网络和沙箱

hub 所在的机器要能经 TCP 直连 Telegram 的数据中心（按 IP 连，不经 HTTP）。hub 的沙箱只能经出网代理走 HTTP(S)，所以这个扩展不能在沙箱里跑：
从官方市场（`persona-extensions`）装的默认不进沙箱；从别的来源（`file:`、`dir:`、其他市场）装的，装好后在 hub 机器上 `personad extension sandbox telegram off`。

## 节奏

和原来一样（Persona #965）：所有请求排队，一次一个，间隔 0.5–1 秒；Telegram 要求等待（FLOOD_WAIT）时一个请求都不发，工具回「限流中，N 秒」，
`tg` 退出码 4。会话页、图片、头像、收件箱、归档、工具都走这一个队列；每次取图、取头像记一行耗时（不记内容）。

## 测试

`go test ./extensions/telegram/...`：用假的 Telegram 客户端和合成的夹具（不用真实账号）。`plugin_test.go` 查清单（`persona-extension.json`，和 `persona-plugin.json` 一样）和程序自己声明的一样，并用官方 SDK 的 `exttest` 查进程协议（握手、错误码、hub 走了就退出）。

协议走 Persona 的官方 Go SDK（`sdk/go/ext`）：`internal/serve` 把连接器协议的 `connector.*` 方法交给连接器，密钥（`connector.secrets.*`）和变化通知（`connector.changed`）经 SDK 发给 hub。
