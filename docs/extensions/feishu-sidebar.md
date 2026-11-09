# Feishu Sidebar

[Documentation](../README.md) · [Source](../../extensions/feishu-sidebar/)

Open the user's Persona web from a Feishu sidebar. The extension builds a Feishu applink from the hub address the user configured; opening the link in Feishu opens Persona in a half-width sidebar. It needs no Feishu app id, makes no request to Feishu, and keeps no credentials.

## How it works

Feishu's general link (no app id, no web app):

```text
https://applink.feishu.cn/client/web_url/open?mode=sidebar-semi&url=<percent-encoded target URL>
```

`mode=sidebar-semi` opens a half-width sidebar (Feishu 3.41+); `window` and `appCenter` are for Feishu 7.5+ but are not used here. The `web_app/open` form needs a web app already created, so it is not used either.

The extension percent-encodes the target URL the way JavaScript's `encodeURIComponent` does (Chinese, `?`, `#`, `&` and `=` all encoded), so the query and hash of the target path are carried whole.

## Configuration

| Setting | Purpose |
| --- | --- |
| `hub_url` | **Required.** The address Feishu's built-in browser can reach the hub at, such as `https://persona.example.com`. The page and `status` stay `not_configured` until it is set. |
| `path` | The same-site path to open by default, such as `/` or `/j/<conversation-id>`; starts with `/`. Default `/`. |

A trailing `/` on the address is normalized away. Only same-site relative paths are accepted (they start with `/`, are not a full or scheme-relative URL, and have no `..` segment); a path that fails these is refused rather than escaped into a different site.

## Contributions

| Contribution | Behavior |
| --- | --- |
| `ui` `feishu-sidebar` (`nav_page`) | The page shows the built applink (an "open in Feishu" link plus the raw link to copy), the target it opens, and the notes below. With no address it shows what to fill in instead. |
| `connector` `feishu` (`status`, `tools`) | `feishu_sidebar_link` builds the applink for a path (default: the setting). `status` is `not_configured` when the address is missing. |

## Notes shown to the user

- Feishu 3.41+ is required (`sidebar-semi`); the phone Feishu may have no sidebar.
- The first open asks for a login inside Feishu's built-in browser: the web sign-in is separate from the app's.
- The extension never calls Feishu and keeps no credentials; the link is built locally.

## Package and install

Use the [public market](../installation.md), or package locally from the repository root:

```sh
scripts/package.sh feishu-sidebar
```

Run the tests:

```sh
go test ./extensions/feishu-sidebar
```

The [manifest](../../extensions/feishu-sidebar/persona-extension.json) declares its contributions and platforms.
