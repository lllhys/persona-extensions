The tg CLI's `tg react` (unlisted): puts an emoji reaction on a message as the user. A write: the user allows it on the phone first. Answers JSON {reacted, chat, id}.

```json
{
  "type": "object",
  "properties": {
    "chat": {"type": "string", "description": "The conversation id or @username"},
    "id": {"type": "integer", "description": "The message id"},
    "emoji": {"type": "string", "description": "One emoji, like 👍"}
  },
  "required": ["chat", "id", "emoji"],
  "additionalProperties": false
}
```
