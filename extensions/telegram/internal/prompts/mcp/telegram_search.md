Search the user's Telegram messages for words, across private chats and groups or within one conversation (`chat`: its key from telegram_chats or its exact name), newest first: each match's conversation and key, time, sender and text. Read-only.

```json
{
  "type": "object",
  "properties": {
    "query": {"type": "string", "description": "Words to look for"},
    "chat": {"type": "string", "description": "Only this conversation: its key from telegram_chats, or its exact name"},
    "limit": {"type": "integer", "minimum": 1, "maximum": 30, "description": "How many matches (default 20)"}
  },
  "required": ["query"],
  "additionalProperties": false
}
```
