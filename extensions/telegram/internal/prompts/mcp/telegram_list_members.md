The tg CLI's `tg members` (unlisted): a group's members (the most recent ones of a large group), as JSON {chat, members: [{id, name, username, bot}], count}. Read-only.

```json
{
  "type": "object",
  "properties": {
    "chat": {"type": "string", "description": "The group id (g…, c…) or its exact name"},
    "limit": {"type": "integer", "minimum": 1, "maximum": 100, "description": "How many members (default 50)"}
  },
  "required": ["chat"],
  "additionalProperties": false
}
```
