# Bypass auto-allow (Claude Code)

Claude Code in bypassPermissions mode still stops on some Bash calls, mostly an `rm` whose target it cannot resolve, and an unattended agent waits there. On the computers you choose, this extension installs a Claude Code `PermissionRequest` hook that allows those calls: only Bash, only in bypassPermissions sessions, never one of your own ask or deny rules. Each allow is logged with the command cut and secrets masked.

- [Extension guide: what triggers the prompts, what is allowed, limits](../../docs/extensions/claude-bypass-allow.md)
- [Installation](../../docs/installation.md)
- [Development and tests](../../docs/development.md)
- [Manifest](persona-extension.json)
