The tg command line's own tool (unlisted; Persona's `personad tool --cli telegram` calls it, the assistant does not): {argv, stdin?} answers the call a tg command line means, {name, call: {tool, args}, render}, or what to say right away (help, a mistake in the words), {name, exit, stdout?, stderr?}; {render, result} shows a tool's answer for people, {stdout}. It only reads the words: it never asks Telegram.

```json
{
  "type": "object",
  "properties": {
    "argv": {"type": "array", "items": {"type": "string"}, "description": "The words after tg, --format taken out"},
    "stdin": {"type": "string", "description": "What was on stdin, when a word is a lone -"},
    "render": {"type": "string", "description": "The command whose answer to show as text (the plan's render)"},
    "result": {"type": "string", "description": "The answer of the tool the plan called"}
  },
  "additionalProperties": false
}
```
