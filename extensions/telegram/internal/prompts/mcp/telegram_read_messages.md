The tg CLI's `tg messages` (unlisted): one page of a conversation's messages, oldest first, as JSON {chat, messages: [{id, at, from, from_id, mine, type, text, reply_to, mentioned}], next_before}. `before` (the last answer's next_before) goes further back; `since` / `until` bound the time. Read-only: nothing is marked as read.

```json
{
  "type": "object",
  "properties": {
    "chat": {"type": "string", "description": "The conversation id from tg chats (u…, g…, c…), or its exact name"},
    "limit": {"type": "integer", "minimum": 1, "maximum": 50, "description": "How many messages (default 30)"},
    "before": {"type": "integer", "description": "Only messages older than this id (next_before of the last page)"},
    "since": {"type": "string", "description": "Only messages from this time on (RFC 3339, or a date: midnight in the user's zone)"},
    "until": {"type": "string", "description": "Only messages before this time (RFC 3339, or a date)"}
  },
  "required": ["chat"],
  "additionalProperties": false
}
```
