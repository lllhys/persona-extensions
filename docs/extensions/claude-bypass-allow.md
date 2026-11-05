# Bypass auto-allow (Claude Code)

[Documentation](../README.md) · [Source](../../extensions/claude-bypass-allow/)

Claude Code in `bypassPermissions` mode (`--dangerously-skip-permissions`, "bypass permissions on") still stops on some calls and waits for someone to choose **Yes**. Persona then shows the agent as waiting for you, and an unattended run, often a subagent, stalls until someone answers or Claude Code denies the call after two minutes. On the computers you choose, this extension adds a Claude Code `PermissionRequest` hook. It allows those prompts only in `bypassPermissions` sessions, only for Bash, the read-only tools and subagents, and never for a Bash command that deletes or overwrites data, forces a push or changes permissions. A `Read` rule of yours covers `Glob`, `Grep`, `LS` and `NotebookRead` too. In every other case, it stays silent and Claude Code asks as usual.

## What still prompts in bypass mode

Measured with Claude Code 2.1.308 in a throwaway project:

| Command | Prompts in bypass mode |
| --- | --- |
| `cd view && rm -f sub/*` (a relative glob after `cd`) | Yes: "Dangerous rm operation on statically-unresolvable target" |
| `for d in …; do rm -rf "$t"; done` (a target built at run time) | Yes, same check |
| A Bash command matching your own `permissions.ask` rule, such as `Bash(touch:*)` | Yes: "Permission rule … requires confirmation" |
| A 12,000-character one-liner, a heredoc, `eval`, a `for` loop, `X=view; rm -rf "$X"/x` | No |
| Read, Glob or Grep of a path outside the working directories, when reads outside them are blocked | Yes: "Read outside the working directories" |

Length alone does not trigger a prompt. Claude Code has a small set of safety checks that bypass mode does not skip. Its build calls them *bypass-immune*. For Bash, the main one is *dangerous removal*: an `rm` or `rmdir` whose target cannot be resolved before the command runs. Examples are a glob after `cd`, a variable or `$(...)` target, and an `rm` inside a `bash -c` script that Claude Code cannot parse. Claude Code runs `PermissionRequest` hooks for these prompts. The hook input includes `permission_mode`, and a hook's `allow` is honoured. When several hooks are configured, the first `allow` wins, so this hook does not wait for slower ones. Persona's own hook, which sends the prompt to your phone, is one of them.

## What it allows, and what it leaves alone

The hook answers `allow` only if all of these hold:

- The session's `permission_mode` is `bypassPermissions`, and the tool is `Bash`, a read-only tool (`Read`, `Glob`, `Grep`, `LS`, `NotebookRead`) or `Agent` (`Task` in older versions). A subagent's own calls come to the hook one by one and are judged the same way. Other modes (`default`, `acceptEdits`, `plan`, `auto`) and other tools, including MCP tools, `Write`, `Edit`, `MultiEdit`, `NotebookEdit` and `Workflow`, are never answered. The settings entry's matcher lists exactly these tools.
- For Bash, the command is not one that stays with a person. These are: deletion (`rm`, `rmdir`, `unlink`, `shred`, `rimraf`, `find -delete` (and whatever `find -exec` runs), `git clean`, `git rm`, `git branch -d`/`-D`/`-M`, `git tag -d`/`-f`, `git stash drop`/`clear`, `git worktree remove`, `git filter-branch`/`filter-repo`, `git reflog expire`, `git update-ref -d`, `rsync --delete`/`--del*`, `docker rm`/`prune`, `docker compose down -v`, `kubectl delete`, `terraform destroy`, `aws … delete-*`/`s3 rm`/`s3 rb`/`--delete`, `gcloud … delete`, `az … delete`, `gh … delete`, `gh repo archive`, `gh pr merge --delete-branch`, `gh api -X DELETE`, SQL `DROP`/`TRUNCATE`/`DELETE FROM`); overwriting data (`>`, `>|`, `N>` or `&>` into a file outside `/dev/` and `/tmp/`, `tee` without `-a` into such a file, `sed -i`/`perl -i`, `ln -f`, `mv`, `cp -f`, `dd`, `mkfs*`, `truncate`, `git reset --hard`, `git checkout -f`/`--`/`.`, `git restore`, `git switch --discard-changes`); a forced or deleting push (`--force*`, `-f`, `-d`, `--mirror`, `--delete`, `--prune`, `+ref`, `:ref`); and permission changes (`chmod`, `chown`, `chgrp`, `install -m`/`-o`/`-g`, `chattr`, `setfacl`, `usermod`, `passwd`, `visudo`, `setcap`, SQL `GRANT`/`REVOKE`/`ALTER USER`, `gh secret`/`variable`/`ssh-key`/`gpg-key`, `gh repo edit --visibility`, `gh repo deploy-key`, a `gh api` write to collaborators, permissions, branch protection, teams, members, keys, secrets, hooks or environments or that sets `visibility`/`private`, a `gh api graphql` mutation, `aws iam` and `put-*-policy`/`put-*-acl`, `gcloud … iam-policy`, `az role`, creating or changing Kubernetes roles and bindings). The command is split the way a shell splits it. A word counts as a program when it comes first or after a wrapper (`sudo`, `xargs`, `env`, `timeout`, `find -exec`, `do`, `then` and the like), so `grep -rn truncate src/` and `git mv a b` go through and `sudo -b rm x` does not. Every part of a list, pipeline or substitution counts. A quoted argument is read again as a command when a shell runs it (`bash -c`, `sh -c`, `ssh host '…'`, `eval`) or when it holds a substitution (`$(…)`, backticks), so `bash -c 'rm -rf x'` and `git commit -m "$(rm -rf x)"` stay with you. Output written with `>`, `>|`, `N>` or `&>` overwrites a file and stays with you, except into `/dev/…` or `/tmp/…`; `>>` appends and goes through. SQL counts only in a command that runs a SQL client (`psql`, `mysql`, `sqlite3` and similar), including a heredoc fed to it. Free text is not read: `git -m`/`--message`, and `gh --title`/`--body`/`--notes`, so `git commit -m 'drop rm wrapper'` goes through; neither is the body of a heredoc fed to anything but a shell or a SQL client, such as `cat > /tmp/x <<EOF`. Only a `<<` the shell reads as a heredoc starts one: inside quotes (`echo 'usage: x <<FILE'`), a comment or arithmetic (`$((1<<n))`), the lines after it are still read as commands. A command hidden in a variable, a script file or another program's input is not seen, nor is a deletion done inside a script language (`python3 -c 'shutil.rmtree(…)'`, `perl -e 'unlink …'`).
- The extension's program is running on that computer. It refreshes a heartbeat every minute. If the heartbeat is more than five minutes old, the hook allows nothing. This covers the case where the program was killed before it could remove its entry.
- None of your own `ask` or `deny` rules could apply to the call. For tools other than Bash, any rule naming the tool counts for every call of it, whatever its pattern: `Read(./secrets/**)` keeps every Read prompt with you, and a rule for `Task` counts for `Agent`. For Bash rules: The hook reads managed settings, the config directory's `settings.json` and `settings.example.json`, and `.claude/settings.json` and `.claude/settings.example.json` in the session's working directory and each parent. A rule counts when the literal start of its pattern appears anywhere in the command, either as written or after quotes, backslashes and line continuations are removed and each run of whitespace becomes one space. For example, `Bash(git push:*)` counts for `cd x && git push`, `git  push` and `'git' push`, and a bare `Bash` counts for everything. Flags in another order are not matched: `Bash(rm -rf:*)` does not count for `rm -fr` or `rm -r -f`, and Claude Code 2.1.318 does not ask for those either. This errs toward asking. Claude Code applies `deny` rules before it asks, so those calls never reach the hook; they are checked anyway.

