List the Discord servers the user's bot is in and the channels and active threads and forum posts there, most recently active first, each with its id (for discord_messages and discord_search) and when its last message was, and the link that adds the bot to another server. Archived threads and forum posts are not listed; discord_search finds them. The bot sees only what the user let it see in Discord. Read-only.

```json
{
  "type": "object",
  "properties": {
    "server": {
      "type": "string",
      "description": "Only this server: its name or id. Leave out for all of them."
    }
  }
}
```
