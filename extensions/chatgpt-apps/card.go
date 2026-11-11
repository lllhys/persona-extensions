package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/nwverse/persona/sdk/go/wire"
)

// recipientKeys are argument names that say who a change reaches: shown as the card's "who will see it".
var recipientKeys = []string{"to", "cc", "bcc", "recipient", "recipients", "attendees", "emails", "email", "channel",
	"channel_id", "channel_name", "chat", "chat_id", "user", "users", "invitees", "reviewers", "assignees"}

// describe answers actions.describe: the phone's card for a change, from memory only (no network). Bad arguments,
// a read-only tool or a plugin not looked at yet are refused here, before the user is asked.
func (a *App) describe(_ context.Context, p wire.ConnectorCallParams) (wire.ConnectorDescribeResult, error) {
	args, err := decodeArgs(p.Args)
	if err != nil {
		return wire.ConnectorDescribeResult{}, err
	}
	a.mu.Lock()
	text := a.text.card
	a.mu.Unlock()
	switch p.Name {
	case "chatgpt_write":
		if args.Name == "" {
			return wire.ConnectorDescribeResult{}, errors.New("name is empty: which app tool? (chatgpt_apps lists them)")
		}
		if len(args.Arguments) > 0 && string(args.Arguments) != "null" && !jsonObject(args.Arguments) {
			return wire.ConnectorDescribeResult{}, errors.New("arguments must be an object")
		}
		t, ok := a.cachedTool(args.Name)
		if !ok {
			return wire.ConnectorDescribeResult{}, fmt.Errorf("%s is not in the last tool list: call chatgpt_apps (refresh) first, then this again", args.Name)
		}
		if t.readOnly() {
			return wire.ConnectorDescribeResult{}, fmt.Errorf("%s is read-only: call it with chatgpt_read", t.Name)
		}
		return writeCard(t, args.Arguments, text), nil
	case "chatgpt_plugin_install", "chatgpt_plugin_uninstall":
		if strings.TrimSpace(args.Plugin) == "" {
			return wire.ConnectorDescribeResult{}, errors.New("plugin is empty")
		}
		pl, ok := a.remembered(args.Plugin)
		if !ok {
			return wire.ConnectorDescribeResult{}, fmt.Errorf("look at %s with chatgpt_plugin_info first, then this again", args.Plugin)
		}
		if p.Name == "chatgpt_plugin_uninstall" {
			return wire.ConnectorDescribeResult{
				Action:  wire.ConnectorAction{Name: fmt.Sprintf(text.uninstall, pl.title())},
				Summary: fmt.Sprintf(text.uninstallSummary, pl.title(), pl.Name),
			}, nil
		}
		return installCard(pl, text), nil
	}
	return wire.ConnectorDescribeResult{}, fmt.Errorf("no action %q", p.Name)
}

func jsonObject(raw json.RawMessage) bool {
	var m map[string]json.RawMessage
	return json.Unmarshal(raw, &m) == nil
}

func writeCard(t mcpTool, raw json.RawMessage, text cardTexts) wire.ConnectorDescribeResult {
	var args map[string]json.RawMessage
	_ = json.Unmarshal(raw, &args)
	// Who it reaches first, then one-line values, then long text (to, subject, then body), each by name.
	rank := func(k string) int {
		switch {
		case slices.Contains(recipientKeys, strings.ToLower(k)):
			return 0
		case strings.Contains(plain(args[k]), "\n") || len(plain(args[k])) > 80:
			return 2
		}
		return 1
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if ri, rj := rank(keys[i]), rank(keys[j]); ri != rj {
			return ri < rj
		}
		return keys[i] < keys[j]
	})

	var who []string
	for _, k := range keys {
		if slices.Contains(recipientKeys, strings.ToLower(k)) {
			who = append(who, plain(args[k]))
		}
	}
	// The hub writes it under the summary as 「谁会看到：…」, so it is only the people (or the account).
	visible := fmt.Sprintf(text.account, t.app())
	if len(who) > 0 {
		visible = strings.Join(who, "; ")
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf(text.callAs, t.app(), t.Name))
	if d := firstLine(t.Description); d != "" {
		b.WriteString("\n" + short(d, 200))
	}
	b.WriteString("\n\n" + text.args)
	if len(keys) == 0 {
		b.WriteString(" " + text.none)
	}
	for _, k := range keys {
		v := plain(args[k])
		if strings.Contains(v, "\n") {
			b.WriteString("\n" + k + ":\n" + v)
		} else {
			b.WriteString("\n" + k + ": " + v)
		}
	}
	return wire.ConnectorDescribeResult{
		Action:  wire.ConnectorAction{Name: t.app() + " · " + t.title(), Visible: visible},
		Summary: b.String(),
	}
}

