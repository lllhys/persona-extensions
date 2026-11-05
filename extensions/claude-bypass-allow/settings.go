package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The hook's entry in a Claude config directory's settings.json: one PermissionRequest group with matcher Bash and
// our hook alone in it. Hooks of every tool add up in that file, so the edit touches nothing else: other keys and
// other hooks keep their order and values (only the layout is normalised, through a JSON encoder, in the file's
// own indent), and our entry is told apart by the tag at the end of its command. A copy of the file is kept before
// each change; a file that is not a JSON object is left alone.

// tagFlag ends the entry's command; it is what marks an entry as this extension's.
const tagFlag = "--persona-extension=claude-bypass-allow"

// hookTimeout is how long Claude Code lets the hook run (seconds); it answers in milliseconds.
const hookTimeout = 10

// hookCommand is the entry's command: this program, as the hook, for the config directory dir.
func hookCommand(bin, dir, data string) string {
	return shellQuote(bin) + " hook --config-dir " + shellQuote(dir) + " --data " + shellQuote(data) + " " + tagFlag
}

func isOurCommand(cmd string) bool { return strings.HasSuffix(strings.TrimSpace(cmd), " "+tagFlag) }

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// settingsPath is dir's settings.json, through a symlink (as dotfile managers make).
func settingsPath(dir string) string {
	p := filepath.Join(dir, "settings.json")
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return p
}

// installHook puts the entry with cmd into the settings file at path (created when missing), replacing ours from
// before; changed says whether the file was written.
func installHook(path, cmd string) (changed bool, err error) {
	want, err := marshalPlain(map[string]any{"matcher": "Bash", "hooks": []any{map[string]any{"type": "command", "command": cmd, "timeout": hookTimeout}}})
	if err != nil {
		return false, err
	}
	return editSettings(path, true, func(top *jsonObject) (bool, error) {
		hooks, groups, err := permissionGroups(top)
		if err != nil {
			return false, err
		}
		rest, ours, stripped := splitOurs(groups)
		if len(ours) == 1 && sameJSON(ours[0], want) && !stripped {
			return false, nil // as it should be: leave the file alone
		}
		return true, setPermissionGroups(top, hooks, append(rest, want))
	})
}

// uninstallHook takes our entry out of the settings file at path; a file without it is not written.
func uninstallHook(path string) (changed bool, err error) {
	return editSettings(path, false, func(top *jsonObject) (bool, error) {
		hooks, groups, err := permissionGroups(top)
		if err != nil {
			return false, err
		}
		rest, ours, stripped := splitOurs(groups)
		if len(ours) == 0 && !stripped {
			return false, nil
		}
		return true, setPermissionGroups(top, hooks, rest)
	})
}

// hasHook says whether the settings file at path has our entry.
func hasHook(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var top jsonObject
	if json.Unmarshal(b, &top) != nil {
		return false
	}
	_, groups, err := permissionGroups(&top)
	if err != nil {
		return false
	}
	_, ours, stripped := splitOurs(groups)
	return len(ours) > 0 || stripped
}

// permissionGroups reads top's hooks object and its PermissionRequest list.
func permissionGroups(top *jsonObject) (jsonObject, []json.RawMessage, error) {
	var hooks jsonObject
	if raw, ok := top.get("hooks"); ok {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return nil, nil, fmt.Errorf("hooks is not an object: %w", err)
		}
	}
	var groups []json.RawMessage
	if raw, ok := hooks.get("PermissionRequest"); ok {
		if err := json.Unmarshal(raw, &groups); err != nil {
			return nil, nil, fmt.Errorf("hooks.PermissionRequest is not a list: %w", err)
		}
	}
	return hooks, groups, nil
}

// setPermissionGroups writes groups back as hooks.PermissionRequest; an empty list removes the key, and an empty
// hooks object the hooks key.
func setPermissionGroups(top *jsonObject, hooks jsonObject, groups []json.RawMessage) error {
	if len(groups) == 0 {
		hooks.remove("PermissionRequest")
	} else {
		b, err := marshalPlain(groups)
		if err != nil {
			return err
		}
		hooks.set("PermissionRequest", b)
	}
	if len(hooks) == 0 {
		top.remove("hooks")
		return nil
	}
	b, err := marshalPlain(hooks)
	if err != nil {
		return err
	}
	top.set("hooks", b)
	return nil
}

