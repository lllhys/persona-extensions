The tg CLI's `tg send` and `tg reply` (unlisted): sends a text message as the user, to a conversation named by its id or @username, as a reply when reply_to is set. A write: the user allows it on the phone first (connector_gate), and the card shows the conversation and the full text. Answers JSON {sent, chat}.

```json
{
  "type": "object",
  "properties": {
    "chat": {"type": "string", "description": "The conversation id from tg chats (u…, g…, c…) or @username"},
    "text": {"type": "string", "description": "The message, at most 4096 characters"},
    "reply_to": {"type": "integer", "description": "The id of the message this replies to"}
  },
  "required": ["chat", "text"],
  "additionalProperties": false
}
```
