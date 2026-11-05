# Bypass auto-allow (Claude Code)

[Documentation](../README.md) · [Source](../../extensions/claude-bypass-allow/)

Claude Code in `bypassPermissions` mode (`--dangerously-skip-permissions`, "bypass permissions on") still stops on some Bash calls and waits for someone to choose **Yes**. Persona then shows the agent as waiting for you, and an unattended run, often a subagent, stalls until someone answers or Claude Code denies the call after two minutes. On the computers you choose, this extension adds a Claude Code `PermissionRequest` hook. It allows those prompts only for Bash calls in `bypassPermissions` sessions. In every other case, it stays silent and Claude Code asks as usual.

## What still prompts in bypass mode

Measured with Claude Code 2.1.308 in a throwaway project:

| Command | Prompts in bypass mode |
| --- | --- |
| `cd view && rm -f sub/*` (a relative glob after `cd`) | Yes: "Dangerous rm operation on statically-unresolvable target" |
| `for d in …; do rm -rf "$t"; done` (a target built at run time) | Yes, same check |
| A Bash command matching your own `permissions.ask` rule, such as `Bash(touch:*)` | Yes: "Permission rule … requires confirmation" |
| A 12,000-character one-liner, a heredoc, `eval`, a `for` loop, `X=view; rm -rf "$X"/x` | No |

Length alone does not trigger a prompt. Claude Code has a small set of safety checks that bypass mode does not skip. Its build calls them *bypass-immune*. For Bash, the main one is *dangerous removal*: an `rm` or `rmdir` whose target cannot be resolved before the command runs. Examples are a glob after `cd`, a variable or `$(...)` target, and an `rm` inside a `bash -c` script that Claude Code cannot parse. Claude Code runs `PermissionRequest` hooks for these prompts. The hook input includes `permission_mode`, and a hook's `allow` is honoured. When several hooks are configured, the first `allow` wins, so this hook does not wait for slower ones. Persona's own hook, which sends the prompt to your phone, is one of them.

## What it allows, and what it leaves alone

The hook answers `allow` only if all of these hold:

- The session's `permission_mode` is `bypassPermissions` and the tool is `Bash`. Other modes (`default`, `acceptEdits`, `plan`, `auto`) and other tools, including MCP tools, Write and Edit, are never answered.
- The extension's program is running on that computer. It refreshes a heartbeat every minute. If the heartbeat is more than five minutes old, the hook allows nothing. This covers the case where the program was killed before it could remove its entry.
- None of your own Bash `ask` or `deny` rules could apply to the command. The hook reads managed settings, the config directory's `settings.json` and `settings.example.json`, and `.claude/settings.json` and `.claude/settings.example.json` in the session's working directory and each parent. A rule counts when the literal start of its pattern appears anywhere in the command. For example, `Bash(git push:*)` counts for `cd x && git push`, and a bare `Bash` counts for everything. This errs toward asking. Claude Code applies `deny` rules before it asks, so those calls never reach the hook; they are checked anyway.

The hook cannot see rules passed on the command line (`--settings`, `--allowedTools`/`--disallowedTools`), rules added in the session with `/permissions` and not saved, or project settings outside the session's working directory and its parents. If an `ask` rule exists only in one of those places, Claude Code's prompt for it will be auto-allowed in bypass mode. Put rules you depend on into a settings file.

The hook also answers safety checks that are not removals, such as Claude Code's isolated-peer-machine and read-block checks, but only when they are raised by a Bash call. A bypass-mode session has already accepted running arbitrary Bash; this extension removes the remaining interruptions for Bash only.

## Persona's own approval card

In a session that Persona started, Persona's hook also receives each `PermissionRequest` and puts a card on your phone. Claude Code does not stop that hook when another hook allows the call, so the card would stay up and the agent would be shown as waiting for you even though the command already ran. After each allow, this hook therefore tells Persona's node on that computer through its socket (`~/.persona/node.sock`, kind `hook.decided`, with the same request input). A node that knows `hook.decided` (nwverse/persona#2648) then shows no card, or withdraws the card it already showed. On an older node, the card stays until it is answered or times out; the command runs either way.

## Settings and switches

| Setting | Meaning |
| --- | --- |
| `machines` | The computers where the hook is installed, one switch per macOS or Linux computer. No computer is on after installation; turning one on asks for confirmation first. |
| `config_dirs` | Claude Code config directories that receive the hook. Defaults to `~/.claude`; `~` is that computer's home directory. Add each directory you use with `CLAUDE_CONFIG_DIR`. A missing directory is skipped and shown on the extension page. |

The extension does nothing until a computer is switched on. You can turn it off for one computer with its switch, or for every computer by disabling the extension. In either case, the program removes its entry as it stops. Uninstalling or upgrading does the same.

## The settings entry

The program adds one group to `hooks.PermissionRequest` in `<config dir>/settings.json`. If the file is a symlink, it edits the file the link points to:

```json
{"matcher": "Bash", "hooks": [{"type": "command", "timeout": 10,
  "command": "'<package>/bin/claude-bypass-allow-<platform>' hook --config-dir '<config dir>' --data '<data dir>' --persona-extension=claude-bypass-allow"}]}
```

The trailing `--persona-extension=claude-bypass-allow` tag identifies the entry. The program changes only entries with that tag. Other keys and other tools' hooks keep their order and values, and the file keeps its indent. Before each change, it copies the previous file to `settings.json.claude-bypass-allow.bak`. A file that is not a JSON object is left alone and reported as an error. A session that was already running when you turned a computer on may not pick up the hook until it restarts.

## Audit log

Each auto-allow appends one JSON line to `audit.jsonl` in the program's data directory on that computer (`~/.persona/node-plugins/claude-bypass-allow/data/`, mode 0600). The log keeps about the newest 1 MB across two files. Each entry records the time, session id, subagent id (when present), working directory, config directory, the command's full length, and the command itself. Before the command is stored, it is put on one line, cut to 240 characters, and has likely secrets replaced with `***`: `NAME=value` where the name suggests a token, password or key; `Bearer`/`token` values; `--password`/`--token` flags; credentials in URLs; and opaque strings of 32 or more characters. This masking is a heuristic, so treat the log as sensitive.

The extension's page in **Settings → Extensions** lists, for each switched-on computer, whether the hook is installed in each config directory and the 30 newest auto-allows.

## Permissions

- **Runs a program on the hub**: only to draw the extension page. It reaches the computers through `nodes.call`, has no network access and holds no hub token.
- **Runs a program on the computers you choose** (`node`), with `files` access. It edits Claude Code's `settings.json` outside its own data directory. It needs no network access and no secrets.

## Install

Use the [public market](../installation.md), or package locally from the repository root:

```sh
scripts/package.sh claude-bypass-allow
go test ./extensions/claude-bypass-allow
```

The tests cover the hook's decisions, rule matching, redaction, the audit log, editing settings.json without changing anything else, the program's install and cleanup, and the page in three languages.
