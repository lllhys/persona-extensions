# Persona Extensions

[English](README.md)

Persona 的公开扩展：接入数据源，为助手添加工具和技能，并贡献由各端 App 渲染的界面。本仓库也是一个扩展市场，版本化安装包发布在 GitHub Releases 中。

## 快速开始

在 Persona App 中打开 **设置 → 扩展 → 扩展市场 → 添加**，输入：

```text
github:nwverse/persona-extensions
```

然后选择扩展安装。这个公开市场不需要 GitHub token。

也可以在 hub 所在的机器上通过命令行登记市场：

```sh
personad extension market add github:nwverse/persona-extensions
```

本地安装包、旧版 hub 兼容以及扩展特有的设置见[安装说明](docs/installation.md)（英文）。

## 主要扩展

| 扩展 | 能力 |
| --- | --- |
| [LINUX DO](docs/extensions/linuxdo.md) | 只读公开的热门、今日、本周和最新帖子；按兴趣过滤；在 App 中或通过助手工具阅读正文与热门回复。不需要账号。 |
| [Telegram](docs/extensions/telegram.md) | 接入你自己的账号：会话、收件箱、知识库归档、助手工具和 `tg` 命令行。写操作默认需要确认，凭据只加密保存在 hub 上。 |
| [Showcase](docs/extensions/showcase.md) | 纯本地的界面示例：每种组件与状态、小组件、快捷指令，以及各端共用的截图输入。 |
| [Starter kit](docs/extensions/starter-kit.md) | 不跑程序的扩展示例：周回顾技能、助手说明、快捷提示、收件箱分拣模板和主题色。 |
| [Starter pack](docs/extensions/starter-kit.md#starter-pack) | 扩展合集，一次安装 starter-kit 和 LINUX DO。 |

## 文档与贡献

以下详细文档默认使用英文：

- [文档索引](docs/README.md)
- [开发指南](docs/development.md)
- [清单、配置和协议参考](docs/reference.md)
- [发布与市场签名指南](docs/releases.md)
- [贡献指南](CONTRIBUTING.md)

文档与人工维护的代码注释默认使用英文。中文 README 与英文版对应；运行时本地化文案和有业务语义的测试数据保留原语言。
