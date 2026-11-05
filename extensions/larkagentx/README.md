# Feishu / LarkAgentX adapter

[Full guide](../../docs/extensions/larkagentx.md) · [Project overview](../../README.md)

An experimental, independent `larkagentx` connector: authorized WebSocket text reception into Persona's inbox, always-confirmed text sends, QR/import login and local logout. It does not replace larkintegration.

The hub package contains four platform-specific Go executables. The original thin Python helper is embedded and can be extracted with `companion-script`; it runs on a separate user-configured machine with Python 3.9+ and user-installed, pinned LarkAgentX. No upstream source or generated protobuf assets are redistributed. Review the licensing and service-policy limits before installing upstream.

Real-account interoperability and market publication have not been validated or completed. See the full guide for installation, credentials, confirmation, deduplication, platform scope and local synthetic tests.