// splitOurs sorts groups into ours (a group whose hooks are all ours) and the rest; a group where the user put a
// hook of theirs beside ours stays, without ours (stripped).
func splitOurs(groups []json.RawMessage) (rest, ours []json.RawMessage, stripped bool) {
	for _, g := range groups {
		var obj jsonObject
		var list []json.RawMessage
		if json.Unmarshal(g, &obj) != nil {
			rest = append(rest, g)
			continue
		}
		if raw, ok := obj.get("hooks"); !ok || json.Unmarshal(raw, &list) != nil {
			rest = append(rest, g)
			continue
		}
		var left []json.RawMessage
		for _, h := range list {
			var e struct {
				Command string `json:"command"`
			}
			if json.Unmarshal(h, &e) == nil && isOurCommand(e.Command) {
				continue
			}
			left = append(left, h)
		}
		switch {
		case len(left) == len(list):
			rest = append(rest, g)
		case len(left) == 0:
			ours = append(ours, g)
		default:
			b, err := marshalPlain(left)
			if err != nil {
				rest = append(rest, g)
				continue
			}
			obj.set("hooks", b)
			out, err := marshalPlain(obj)
			if err != nil {
				rest = append(rest, g)
				continue
			}
			rest, stripped = append(rest, out), true
		}
	}
	return rest, ours, stripped
}

func sameJSON(a, b []byte) bool {
	var x, y bytes.Buffer
	return json.Compact(&x, a) == nil && json.Compact(&y, b) == nil && bytes.Equal(x.Bytes(), y.Bytes())
}

// editSettings reads the settings file at path, lets edit change its top-level object, and writes it back when edit
// says it changed: a copy of the old file first, then a temporary file in the same directory renamed over it.
// create: a missing file starts as {}.
func editSettings(path string, create bool, edit func(top *jsonObject) (bool, error)) (bool, error) {
	orig, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if !create {
			return false, nil
		}
	case err != nil:
		return false, err
	}
	var top jsonObject
	if len(bytes.TrimSpace(orig)) > 0 {
		if err := json.Unmarshal(orig, &top); err != nil {
			return false, fmt.Errorf("%s is not a JSON object, left alone: %w", path, err)
		}
	}
	changed, err := edit(&top)
	if err != nil || !changed {
		return false, err
	}
	body, err := marshalPlain(top)
	if err != nil {
		return false, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, body, "", indentOf(orig)); err != nil {
		return false, err
	}
	out.WriteByte('\n')
	mode := os.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	if len(orig) > 0 {
		if err := os.WriteFile(path+".claude-bypass-allow.bak", orig, mode); err != nil {
			return false, fmt.Errorf("backup: %w", err)
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-claude-bypass-allow-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(out.Bytes()); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	return true, os.Rename(tmp.Name(), path)
}

// indentOf is the file's own indent unit (its first indented line), two spaces when it has none.
func indentOf(b []byte) string {
	for _, line := range strings.Split(string(b), "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed != "" && len(trimmed) < len(line) {
			return line[:len(line)-len(trimmed)]
		}
	}
	return "  "
}

// marshalPlain encodes v without escaping <, > and & (commands have && and redirects), as Claude Code writes them.
func marshalPlain(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// jsonObject is a JSON object that keeps its members in the file's order (a Go map would sort them).
type jsonObject []jsonMember

type jsonMember struct {
	Key string
	Val json.RawMessage
}

func (o *jsonObject) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil {
		return err
	} else if d, ok := t.(json.Delim); !ok || d != '{' {
		return errors.New("not an object")
	}
	*o = (*o)[:0]
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return err
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return err
		}
		*o = append(*o, jsonMember{Key: kt.(string), Val: v})
	}
	return nil
}

func (o jsonObject) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		k, err := marshalPlain(m.Key)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(m.Val)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func (o jsonObject) get(key string) (json.RawMessage, bool) {
	for _, m := range o {
		if m.Key == key {
			return m.Val, true
		}
	}
	return nil, false
}

func (o *jsonObject) set(key string, v json.RawMessage) {
	for i, m := range *o {
		if m.Key == key {
			(*o)[i].Val = v
			return
		}
	}
	*o = append(*o, jsonMember{Key: key, Val: v})
}

func (o *jsonObject) remove(key string) {
	for i, m := range *o {
		if m.Key == key {
			*o = append((*o)[:i], (*o)[i+1:]...)
			return
		}
	}
}
