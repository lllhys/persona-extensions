The tg CLI's `tg search` (unlisted): messages with the words, across private chats and groups or in one conversation, newest first, as JSON {results: [{chat, message}]}. Read-only.

```json
{
  "type": "object",
  "properties": {
    "query": {"type": "string", "description": "The words to look for"},
    "chat": {"type": "string", "description": "Only in this conversation (id or exact name)"},
    "limit": {"type": "integer", "minimum": 1, "maximum": 30, "description": "How many matches (default 20)"},
    "since": {"type": "string", "description": "Only messages from this time on (RFC 3339, or a date: midnight in the user's zone)"},
    "until": {"type": "string", "description": "Only messages before this time (RFC 3339, or a date)"}
  },
  "required": ["query"],
  "additionalProperties": false
}
```
