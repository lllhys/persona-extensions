The tg CLI's `tg chat` (unlisted): a conversation's profile, as JSON {chat: {id, name, kind, username, unread, about, members, …}}: a person's bio, a group's description and member count. Read-only.

```json
{
  "type": "object",
  "properties": {
    "chat": {"type": "string", "description": "The conversation id (u…, g…, c…) or its exact name"}
  },
  "required": ["chat"],
  "additionalProperties": false
}
```