The hook cannot see rules passed on the command line (`--settings`, `--allowedTools`/`--disallowedTools`), rules added in the session with `/permissions` and not saved, or project settings outside the session's working directory and its parents. If an `ask` rule exists only in one of those places, Claude Code's prompt for it will be auto-allowed in bypass mode. Put rules you depend on into a settings file.

The hook also answers other safety checks, such as Claude Code's isolated-peer-machine and read-block checks, when they are raised by one of the tools above. Prompts that Claude Code marks as approvable only by a person, such as edits to its own settings files or a classifier timeout in `auto` mode, ignore a hook's `allow`; this extension does not try to work around them. The `Run a dynamic workflow?` prompt is for the `Workflow` tool, which the hook never answers.

Before 0.2.0, the hook answered every Bash prompt in bypass mode, including dangerous removals, and nothing else.

This floor only decides which of Claude Code's own prompts the hook leaves to you. In `bypassPermissions` mode Claude Code runs most commands without asking at all, including a `git push --force` or an `rm` whose target it can resolve; the hook never sees those calls and cannot stop them.

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
{"matcher": "Bash|Read|Glob|Grep|LS|NotebookRead|Agent|Task", "hooks": [{"type": "command", "timeout": 10,
  "command": "'<package>/bin/claude-bypass-allow-<platform>' hook --config-dir '<config dir>' --data '<data dir>' --persona-extension=claude-bypass-allow"}]}
```

The trailing `--persona-extension=claude-bypass-allow` tag identifies the entry. The program changes only entries with that tag. Other keys and other tools' hooks keep their order and values, and the file keeps its indent. Before each change, it copies the previous file to `settings.json.claude-bypass-allow.bak`. A file that is not a JSON object is left alone and reported as an error. A session that was already running when you turned a computer on may not pick up the hook until it restarts.

## Audit log

Each auto-allow appends one JSON line to `audit.jsonl` in the program's data directory on that computer (`~/.persona/node-plugins/claude-bypass-allow/data/`, mode 0600). The log keeps about the newest 1 MB across two files. Each entry records the time, session id, subagent id (when present), working directory, config directory, the tool, the call's full length, and the call itself: the command for Bash, otherwise the tool's name and what it is about (a path, a pattern, a subagent's type and description). Before the command is stored, it is put on one line, cut to 240 characters, and has likely secrets replaced with `***`: `NAME=value` where the name suggests a token, password or key; `Bearer`/`token` values; `--password`/`--token` flags; `-p` passwords for `mysql` and `sshpass`; the password in `curl -u user:password`; credentials in URLs; and opaque strings of 32 or more characters. This masking is a heuristic, so treat the log as sensitive.

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

The tests cover the hook's decisions for each tool, the commands that stay with a person, rule matching, redaction, the audit log, editing settings.json without changing anything else, the program's install and cleanup, and the page in three languages.
