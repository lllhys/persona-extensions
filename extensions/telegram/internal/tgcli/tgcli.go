// Package tgcli is tg, the Telegram connector's command line for the assistant (its telegram skill), as the connector
// plans it: Persona's personad tool --cli telegram hands each command line to the telegram_cli tool, which answers the
// call it means (PlanOf) and shows the answer for people (Render); personad calls that tool through the hub, whose
// gate decides each write (Persona docs/connector-protocol.md「命令行」). Moved here from Persona's internal/tgcli
// (#1704): the subcommands, their arguments and the text are tg's as they were; the exit codes and the error kinds
// are personad tool's, the same as tg's.
package tgcli

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// now is the clock relative times ("2h") count back from (replaced by tests).
var now = time.Now

const usage = `tg: the user's Telegram through the Persona hub (the telegram skill)

  tg chats [--unread] [--limit N]           conversations, newest first (ids: u… private, g… group, c… supergroup)
  tg unread [--limit N]                     the ones with unread messages
  tg messages <chat> [--limit N] [--before ID] [--since T] [--until T]
                                            one page of messages, oldest first; next_before is the next page's --before
  tg search <words> [--chat C] [--limit N] [--since T] [--until T]
  tg message <chat> <id>                    one message
  tg chat <chat>                            the conversation's profile
  tg members <chat> [--limit N]             a group's members
  tg send <chat> <text…|->                  send (- reads the text from stdin)
  tg reply <chat> <id> <text…|->            reply to a message
  tg forward <chat> <id>[,<id>…] --to <chat>
  tg react <chat> <id> <emoji>
  tg read <chat> [--up-to ID]               mark as read (the other side sees it)

Every write is allowed by the user on the phone first (or by the hub's connector_gate). A write names the chat by its
id or @username, as tg chats or tg search listed it. Times: RFC 3339, a date (2026-11-03), Unix seconds, or how long
ago (30m, 2h, 3d). --format text prints for people; JSON is the default.
Exit codes: 0 ok, 1 failed, 2 usage, 3 unavailable, 4 rate limited (retry_after), 5 declined.`

// flagKind is how a flag takes its value.
type flagKind int

const (
	boolFlag flagKind = iota
	intFlag
	stringFlag
)

// command is one of tg's: its arguments and flags, the tool it calls, how its answer reads as text.
type command struct {
	args  []string            // positional arguments; one ending in "…" takes the rest
	flags map[string]flagKind // without the leading --
	tool  string
	build func(p parsed) (map[string]any, error)
	text  func(w io.Writer, raw []byte) error
}

var timeFlags = map[string]flagKind{"since": stringFlag, "until": stringFlag}

func withTimes(m map[string]flagKind) map[string]flagKind {
	for k, v := range timeFlags {
		m[k] = v
	}
	return m
}

