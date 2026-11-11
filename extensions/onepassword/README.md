# 1Password

Connect Persona to the 1Password vaults you choose, with a 1Password service account. The assistant can then list those vaults, find items by title, tag or website, and see which fields an item has together with each field's `op://` secret reference. No secret value ever leaves the extension: when it describes an item, 1Password's SDK hands the whole item to the extension's process, which keeps only names and types and drops the values at once, so no password, key, note or one-time code reaches the assistant, the knowledge base, the Inbox or a log. Nothing it does changes your vaults.

The service account token is the extension's secret, encrypted on the hub and checked with 1Password before it is saved. Settings › Extensions › 1Password › 连接 shows whether 1Password accepts it and which vaults it can read.

This first version is read-only. Using `op://` references in Persona's settings and handing a secret to an Agent's run after a phone approval need host support first ([nwverse/persona#3962](https://github.com/nwverse/persona/issues/3962)); connecting through the `op` CLI on one of your computers is tracked in [#376](https://github.com/nwverse/persona-extensions/issues/376).

- [Extension guide](../../docs/extensions/onepassword.md)
- [Installation](../../docs/installation.md)
- [Development and tests](../../docs/development.md)
- [Manifest](persona-extension.json)
