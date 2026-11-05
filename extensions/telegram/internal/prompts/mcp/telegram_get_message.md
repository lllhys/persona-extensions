The tg CLI's `tg message` (unlisted): one message by its id, as JSON {chat, message}. Read-only.

```json
{
  "type": "object",
  "properties": {
    "chat": {"type": "string", "description": "The conversation id (u…, g…, c…) or its exact name"},
    "id": {"type": "integer", "description": "The message id"}
  },
  "required": ["chat", "id"],
  "additionalProperties": false
}
```
