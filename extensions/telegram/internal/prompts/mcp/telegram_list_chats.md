The tg CLI's `tg chats` (unlisted: not in the models' tool lists): the account's private chats and groups, newest first, as JSON {chats: [{id, name, kind, username, unread, mentions, muted, archived, last}], more}. `id` (u…, g…, c…) names the conversation in every other call. Read-only.

```json
{
  "type": "object",
  "properties": {
    "limit": {"type": "integer", "minimum": 1, "maximum": 100, "description": "How many conversations (default 30)"},
    "unread_only": {"type": "boolean", "description": "Only those with unread messages"}
  },
  "additionalProperties": false
}
```
