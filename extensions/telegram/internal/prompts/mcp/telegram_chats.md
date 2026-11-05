List the user's recent Telegram conversations (private chats and groups; not channels or bots), newest first: each one's key (for telegram_messages and telegram_search), name, whether it is a private chat or a group, how many messages are unread and how many of those mention the user, and its last message. Read-only.

```json
{
  "type": "object",
  "properties": {
    "limit": {"type": "integer", "minimum": 1, "maximum": 100, "description": "How many conversations (default 30)"},
    "unread_only": {"type": "boolean", "description": "Only conversations with unread messages"}
  },
  "additionalProperties": false
}
```
