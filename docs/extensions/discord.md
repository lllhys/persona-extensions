# Discord

[Documentation](../README.md) · [Source](../../extensions/discord/)

Connect Discord to Persona through **your own bot**. Discord's terms leave no other way for ordinary users: a user token ("self-bot") gets the account banned, and OAuth access to someone's messages is only for partners Discord approves. So Persona reads what your bot can read: the channels of the servers you add it to, with the permissions you give it there. Direct messages between other people and you are out of reach.

This is the first step (D1) of the plan tracked in [nwverse/persona#917](https://github.com/nwverse/persona/issues/917): sign-in, status, the knowledge-base archive and read-only tools. The Inbox (mentions and replies), a "Send to Persona" message command and a conversation page come in later versions. It sends no messages.

| Extension point | Contribution | Behavior |
| --- | --- | --- |
| `connectors` | `discord` | Sign-in with a bot token, status with a health check, the knowledge-base archive (`archive`, read every 5 minutes) and the read-only tools `discord_channels`, `discord_messages` and `discord_search`. |

## Setting up the bot

1. In the [Discord Developer Portal](https://discord.com/developers/applications), create an application.
2. On its **Bot** page, turn on **Message Content Intent** (a small bot needs no review for it) and turn off **Public Bot**, so that only you can add it to servers. Then **Reset Token** and copy the token.
3. In Persona, open **Settings → Connectors → Discord**, sign in and paste the token. The extension asks Discord who the token belongs to: the application, its bot and its owner (for an application owned by a team, the team's owner). The owner is you: your own messages are marked as yours.
4. Add the bot to your servers with the invite link. It asks only for **View Channels** and **Read Message History**. Adding a bot to a server needs the **Manage Server** permission there, so it is your own server or one whose admins agreed.

Signing in never waits on the rest: if Message Content Intent is still off, or the bot is in no server yet, the sign-in succeeds and the connector's status says what is left (with the invite link), then turns ok by itself within a few minutes once it is done. The invite link is also in the answer of `discord_channels`, for adding the bot to another server later.

The token is kept as the connector's encrypted secret in the hub's database (connector id `discord`), never in the extension directory, a log, the status or a tool's answer. Signing out deletes it and stops every request.

What is read is decided in Discord: a channel where the bot may not **View Channels** or **Read Message History** is not read. There is no channel picker in Persona.

## What goes into the knowledge base

- One conversation per channel, thread or forum post, `discord-<channel id>`, named `Server #channel` or `Server #channel › thread`. Text and announcement channels, their threads and forum posts are read; voice channels' chat and direct messages are not.
- **The first time** a channel is seen, `history_days` back (default 30, at most 500 messages per channel); also after a gap longer than that (the archive was off for months), rather than everything since. Threads are read when they are active or were archived within that window.
- **After that**, new messages, every 5 minutes (the hub's poll). Each pass asks Discord for the bot's servers, their channels and active threads, and reads only the channels whose last message moved past the archive's position: a quiet channel costs no request.
- **Messages**: who sent them (a display name), what they said with mentions, channels, roles, custom emoji and Discord timestamps made readable, what a reply answers, what a forward forwards, short notes for attachments, stickers, polls and link cards (with their fields), and the text of bots' component messages; a new thread is noted, other system messages (joins, boosts, pins) are left out. Edits and deletions are not followed. Attachments are noted by name; their files are not downloaded yet.
- **No people**: messages carry the sender's name only, never a sender id or handle, so the knowledge base records no identities from Discord. Discord's developer policy forbids profiling its users.

### Nothing skipped, nothing lost

The archive's position (per conversation, the last message archived; per channel, when its archived threads were last listed) is kept by the hub and only moves past what was read. The only thing the extension keeps in memory is which conversations to leave alone for a while: a restart repeats some requests, never skips a message.

- **Message Content Intent off**: Discord then sends every message empty. A pass checks the application's flags first and reads nothing while it is off; once it is back on, the next pass reads from where it stopped. (Turning it off in the seconds between that check and a read is not caught.)
- **No Read Message History, or the newest messages deleted**: Discord shows nothing new. The position stays; the conversation is tried again on its next message, or after six hours, so its history is read once the permission is given.
- **One conversation failing** (Discord refusing it, failing on it) holds up only that conversation: the others are read, the status stays ok, and it is tried again on its next message or after 15 minutes (six hours for a refusal: many refused requests get an IP banned). A message that cannot be decoded is left out without ending the read; a page with none that can is not read past.
- **A gap longer than the window** (the archive was off for months) reads from the window's start rather than everything since; a conversation never read before is read back over the window, 500 messages at most.
- **Archived threads**: each channel's archived threads are listed every 50 minutes at most (a thread archives after an hour of quiet at the earliest), the first time those archived within the window, then those since an hour before its last listing. A listing goes a page per call and is kept in the archive's position per channel: it moves back past a thread once that thread was read, so a server joined later is listed over the whole window, a listing the hub could not keep is simply done again, and a large forum costs a request per call, not its whole list again each time. A thread held back (it failed, was refused or showed nothing) does not stop the listing: it is kept as owed in the position and read on its own once it can be. Archived private threads are not read (Discord lists them only to moderators); of more than 100 threads archived at the very same millisecond, some can be missed.
- **Starting over** (the position lost) reads the window again; the knowledge base drops messages it already has, so nothing is duplicated.
- **Long reads**: a call reads at least one conversation, then stops after about 25 seconds, 20 conversations or about 2 MB and lets the hub call again. Conversations read before (furthest behind first) and those never read take turns, so neither a busy server nor a newly joined one holds up the other.

## Assistant tools

| Tool | What it does |
| --- | --- |
| `discord_channels` | The servers and the channels and active threads the bot can read, most recently active first, with their ids, and the invite link |
| `discord_messages` | Messages in one channel, thread or forum post (archived ones too), oldest first, paging back with `before` |
| `discord_search` | Discord's own message search, in one server or the first five. While Discord is still indexing a server (it answers 202), the answer says so and points at the knowledge base |

The tools answer in English, their notes (attachments, replies) too. Their descriptions are [prompts/mcp/](../../extensions/discord/prompts/mcp/). Answers name people, never their ids. Each message starts with a line of its time, place and sender and its id; the place (a thread's name is anyone's words) and the sender's display name are quoted, "(the user)" stands outside the quotes, and what it says follows on indented lines (any kind of line break). Server, channel and thread names are kept on one line everywhere. So neither a name nor a message's text can pass for the user or for another message. What people wrote is data for the assistant, not instructions.

## Status

The hub hides a connector's tools and stops its archive while its status is not ok, so only what needs you, or will not pass by itself, changes it. A failed call or a rate limit does not.

| State | Meaning |
| --- | --- |
| `ok` | Signed in and usable. |
| `not_signed_in` | No token yet, or Discord no longer accepts it (it was reset or the application deleted). Nothing asks Discord until you sign in again. |
| `no_permission` | Message Content Intent is off, or the bot is in no server yet (the guide has the invite link). |
| `rate_limited` | Two health checks in a row were asked to wait by Discord. |
| `error` | Two health checks in a row could not reach Discord (or the proxy setting is not usable). |

A health check (the application and the bot's first server, two requests) runs every 10 minutes while all is well and every minute while not, so what you fix in Discord shows within a minute without a restart.

## Network and sandbox

The extension uses only HTTPS to `discord.com`, the one host in its manifest, so it can run in Persona's sandbox: then it goes out through the hub's egress proxy, which honors the hub's own `HTTPS_PROXY`.

When the sandbox is off and the hub's machine cannot reach Discord, set the extension's **proxy** secret: `http://`, `https://`, `socks5://` or `socks5h://`, with a host and port, optionally `user:password@`. A proxy that does not work fails every request rather than being bypassed. Without it, the environment's `HTTPS_PROXY` applies.

The bot shows as offline in Discord: D1 keeps no Gateway connection.

## Design notes

- **REST is the source of truth.** Every pass and every tool call asks Discord's REST API what the bot can read (cached for two minutes), so nothing is kept up from events and nothing drifts. A Gateway connection would not make the archive faster: the hub reads a connector when its poll interval has passed, whatever the connector says. The Gateway comes with the step that needs it ("Send to Persona" interactions), and then only as a doorbell whose failure never takes the connector down.
- **No Discord library.** A small REST client of its own (`api.go`) keeps to Discord's rate limits from the response headers, retries a short 429 once and answers a longer one instead of waiting it out. disgo was tried first: its Gateway client races under `go test -race`, its search waits for minutes while Discord indexes, `after=0` is silently dropped and it logs response bodies.
- Being HTTP only, it can later become a TypeScript source extension without waiting for WebSocket support there.

## Local validation

```sh
go -C extensions/discord test -race ./...
scripts/package.sh discord
```

The tests run against a fake Discord REST API (`httptest`, synthetic servers, channels and messages; no account or token) and the SDK's protocol checks. They cover sign-in and sign-out, a restart with the kept sign-in, a failed read of it tried again and a late one not replacing a newer sign-in, sign-in with Message Content off and with no server (and the status turning ok by itself), Message Content found off by the archive, the first read and its window, refused channels, forum posts and archived threads (in a server joined later, after a cursor the hub could not keep, in a held channel, with a failing server, over several listing pages), new messages only after that, nothing read or moved while Message Content is off, a channel without history read once allowed, a deleted newest message not holding up the next one, a quiet channel with a deleted newest message costing nothing, one failing conversation not holding up the others, an unreadable message not ending a read, an emptied archived thread, an unreadable cursor and a gap longer than the window, a refused token stopping every request, failures, route and global rate limits, the per-call time and size limits, pruning, conversations read before and new ones taking turns, a large forum listed a page per call, an unreadable archived post owed without stopping the listing, the tools (archived posts, a failing server in a search, output that cannot be forged by a display name, a thread's name or a line break, English notes), rendering (embed fields, component messages), decoding and the proxy. They also check that no message identifies its sender and that the token never reaches the logs, the status or a tool's answer.

Not checked here: a real Discord account and server. Before relying on it, sign in with a real bot, invite it to a test server and check the status, the archive and the tools.
