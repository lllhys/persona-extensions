# Persona Extensions

[English](README.md)

Persona 的公开扩展：接入数据源，为助手添加工具和技能，并贡献由各端 App 渲染的界面。本仓库也是一个扩展市场，使用单文件嵌入签名目录，以及 GHCR 上按摘要固定的 OCI 安装包。初始目录格式转换暂时保留清洗后冻结的 Release 引用，直到获批的切换引导发布和匿名公开校验完成；格式转换本身不发布安装包。

新目录的签名还覆盖前一份目录和源码清单，PR CI 会对照真实 base commit 校验。next 期间只检查不发布；切换完成后，最终仓库身份自动启用可信 main 发布，无需额外仓库设置。包可见性是唯一需要手动修改的包设置。目录延续校验、不可变版本和每次目录更新的独立 issue 见[发布说明](docs/releases.md)（英文）。

[![Persona Extensions 宣传片：加一行地址，再挑需要的扩展](docs/assets/promo-preview-zh.webp)](#视频介绍)

[观看视频介绍（含配音）](#视频介绍)

## 视频介绍

https://github.com/user-attachments/assets/172bb2bb-6c28-401e-aae2-695f67e8356d

[下载高清版和字幕](https://github.com/nwverse/persona-extensions/releases/tag/promo-persona-extensions-v1)

## 快速开始

在 Persona App 中打开 **设置 → 扩展 → 扩展市场 → 添加**，输入：

```text
github:nwverse/persona-extensions
```

然后选择扩展安装。这个公开市场不需要 GitHub token。hub 需要支持嵌入签名目录和 OCI，并使用当前官方公钥 pin；旧版 hub 仍可安装本地包。

也可以在 hub 所在的机器上通过命令行登记市场：

```sh
personad extension market add github:nwverse/persona-extensions
```

本地安装包、旧版 hub 兼容以及扩展特有的设置见[安装说明](docs/installation.md)（英文）。

## 扩展

有哪些扩展请直接看 [`extensions/`](extensions/) 目录，每个扩展的说明在各自的目录里，较长的指南在 [`docs/extensions/`](docs/extensions/)（英文）。

bypass 扩展的源码现已更名为 `agent-bypass-allow`（原 `claude-bypass-allow`）。同市场更新需要明确确认身份变更，并保留已安装的设置、规则和数据。请先等安装包和目录发布，并升级 hub 与节点；不要用卸载重装来迁移。见[扩展说明](docs/extensions/agent-bypass-allow.md#upgrading-from-the-former-id)（英文）。

## 文档与贡献

以下详细文档默认使用英文：

- [文档索引](docs/README.md)
- [开发指南](docs/development.md)
- [清单、配置和协议参考](docs/reference.md)
- [发布与市场签名指南](docs/releases.md)
- [贡献指南](CONTRIBUTING.md)

文档与人工维护的代码注释默认使用英文。中文 README 与英文版对应；运行时本地化文案和有业务语义的测试数据保留原语言。
