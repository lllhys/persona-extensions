The tg CLI's `tg forward` (unlisted): forwards messages from one conversation to another as the user. A write: the user allows it on the phone first. Answers JSON {forwarded, to}.

```json
{
  "type": "object",
  "properties": {
    "chat": {"type": "string", "description": "The conversation the messages are in (id or @username)"},
    "ids": {"type": "array", "items": {"type": "integer"}, "minItems": 1, "maxItems": 20, "description": "The message ids"},
    "to": {"type": "string", "description": "The conversation to forward them to (id or @username)"}
  },
  "required": ["chat", "ids", "to"],
  "additionalProperties": false
}
```
