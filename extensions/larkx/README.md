# Feishu through LarkAgentX (larkx)

Feishu as yourself, through [LarkAgentX](https://github.com/cv-cat/LarkAgentX): sign in to the Feishu web client with a QR code, receive every message over its long connection into the inbox and the knowledge base, and send as yourself after you confirm the full text on the phone. Write `/agent <what to do>` in any chat to hand it to the assistant, whose answer appears under it, as you, marked as an AI answer. A Python program; Persona needs no change.

Read this before installing:

- **Unofficial protocol.** LarkAgentX reverse-engineers the Feishu web client. It can break whenever Feishu changes, it may not fit Feishu's terms of use, and the account can be flagged. Whether to use it is the account owner's call; for a company account, check the company's rules first.
- **Not in the package.** LarkAgentX has no license, so neither this repository nor the package carries its code. On first start the program downloads it from GitHub at the commit in [`src/larkx_persona/upstream.json`](src/larkx_persona/upstream.json) and checks every file's SHA-256, then installs its dependencies from the hash-locked [`requirements.txt`](requirements.txt).
- **The sign-in is a full web session.** It stays in the hub's encrypted secret store, never in a file or the log.

- [Extension guide](../../docs/extensions/larkx.md)
- [Installation](../../docs/installation.md)
- [Development and tests](../../docs/development.md#add-a-python-extension)
- [Manifest](persona-extension.json)