func installCard(p pluginDetail, text cardTexts) wire.ConnectorDescribeResult {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(text.installSummary, p.title(), p.Name))
	if dev := p.Release.Interface.DeveloperName; dev != "" {
		b.WriteString("\n" + fmt.Sprintf(text.developer, dev))
	}
	if d := strings.TrimSpace(p.Release.Description); d != "" {
		b.WriteString("\n" + short(d, 400))
	}
	var apps []string
	for _, s := range p.apps {
		name := s.Name
		if name == "" {
			name = s.ID
		}
		if s.State != "connected" {
			name = fmt.Sprintf(text.needsConnect, name)
		}
		apps = append(apps, name)
	}
	if p.apps == nil && len(p.Release.AppIDs) > 0 {
		apps = append(apps, fmt.Sprint(len(p.Release.AppIDs)))
	}
	b.WriteString("\n\n")
	if len(apps) == 0 {
		b.WriteString(fmt.Sprintf(text.apps, text.none))
	} else {
		b.WriteString(fmt.Sprintf(text.apps, strings.Join(apps, "、")))
	}
	b.WriteString("\n" + fmt.Sprintf(text.skills, len(p.Release.Skills)))
	return wire.ConnectorDescribeResult{
		Action:  wire.ConnectorAction{Name: fmt.Sprintf(text.install, p.title())},
		Summary: b.String(),
	}
}

// plain is an argument as people read it: a string as it is, a list of strings joined, anything else as JSON.
func plain(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return strings.Join(list, ", ")
	}
	return string(raw)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "\n"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// do answers actions.do: the change the user just approved, once per request id.
func (a *App) do(ctx context.Context, p wire.ConnectorDoParams) (wire.ConnectorCallResult, error) {
	return a.ledger.once(p.RequestID, func() (wire.ConnectorCallResult, error) {
		args, err := decodeArgs(p.Args)
		if err != nil {
			return wire.ConnectorCallResult{}, err
		}
		switch p.Name {
		case "chatgpt_write":
			t, err := a.findTool(ctx, args.Name)
			if err != nil {
				return wire.ConnectorCallResult{}, err
			}
			text, err := a.callTool(ctx, t, args.Arguments)
			return wire.ConnectorCallResult{Text: text}, err
		case "chatgpt_plugin_install":
			text, err := a.install(ctx, args.Plugin)
			return wire.ConnectorCallResult{Text: text}, err
		case "chatgpt_plugin_uninstall":
			text, err := a.uninstall(ctx, args.Plugin)
			return wire.ConnectorCallResult{Text: text}, err
		}
		return wire.ConnectorCallResult{}, fmt.Errorf("no action %q", p.Name)
	})
}

// ledger runs each request id once: the hub's id for an approved change is the same if it is ever sent again. Results
// stay in memory (they can hold mail or orders); only the ids go to the data directory, so a repeat after a restart is
// still not run twice.
type ledger struct {
	mu      sync.Mutex
	path    string
	results map[string]*ledgerEntry
	order   []string
}

type ledgerEntry struct {
	done chan struct{}
	res  wire.ConnectorCallResult
	err  error
}

const ledgerSize = 500

func newLedger(dataDir string) *ledger {
	l := &ledger{results: map[string]*ledgerEntry{}}
	if dataDir != "" {
		l.path = filepath.Join(dataDir, "done-requests.json")
		var ids []string
		if raw, err := os.ReadFile(l.path); err == nil && json.Unmarshal(raw, &ids) == nil {
			for _, id := range ids {
				e := &ledgerEntry{done: make(chan struct{}), err: errAlreadyRan}
				close(e.done)
				l.results[id] = e
				l.order = append(l.order, id)
			}
		}
	}
	return l
}

var errAlreadyRan = errors.New("this change already ran (the same request again); its result was not kept, so look it up instead of running it again")

func (l *ledger) once(id string, run func() (wire.ConnectorCallResult, error)) (wire.ConnectorCallResult, error) {
	if id == "" {
		return run()
	}
	l.mu.Lock()
	if e, ok := l.results[id]; ok {
		l.mu.Unlock()
		<-e.done
		return e.res, e.err
	}
	order := append(slices.Clone(l.order), id)
	if len(order) > ledgerSize {
		order = order[len(order)-ledgerSize:]
	}
	// The id is on disk before the change runs, or the change does not run: an id kept only in memory is gone after a
	// restart, and the hub sending the same approved request again would run it a second time.
	if err := l.persist(order); err != nil {
		l.mu.Unlock()
		return wire.ConnectorCallResult{}, fmt.Errorf("could not record the request before running it, so it did not run: %w", err)
	}
	e := &ledgerEntry{done: make(chan struct{})}
	l.results[id] = e
	for _, old := range l.order[:len(l.order)+1-len(order)] {
		delete(l.results, old)
	}
	l.order = order
	l.mu.Unlock()
	e.res, e.err = run()
	close(e.done)
	return e.res, e.err
}

// persist writes order as the ledger's ids (0600, through a synced temporary file); no path (a ledger without a data
// directory) keeps them in memory only.
func (l *ledger) persist(order []string) error {
	if l.path == "" {
		return nil
	}
	raw, err := json.Marshal(order)
	if err != nil {
		return err
	}
	tmp := l.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, l.path)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}
