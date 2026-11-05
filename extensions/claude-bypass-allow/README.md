# Bypass auto-allow (Claude Code)

Claude Code in bypassPermissions mode still stops on some calls, such as a read outside the working directories, and an unattended agent waits there. On the computers you choose, this extension installs a Claude Code `PermissionRequest` hook that allows those calls for Bash, the read-only tools (Read, Glob, Grep, LS, NotebookRead) and subagents (Agent), only in bypassPermissions sessions. Bash commands that delete or overwrite data, force a push or change permissions, every other tool, and your own ask and deny rules still ask. Each allow is logged with the command cut and secrets masked.

- [Extension guide: what triggers the prompts, what is allowed, limits](../../docs/extensions/claude-bypass-allow.md)
- [Installation](../../docs/installation.md)
- [Development and tests](../../docs/development.md)
- [Manifest](persona-extension.json)
