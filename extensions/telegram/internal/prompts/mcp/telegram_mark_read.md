The tg CLI's `tg read` (unlisted): marks a conversation as read, up to a message or all of it; the other side can see it. A write: the user allows it on the phone first. Answers JSON {read, up_to}.

```json
{
  "type": "object",
  "properties": {
    "chat": {"type": "string", "description": "The conversation id or @username"},
    "up_to": {"type": "integer", "description": "Read up to this message id (default: all of it)"}
  },
  "required": ["chat"],
  "additionalProperties": false
}
```
