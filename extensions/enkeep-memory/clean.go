package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/voice"
)

// cleanSources applies the rule "only the user's words are what an entry about the user rests on" to a state
// written before it: every cited line is read again from the knowledge base and given its role (voice.Role); a memory
// entry keeps only the user's own lines (not bare agreement) and goes when none is left; an item drops the
// assistant's, agents' and bots' lines and goes when nothing else is left; one resting only on other people who do
// not name the user, or on a single source, is a candidate again (待确认); a pending proposal for an item that went or became a candidate is
// expired. Only the curator's own state changes: nothing in the knowledge base. The state is edited as raw JSON, so
// fields this build does not know are kept as they are. Before writing, state.json is copied to
// state.json.bak-<time> next to it, and only the newest -keep copies are kept.
func cleanSources(args []string) error {
	fs := flag.NewFlagSet("clean-sources", flag.ContinueOnError)
	stateDir := fs.String("state", "", "the curator's data directory (state.json)")
	kb := fs.String("kb", "", "a copy of the hub's knowledge-base directory with the chats the state cites (read only)")
	dry := fs.Bool("dry", false, "report only; write nothing")
	keep := fs.Int("keep", 3, "backups of state.json kept next to it")
	rejectOthers := fs.Bool("reject-others", false, "mark items that rest only on other people's words, none naming the user, rejected (hidden, kept in the state) instead of waiting as candidates")
	samples := fs.Int("sample", 10, "entries shown in the report")
	report := fs.String("report", "", "write the report (JSON) here")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *stateDir == "" || *kb == "" {
		return fmt.Errorf("-state and -kb are required")
	}
	path := filepath.Join(*stateDir, "state.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fx, err := kbsrc.LoadFixture(*kb)
	if err != nil {
		return err
	}
	cl := &cleaner{src: fx, lines: map[string][]norm.Line{}, kinds: map[string]string{}, rejectOthers: *rejectOthers}
	if o, err := fx.Owner(context.Background()); err == nil {
		cl.owner = append([]string{o.Name}, o.Aliases...)
	}
	out, rep, err := cl.clean(raw)
	if err != nil {
		return err
	}
	rep.Samples = sample(rep.Changes, *samples)
	b, _ := json.MarshalIndent(rep, "", "  ")
	if *report != "" {
		if err := os.WriteFile(*report, b, 0o600); err != nil {
			return err
		}
	}
	fmt.Printf("memory entries %d → %d, items %d → %d (%d now candidates, %d rejected), proposals expired %d, cited lines not found %d\n",
		rep.Before.Memory, rep.After.Memory, rep.Before.Items, rep.After.Items, rep.Demoted, rep.Rejected, rep.ProposalsExpired, rep.Unknown)
	if *dry {
		return nil
	}
	backup, err := backupState(path, *keep)
	if err != nil {
		return err
	}
	fmt.Println("backup:", backup)
	return writeFileAtomic(path, out)
}

type cleaner struct {
	err   error // the first error reading the knowledge base (not a missing day): the clean is not to be trusted
	src   kbsrc.Source
	owner []string
	lines map[string][]norm.Line // "<chat>@<day>" → its lines as the curator reads them
	kinds map[string]string      // chat → kind
	// rejectOthers marks an item that rests only on other people's words, none naming the user, rejected instead of a
	// candidate. Only clean-sources -reject-others sets it: the start-up migration never does.
	rejectOthers bool
}

// cleanReport is what clean-sources did.
type cleanReport struct {
	Before, After    counts
	Demoted          int `json:"demoted"`
	Rejected         int `json:"rejected"` // items marked rejected: they rest only on other people's words (-reject-others)
	ProposalsExpired int `json:"proposals_expired"`
	Unknown          int `json:"unknown_lines"` // cited lines no longer in the copy: kept as they were
	// Removed counts what went, by layer and category: "memory:agent", "item:assistant"… (category below).
	Removed map[string]int `json:"removed"`
	Changes []change       `json:"changes"`
	Samples []change       `json:"samples"`
}

type counts struct {
	Memory int `json:"memory"`
	Items  int `json:"items"`
}

// Categories of what an entry wrongly rested on.
const (
	catAgent     = "agent"     // ① an agent's text under the user's name, or what Persona typed in as the user
	catAssistant = "assistant" // ② the assistant's words, its paraphrase of the user ("你之前说过…") included
	catDecision  = "decision"  // ③ the user's bare "好" to what the assistant decided or proposed
	catBot       = "bot"       //   an automated sender
	catOthers    = "others"    //   other people who do not name the user (made a candidate, not removed)
)

// category is what a dropped line was, by its role.
func category(role string) string {
	switch role {
	case voice.RoleAgentDraft:
		return catAgent
	case voice.RoleAssistant:
		return catAssistant
	case voice.RoleUser:
		return catDecision
	case voice.RoleBot:
		return catBot
	}
	return catOthers
}

type change struct {
	Kind     string   `json:"kind"`   // memory | item
	ID       string   `json:"id"`     //
	Text     string   `json:"text"`   //
	Action   string   `json:"action"` // removed | candidate | rejected | evidence
	Why      string   `json:"why"`
	Lines    []string `json:"lines"`    // "<role> <chat>#<n>: <quote>"
	Category string   `json:"category"` // catAgent…, "single_source" for an item made a candidate for that
}

// role is the role of a cited line, as the curator reads it now ("" when the line is not in the copy).
func (c *cleaner) role(chat, day string, n int) (string, norm.Line) {
	key := chat + "@" + day
	ls, ok := c.lines[key]
	if !ok {
		kind, id, _ := strings.Cut(chat, "/")
		if kind == "chats" {
			r, err := c.src.Day(context.Background(), id, day)
			if err != nil && !errors.Is(err, kbsrc.ErrNotFound) && c.err == nil {
				c.err = fmt.Errorf("kb.day %s %s: %w", id, day, err)
			}
			if err == nil {
				for _, m := range r.Messages {
					ls = append(ls, voice.Relabel(norm.Parse(m.N, m.Text)))
				}
				if strings.HasPrefix(id, "github-") || strings.HasPrefix(id, "gh-") {
					ls = voice.AgentThread(ls)
				}
			}
			if d, err := c.src.Get(context.Background(), "chats", id); err == nil {
				c.kinds[chat] = kbsrc.DecodeMeta(d).Kind
			}
		}
		c.lines[key] = ls
	}
	for _, l := range ls {
		if l.N == n {
			return voice.Role(l, c.kinds[chat]), l
		}
	}
	return "", norm.Line{}
}

func (c *cleaner) clean(raw []byte) ([]byte, *cleanReport, error) {
	var s map[string]json.RawMessage
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, nil, fmt.Errorf("state.json: %w", err)
	}
	rep := &cleanReport{Removed: map[string]int{}}
	// The memory layer of a state written before v4 (none in a newer one).
	mem := map[string]json.RawMessage{}
	if len(s["memory"]) > 0 {
		if err := json.Unmarshal(s["memory"], &mem); err != nil {
			return nil, nil, fmt.Errorf("memory: %w", err)
		}
	}
	entries := map[string]map[string]any{}
	if len(mem["entries"]) > 0 {
		if err := json.Unmarshal(mem["entries"], &entries); err != nil {
			return nil, nil, fmt.Errorf("memory entries: %w", err)
		}
	}
	var items map[string]map[string]any
	if err := json.Unmarshal(s["items"], &items); err != nil {
		return nil, nil, fmt.Errorf("items: %w", err)
	}
	rep.Before = counts{len(entries), len(items)}

	removedEntries := map[string]bool{}
	for _, id := range sortedKeys(entries) {
		e := entries[id]
		evs, _ := e["evidence"].([]any)
		var kept []any
		var lines []string
		users := false
		for _, x := range evs {
			ev, _ := x.(map[string]any)
			anchor, _ := ev["anchor"].(string)
			quote, _ := ev["quote"].(string)
			day, _ := ev["day"].(string)
			chat, nstr, ok := strings.Cut(anchor, "#")
			n, err := strconv.Atoi(nstr)
			if !ok || err != nil || !strings.HasPrefix(chat, "chats/") {
				kept = append(kept, x) // an imported or other anchor: not a chat line, kept as it is
				users = true
				continue
			}
			role, _ := c.role(chat, day, n)
			switch {
			case role == "":
				rep.Unknown++
				kept = append(kept, x)
				users = true
			case role == voice.RoleUser && !voice.Agreement(quote):
				kept = append(kept, x)
				users = true
			default:
				if role == voice.RoleUser {
					role = "user (agreement only)"
				}
				lines = append(lines, fmt.Sprintf("%s %s#%d: %s", role, chat, n, clip(quote)))
			}
		}
		if len(lines) == 0 {
			continue
		}
		text, _ := e["text"].(string)
		if !users || len(kept) == 0 {
			delete(entries, id)
			removedEntries[id] = true
			rep.Changes = append(rep.Changes, change{"memory", id, text, "removed", "它依据的话没有一句是用户本人说的", lines, ""})
			continue
		}
		e["evidence"] = kept
		rep.Changes = append(rep.Changes, change{"memory", id, text, "evidence", "去掉了不是用户本人说的依据", lines, ""})
	}
	// Contradictions with an entry that went go too.
	if len(removedEntries) > 0 {
		var conflicts []map[string]any
		if raw, ok := mem["conflicts"]; ok && json.Unmarshal(raw, &conflicts) == nil {
			var keep []map[string]any
			for _, cf := range conflicts {
				a, _ := cf["a"].(string)
				b, _ := cf["b"].(string)
				if !removedEntries[a] && !removedEntries[b] {
					keep = append(keep, cf)
				}
			}
			if keep == nil {
				keep = []map[string]any{}
			}
			mem["conflicts"], _ = json.Marshal(keep)
		}
	}

	gone := map[string]bool{}
	for _, id := range sortedKeys(items) {
		it := items[id]
		evs, _ := it["evidence"].([]any)
		var kept []any
		var dropped []string
		rests := false
		chats := map[string]bool{}
		for _, x := range evs {
			ev, _ := x.(map[string]any)
			chat, _ := ev["chat"].(string)
			day, _ := ev["day"].(string)
			quote, _ := ev["quote"].(string)
			n := 0
			if f, ok := ev["n"].(float64); ok {
				n = int(f)
			}
			role, l := c.role(chat, day, n)
			switch {
			case role == "":
				rep.Unknown++
				kept = append(kept, x)
				rests = true
			case role == voice.RoleUser && !voice.Agreement(quote):
				ev["role"] = role
				kept = append(kept, x)
				rests = true
			case role == voice.RoleOther:
				ev["role"] = role
				kept = append(kept, x)
				rests = rests || voice.Mentions(l.Body, c.owner)
			default:
				if role == voice.RoleUser {
					role = "user (agreement only)"
				}
				dropped = append(dropped, fmt.Sprintf("%s %s#%d: %s", role, chat, n, clip(quote)))
				continue
			}
			chats[chat] = true
		}
		claim, _ := it["claim"].(string)
		status, _ := it["status"].(string)
		if !rests && len(kept) == 0 {
			// It rested only on the assistant, an agent's draft or a bot (or on the user agreeing to one).
			delete(items, id)
			gone[id] = true
			rep.Changes = append(rep.Changes, change{"item", id, claim, "removed", "依据的话来自助手、Agent 代写或机器人，不是用户本人说的", dropped, ""})
			continue
		}
		if !rests && c.rejectOthers {
			// What the extraction gate (itemGate) would throw away today: other people's words, none naming the user.
			// Marked rejected, not deleted: the claim, its citations and its history stay in the state, and the
			// backup holds the state as it was.
			for _, x := range kept {
				ev, _ := x.(map[string]any)
				dropped = append(dropped, fmt.Sprintf("other %v#%v: %s", ev["chat"], ev["n"], clip(fmt.Sprint(ev["quote"]))))
			}
			if status != "rejected" && status != "merged" {
				it["status"] = "rejected"
				rep.Rejected++
			}
			gone[id] = true
			rep.Changes = append(rep.Changes, change{"item", id, claim, "rejected", "依据是别人说的、没有点名用户：标为拒绝", dropped, catOthers})
			continue
		}
		if !rests {
			// It rests on other people's words that do not name the user: not the assistant's, but not the user's
			// either. It waits for the user (待确认) instead of going.
			for _, x := range kept {
				ev, _ := x.(map[string]any)
				dropped = append(dropped, fmt.Sprintf("other %v#%v: %s", ev["chat"], ev["n"], clip(fmt.Sprint(ev["quote"]))))
			}
			if len(kept) < len(evs) {
				it["evidence"] = kept
			}
			if status != "candidate" && status != "observed" && status != "merged" {
				if it["class"] == "practice" {
					it["status"] = "observed"
				} else {
					it["status"] = "candidate"
				}
				rep.Demoted++
			}
			gone[id] = true
			rep.Changes = append(rep.Changes, change{"item", id, claim, "candidate", "依据是别人说的、没有点名用户：改为待确认", dropped, ""})
			continue
		}
		if len(dropped) > 0 {
			it["evidence"] = kept
		}
		// Two independent sources make an item active now; one is a candidate again (待确认).
		if (status == "active" || status == "recurring" || status == "established") && len(chats) < 2 && status != "merged" {
			if status == "active" {
				it["status"] = "candidate"
			} else {
				it["status"] = "observed"
			}
			it["sources"] = len(chats)
			gone[id] = true // its pin proposal waits for a second source
			rep.Demoted++
			rep.Changes = append(rep.Changes, change{"item", id, claim, "candidate", "只有一个来源，改回待确认", dropped, ""})
		} else if len(dropped) > 0 {
			rep.Changes = append(rep.Changes, change{"item", id, claim, "evidence", "去掉了不是用户本人说的依据", dropped, ""})
		}
	}
	rep.After = counts{len(entries), len(items)}

	var props []map[string]any
	if err := json.Unmarshal(s["proposals"], &props); err == nil {
		today := time.Now().Format("2006-01-02")
		for _, p := range props {
			if st, _ := p["status"].(string); st != "pending" && st != "later" {
				continue
			}
			ids, _ := p["items"].([]any)
			for _, x := range ids {
				if id, _ := x.(string); gone[id] {
					p["status"], p["decided"] = "expired", today
					p["apply_log"] = "清理：它的条目不再只依据用户本人的话，或只剩一个来源"
					rep.ProposalsExpired++
					break
				}
			}
		}
		s["proposals"], _ = json.Marshal(props)
	}

	if len(s["memory"]) > 0 {
		mem["entries"], _ = json.Marshal(entries)
		s["memory"], _ = json.Marshal(mem)
	}
	s["items"], _ = json.Marshal(items)
	for i := range rep.Changes {
		ch := &rep.Changes[i]
		ch.Category = catOthers
		if len(ch.Lines) > 0 {
			role, _, _ := strings.Cut(ch.Lines[0], " ")
			ch.Category = category(role)
		}
		if ch.Action == "candidate" && ch.Why == "只有一个来源，改回待确认" {
			ch.Category = "single_source"
		}
		if ch.Action == "removed" {
			rep.Removed[ch.Kind+":"+ch.Category]++
		}
	}
	if c.err != nil {
		return nil, rep, c.err
	}
	out, err := json.Marshal(s)
	return out, rep, err
}