var commands = map[string]command{
	"chats": {flags: map[string]flagKind{"unread": boolFlag, "limit": intFlag}, tool: "telegram_list_chats", text: textChats,
		build: func(p parsed) (map[string]any, error) {
			return p.with(map[string]any{"unread_only": p.bools["unread"]}, "limit"), nil
		}},
	"unread": {flags: map[string]flagKind{"limit": intFlag}, tool: "telegram_list_chats", text: textChats,
		build: func(p parsed) (map[string]any, error) {
			return p.with(map[string]any{"unread_only": true}, "limit"), nil
		}},
	"messages": {args: []string{"chat"}, flags: withTimes(map[string]flagKind{"limit": intFlag, "before": intFlag}), tool: "telegram_read_messages",
		text: textMessages, build: func(p parsed) (map[string]any, error) {
			return p.timed(map[string]any{"chat": p.pos[0]}, "limit", "before")
		}},
	"search": {args: []string{"words…"}, flags: withTimes(map[string]flagKind{"chat": stringFlag, "limit": intFlag}), tool: "telegram_search_messages",
		text: textSearch, build: func(p parsed) (map[string]any, error) {
			return p.timed(map[string]any{"query": strings.Join(p.pos, " ")}, "chat", "limit")
		}},
	"message": {args: []string{"chat", "id"}, tool: "telegram_get_message", text: textMessage,
		build: func(p parsed) (map[string]any, error) {
			id, err := p.id(1)
			return map[string]any{"chat": p.pos[0], "id": id}, err
		}},
	"chat": {args: []string{"chat"}, tool: "telegram_get_chat", text: textChat,
		build: func(p parsed) (map[string]any, error) { return map[string]any{"chat": p.pos[0]}, nil }},
	"members": {args: []string{"chat"}, flags: map[string]flagKind{"limit": intFlag}, tool: "telegram_list_members", text: textMembers,
		build: func(p parsed) (map[string]any, error) { return p.with(map[string]any{"chat": p.pos[0]}, "limit"), nil }},
	"send": {args: []string{"chat", "text…"}, tool: "telegram_send", text: textWrite,
		build: func(p parsed) (map[string]any, error) {
			text, err := p.body(1)
			return map[string]any{"chat": p.pos[0], "text": text}, err
		}},
	"reply": {args: []string{"chat", "id", "text…"}, tool: "telegram_send", text: textWrite,
		build: func(p parsed) (map[string]any, error) {
			id, err := p.id(1)
			if err != nil {
				return nil, err
			}
			text, err := p.body(2)
			return map[string]any{"chat": p.pos[0], "reply_to": id, "text": text}, err
		}},
	"forward": {args: []string{"chat", "ids"}, flags: map[string]flagKind{"to": stringFlag}, tool: "telegram_forward", text: textWrite,
		build: func(p parsed) (map[string]any, error) {
			if p.strings["to"] == "" {
				return nil, errors.New("--to <chat> is required")
			}
			var ids []int
			for _, s := range strings.Split(p.pos[1], ",") {
				id, err := strconv.Atoi(strings.TrimSpace(s))
				if err != nil || id <= 0 {
					return nil, fmt.Errorf("%q is not a list of message ids (991,992)", p.pos[1])
				}
				ids = append(ids, id)
			}
			return map[string]any{"chat": p.pos[0], "ids": ids, "to": p.strings["to"]}, nil
		}},
	"react": {args: []string{"chat", "id", "emoji"}, tool: "telegram_react", text: textWrite,
		build: func(p parsed) (map[string]any, error) {
			id, err := p.id(1)
			return map[string]any{"chat": p.pos[0], "id": id, "emoji": p.pos[2]}, err
		}},
	"read": {args: []string{"chat"}, flags: map[string]flagKind{"up-to": intFlag}, tool: "telegram_mark_read", text: textWrite,
		build: func(p parsed) (map[string]any, error) {
			out := map[string]any{"chat": p.pos[0]}
			if n, ok := p.ints["up-to"]; ok {
				out["up_to"] = n
			}
			return out, nil
		}},
}

// parsed is a command line: positional arguments and flags by kind.
type parsed struct {
	pos     []string
	bools   map[string]bool
	ints    map[string]int
	strings map[string]string
	stdin   io.Reader
}

// with adds the int or string flags given to args.
func (p parsed) with(args map[string]any, flags ...string) map[string]any {
	for _, f := range flags {
		if n, ok := p.ints[f]; ok {
			args[f] = n
		} else if s, ok := p.strings[f]; ok {
			args[f] = s
		}
	}
	return args
}

// timed is with, and --since / --until as the connector reads them.
func (p parsed) timed(args map[string]any, flags ...string) (map[string]any, error) {
	args = p.with(args, flags...)
	for _, f := range []string{"since", "until"} {
		s, ok := p.strings[f]
		if !ok {
			continue
		}
		t, err := when(s)
		if err != nil {
			return nil, fmt.Errorf("--%s: %w", f, err)
		}
		args[f] = t
	}
	return args, nil
}

func (p parsed) id(i int) (int, error) {
	id, err := strconv.Atoi(p.pos[i])
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%q is not a message id", p.pos[i])
	}
	return id, nil
}

// body is the text from the argument i on, or stdin for "-".
func (p parsed) body(i int) (string, error) {
	if len(p.pos) == i+1 && p.pos[i] == "-" {
		b, err := io.ReadAll(io.LimitReader(p.stdin, 1<<20))
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(b), "\n"), nil
	}
	return strings.Join(p.pos[i:], " "), nil
}

var relative = regexp.MustCompile(`^(\d+)([mhdw])$`)

