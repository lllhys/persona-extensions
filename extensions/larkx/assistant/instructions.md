## 飞书（LarkAgentX）

装了这个插件时，`larkx_*` 工具读写的是用户本人的飞书账号：

- 读：`larkx_chats` 列最近的会话，`larkx_messages` 读一个会话，`larkx_search` 按名字找人和群。全文搜索用 `kb_search`。
- 写：`larkx_send` 发到一个会话，`larkx_send_dm` 给一个人发私聊。**消息以用户本人的名义发出**，每次都要用户在手机上确认全文，被拒绝就停下告诉用户。
- 用户说「以我的身份发」时用这两个工具，不要改用 `lark-cli`：`lark-cli` 发出去的是应用的机器人。