func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 60 {
		return string(r[:60]) + "…"
	}
	return s
}

// sample picks n changes, the same ones every time, removals first.
func sample(cs []change, n int) []change {
	var removed, other []change
	for _, c := range cs {
		if c.Action == "removed" {
			removed = append(removed, c)
		} else {
			other = append(other, c)
		}
	}
	r := rand.New(rand.NewSource(20261109))
	r.Shuffle(len(removed), func(i, j int) { removed[i], removed[j] = removed[j], removed[i] })
	r.Shuffle(len(other), func(i, j int) { other[i], other[j] = other[j], other[i] })
	out := append(removed, other...)
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// backupState copies state.json to state.json.bak-<time> next to it and keeps only the newest keep copies.
func backupState(path string, keep int) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	name := path + ".bak-" + time.Now().UTC().Format("20060102T150405Z")
	if err := os.WriteFile(name, raw, 0o600); err != nil {
		return "", err
	}
	old, _ := filepath.Glob(path + ".bak-*")
	sort.Strings(old)
	for len(old) > keep && keep > 0 {
		if err := os.Remove(old[0]); err != nil && !errors.Is(err, os.ErrNotExist) {
			return name, err
		}
		old = old[1:]
	}
	return name, nil
}

func writeFileAtomic(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// migrateSources cleans a state written before source roles, once, in the running extension (the caller holds the
// store's lock, so no run writes in between): the same clean as clean-sources, reading the cited lines through src
// (the hub's kb.day). state.json is backed up next to itself first (the newest three kept). What it did is kept in
// the state (SourceClean) for the health page and the log. A clean that could not read the knowledge base changes
// nothing and keeps the old version, so the next start tries again.
func migrateSources(ctx context.Context, st *store.Store, src kbsrc.Source, now time.Time) (*store.SourceClean, error) {
	if st.S.Version >= store.Version {
		return nil, nil
	}
	raw, err := json.Marshal(st.S)
	if err != nil {
		return nil, err
	}
	cl := &cleaner{src: src, lines: map[string][]norm.Line{}, kinds: map[string]string{}}
	if o, err := src.Owner(ctx); err == nil {
		cl.owner = append([]string{o.Name}, o.Aliases...)
	}
	out, rep, err := cl.clean(raw)
	if err != nil {
		return nil, err
	}
	var ns store.State
	if err := json.Unmarshal(out, &ns); err != nil {
		return nil, err
	}
	sc := &store.SourceClean{At: now.UTC().Format(time.RFC3339), MemoryBefore: rep.Before.Memory, MemoryAfter: rep.After.Memory,
		ItemsBefore: rep.Before.Items, ItemsAfter: rep.After.Items, Removed: rep.Removed, Candidates: rep.Demoted,
		ProposalsExpired: rep.ProposalsExpired}
	path := filepath.Join(st.Dir, "state.json")
	if _, err := os.Stat(path); err == nil {
		if sc.Backup, err = backupState(path, 3); err != nil {
			return nil, err
		}
	}
	ns.Version, ns.SourceClean = store.Version, sc
	st.Replace(&ns)
	return sc, st.Save()
}

// cleanSummary is a SourceClean in one line, for the log.
func cleanSummary(sc *store.SourceClean) string {
	var parts []string
	for _, k := range sortedKeys(sc.Removed) {
		parts = append(parts, fmt.Sprintf("%s %d", k, sc.Removed[k]))
	}
	return fmt.Sprintf("memory %d → %d, items %d → %d, removed: %s; candidates %d, proposals expired %d, backup %s",
		sc.MemoryBefore, sc.MemoryAfter, sc.ItemsBefore, sc.ItemsAfter, strings.Join(parts, ", "), sc.Candidates,
		sc.ProposalsExpired, filepath.Base(sc.Backup))
}
