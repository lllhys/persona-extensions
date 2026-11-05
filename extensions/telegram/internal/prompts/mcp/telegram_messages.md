Read one Telegram conversation's messages, oldest first: time, sender and text (replies, forwards and media are described in words; photos and files are not opened). Name the conversation by its key from telegram_chats (u…, g…, c…) or its exact name. To go further back, pass the id the answer gives as `before`. Read-only: nothing is marked as read.

```json
{
  "type": "object",
  "properties": {
    "chat": {"type": "string", "description": "The conversation's key from telegram_chats, or its exact name"},
    "limit": {"type": "integer", "minimum": 1, "maximum": 50, "description": "How many messages (default 30)"},
    "before": {"type": "integer", "description": "Only messages older than this id (from a previous answer)"}
  },
  "required": ["chat"],
  "additionalProperties": false
}
```
