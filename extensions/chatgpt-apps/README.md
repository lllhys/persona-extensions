# ChatGPT apps

After you sign in to ChatGPT on the phone (a device code), the Persona assistant can use the apps connected to your ChatGPT account (mail, calendar, code hosting, brokerage, documents and others) and the ChatGPT plugin marketplace. Reads run directly; every change the assistant makes (sending mail, placing order instructions, deleting, installing or removing a plugin) waits for your approval on the phone, every time. Settings › Extensions › ChatGPT 应用 › 插件 browses the marketplace, your installed plugins and your connected apps, and installs or uninstalls plugins yourself.

It uses ChatGPT's own web backend, which is not a public API, and the Codex CLI's sign-in, so the sign-in page says Codex. Use it at your own risk under OpenAI's terms (see the guide). The tokens stay in the extension and the hub's encrypted secrets; the assistant never sees them.

- [Extension guide](../../docs/extensions/chatgpt-apps.md)
- [Installation](../../docs/installation.md)
- [Development and tests](../../docs/development.md)
- [Manifest](persona-extension.json)