// when turns a time on the command line into what the connector reads: RFC 3339 and dates as they are (a date is
// midnight in the user's zone, which only the hub knows), Unix seconds and "2h" (ago) into RFC 3339.
func when(s string) (string, error) {
	if _, err := time.Parse(time.RFC3339, s); err == nil {
		return s, nil
	}
	if _, err := time.Parse(time.DateOnly, s); err == nil {
		return s, nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
		return time.Unix(n, 0).UTC().Format(time.RFC3339), nil
	}
	if m := relative.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		unit := map[string]time.Duration{"m": time.Minute, "h": time.Hour, "d": 24 * time.Hour, "w": 7 * 24 * time.Hour}[m[2]]
		return now().Add(-time.Duration(n) * unit).UTC().Format(time.RFC3339), nil
	}
	return "", fmt.Errorf("%q: give RFC 3339, a date (2026-11-03), Unix seconds or how long ago (30m, 2h, 3d)", s)
}

// parse reads args for c: flags anywhere (--name value, --name=value), "-" and the rest as positional.
func parse(c command, args []string, stdin io.Reader) (parsed, error) {
	p := parsed{bools: map[string]bool{}, ints: map[string]int{}, strings: map[string]string{}, stdin: stdin}
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, ok := strings.CutPrefix(a, "--")
		if !ok || name == "" {
			p.pos = append(p.pos, a)
			continue
		}
		name, value, hasValue := strings.Cut(name, "=")
		kind, known := c.flags[name]
		if !known {
			return p, fmt.Errorf("unknown flag --%s", name)
		}
		if kind == boolFlag {
			if hasValue {
				return p, fmt.Errorf("--%s takes no value", name)
			}
			p.bools[name] = true
			continue
		}
		if !hasValue {
			if i+1 == len(args) {
				return p, fmt.Errorf("--%s needs a value", name)
			}
			i++
			value = args[i]
		}
		if kind == intFlag {
			n, err := strconv.Atoi(value)
			if err != nil || n <= 0 {
				return p, fmt.Errorf("--%s: %q is not a positive number", name, value)
			}
			p.ints[name] = n
		} else {
			p.strings[name] = value
		}
	}
	want := len(c.args)
	rest := want > 0 && strings.HasSuffix(c.args[want-1], "…")
	if len(p.pos) < want || !rest && len(p.pos) > want {
		return p, fmt.Errorf("wants %s", strings.Join(c.args, " "))
	}
	return p, nil
}

// Plan is what a command line means (personad tool --cli's plan): the call with its arguments and how its answer reads
// as text, or what to say right away (help, usage, a mistake) with an exit code.
type Plan struct {
	Name   string `json:"name"`
	Exit   *int   `json:"exit,omitempty"`
	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
	Call   *Call  `json:"call,omitempty"`
	Render string `json:"render,omitempty"`
}

// Call is a tool and its arguments.
type Call struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args"`
}

// PlanOf is the plan for argv (--format already taken out by personad tool), with stdin for a text given as "-".
func PlanOf(argv []string, stdin string) Plan {
	say := func(code int, out, err string) Plan { return Plan{Name: "tg", Exit: &code, Stdout: out, Stderr: err} }
	if len(argv) > 0 && (argv[0] == "-h" || argv[0] == "--help" || argv[0] == "help") {
		return say(0, usage, "")
	}
	if len(argv) == 0 {
		return say(2, usage, "")
	}
	c, ok := commands[argv[0]]
	if !ok {
		return say(2, "", fmt.Sprintf("no command %q (tg --help)", argv[0]))
	}
	p, err := parse(c, argv[1:], strings.NewReader(stdin))
	if err != nil {
		return say(2, "", fmt.Sprintf("tg %s: %v", argv[0], err))
	}
	args, err := c.build(p)
	if err != nil {
		return say(2, "", fmt.Sprintf("tg %s: %v", argv[0], err))
	}
	return Plan{Name: "tg", Call: &Call{Tool: c.tool, Args: args}, Render: argv[0]}
}

// Render is the answer of command what as text for people.
func Render(what, result string) (string, error) {
	c, ok := commands[what]
	if !ok {
		return "", fmt.Errorf("no command %q", what)
	}
	var b strings.Builder
	if err := c.text(&b, []byte(result)); err != nil {
		return "", fmt.Errorf("the answer is not what tg reads: %w", err)
	}
	return b.String(), nil
}
