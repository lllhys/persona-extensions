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

## 扩展

有哪些扩展请直接看 [`extensions/`](extensions/) 目录，每个扩展的说明在各自的目录里，较长的指南在 [`docs/extensions/`](docs/extensions/)（英文）。

## 文档与贡献

以下详细文档默认使用英文：

- [文档索引](docs/README.md)
- [开发指南](docs/development.md)
- [清单、配置和协议参考](docs/reference.md)
- [发布与市场签名指南](docs/releases.md)
- [贡献指南](CONTRIBUTING.md)

文档与人工维护的代码注释默认使用英文。中文 README 与英文版对应；运行时本地化文案和有业务语义的测试数据保留原语言。
