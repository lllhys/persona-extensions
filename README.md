# persona-extensions

Persona 的扩展里，**以后可以公开**的那些：不依赖任何公司内部的服务、账号或数据，代码和文档里也没有内部信息。仓库现在是 private，公开与否由维护者决定。
不打算公开的扩展在 [`nwverse/persona-integration`](https://github.com/nwverse/persona-integration)（原 `integration`），两个仓库同一套结构和脚本。
扩展怎么写、怎么装见 Persona 的 `docs/extensions.md`（改名前是 `docs/plugins.md`）；这个仓库是 nwverse/persona#1843 的阶段 2（#1847）建的。

| 扩展 | 扩展点 | 说明 |
| --- | --- | --- |
| `linuxdo` | `ui`、`connectors` | LINUX DO 的热门帖子：扩展页里按兴趣看热门 / 今日 / 本周 / 最新，点开读正文和热门回复（hub 去读，手机不用代理）；助手的工具。只读公开内容、不用账号。从 internal 搬来。见 `extensions/linuxdo/README.md` |

## 放哪个仓库

- 放这里：只用公开的服务和公开的协议，不需要公司账号、内网、内部工具；README、测试数据、提交信息里没有内网域名 / IP、内部项目名、真实的人名和消息、各种 key。
- 放 internal：需要公司内部的东西，或者拿不准能不能公开的。拿不准先放 internal，以后再搬过来（搬家就是 `git mv` 到这里、两边的索引各改一次）。

## 结构

```text
extensions/<id>/            一个扩展：清单、源码、测试，market.json（市场里的标签）
  persona-extension.json    清单
  persona-plugin.json       同一份清单的老名字：改名前的 hub 只认它，和新的保持一样（CI 查），hub 都升级后删掉
pluginkit/                  额度、声明式 UI、收件箱规则的辅助；协议本身走官方 SDK（sdk/go/ext），这里只保留扩展原来的写法
sdk/go/                     Persona 官方 Go SDK 的拷贝（Persona 仓库的 scripts/gen/sync-sdk.sh 拷来，sdk/VERSION 记着来源 commit，别手改）
scripts/                    package.sh、release.sh、index.py、manifest.py、ci.sh、sign-index.py（和 internal 的一样）
persona-extensions.json     市场索引（键 extensions），新 hub 读
persona-plugins.json        同样的条目（键 plugins、老清单），改名前的 hub 读
*.json.sig                  两份索引的签名；market.pub 是这个市场的公钥（见「签名的格式」）
```

一个 Go module（`github.com/nwverse/persona-extensions`），每个扩展是 `extensions/<id>` 下的一个 main 包。

## 加一个扩展

1. 建 `extensions/<id>/`：目录名就是清单里的 `id`（`^[a-z0-9][a-z0-9_-]{0,31}$`）。写 `persona-extension.json`（再拷一份成 `persona-plugin.json`，让改名前的 hub 也能装）、`main.go` 和测试，`market.json` 写市场里的标签。
2. `runtime.command` 列出各平台的程序（`linux-amd64`、`linux-arm64`、`darwin-arm64`、`darwin-amd64`，路径 `bin/<id>-<平台>`）：包里只有编好的程序，hub 不装任何运行时。
3. `scripts/ci.sh` 过了再提 PR；合并后 CI 发布（下面）。

## 开发

- 测试：`go test ./...`；CI 跑的全部检查：`scripts/ci.sh`（清单、gofmt、vet、测试、每个平台都能编、两份索引一致、签名对得上）。
- 打包：`scripts/package.sh <id>` → `dist/<id>-<版本>.tar.gz` 和它的 sha256。
- 在 hub 机器上装包：`personad extension install file:/path/to/<id>-<版本>.tar.gz`（改名前的 hub：`personad plugin install …`）。

## 扩展市场

这个仓库本身就是一个扩展市场：根目录的索引列出所有发布过的扩展，包放在 GitHub Release（`<id>-v<版本>`）里，用 sha256 钉住。

- **在 hub 上登记**：App 的 设置 › 扩展 › 扩展市场 › 添加，地址填 `github:nwverse/persona-extensions`；仓库是 private 时 token 填一个对本仓库 Contents 只读的 fine-grained token。
  命令行：`personad extension market add github:nwverse/persona-extensions --token`（token 从标准输入读）。
- **发布**：改扩展清单里的版本（两个名字的清单一起改），提 PR，合并到 main 后 CI 给**这次推送里版本变了的**扩展打包、建 Release、更新两份索引并提交；
  要发一个之前合并的版本，在 Actions 里手动跑 ci（填扩展 id）。CI 跑不了时在本地：`scripts/release.sh <id>`，再提交两份索引。
- **签名**（nwverse/persona#1848）：两份索引都用这个市场的 ed25519 私钥签名，hub 登记市场时记下公钥，以后验签不过、签名没了或换了 key 就不装、不升级。
  CI 发布后自动签（私钥只在仓库的 Actions secret `EXTENSION_MARKET_SIGNING_KEY` 里，只交给签名那一步）；在本地发布时把私钥放进同名环境变量，
  或 `scripts/sign-index.py --key-file <pem>`。没有私钥时 `release.sh` 打印 `unsigned`，`scripts/ci.sh` 会因为签名和索引对不上而失败：发布后一定要签。

### 签名的格式

| 文件 | 内容 |
| --- | --- |
| `market.pub` | 一行 `ed25519:<32 字节公钥的 base64>` |
| 索引顶层的 `signing_key` | 和 `market.pub` 同一行（老 hub 读索引时忽略不认识的字段） |
| `persona-extensions.json.sig`、`persona-plugins.json.sig` | `{"alg": "ed25519", "key_id": "<32 字节公钥的 sha256 的前 16 位 hex>", "sig": "<对索引文件原始字节的 ed25519 签名，base64>"}` |

签的是索引文件的原始字节（不做 JSON 规范化），所以签完不能再改索引的任何一个字节。

验证：`scripts/sign-index.py verify`（CI 跑的就是它），或者只用 openssl：

```sh
python3 - <<'PY'
import base64, json
raw = base64.b64decode(open("market.pub").read().strip().removeprefix("ed25519:"))
der = bytes.fromhex("302a300506032b6570032100") + raw
open("/tmp/market.pem", "w").write("-----BEGIN PUBLIC KEY-----\n" + base64.b64encode(der).decode() + "\n-----END PUBLIC KEY-----\n")
open("/tmp/index.sig", "wb").write(base64.b64decode(json.load(open("persona-extensions.json.sig"))["sig"]))
PY
openssl pkeyutl -verify -pubin -inkey /tmp/market.pem -rawin -in persona-extensions.json -sigfile /tmp/index.sig
```

### 私钥

- 生成：`openssl genpkey -algorithm ed25519 -out market.pem`；公钥 `ed25519:$(openssl pkey -in market.pem -pubout -outform DER | tail -c 32 | base64)` 写进 `market.pub`。
- 存放：只放仓库的 Actions secret（`gh secret set EXTENSION_MARKET_SIGNING_KEY < market.pem`），另有一份离线备份在仓库主人的密码管理器里；不进仓库、不打日志。
- 轮换（泄漏或丢了）：生成新 key，换 secret 和 `market.pub`，重新签索引并提交；已登记这个市场的 hub 会因为换了 key 拒绝刷新，
  由 hub 的主人确认后重新信任（见 Persona 的 `docs/extensions.md`「扩展市场」）。
