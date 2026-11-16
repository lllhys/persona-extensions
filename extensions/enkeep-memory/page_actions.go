package main

// What the v4 pages change. Every entry change goes through the curator's entry API (curate.UserAct, Observe); the
// page itself owns State.Onboarding, the user's switches and the verdicts. A change that can only take things out of
// the exits ("wrong", delete, a switch turned off) is compiled into them at once (withdraw); additions wait for the
// next daily pass, which also rewrites them for the exits.

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/curate"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	exits "github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/export"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/people"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// typeEditor is the open type table's editing (P3, curate.(*Curator).RenameType / MergeTypes). Until the curator has
// them the page says so instead of changing entries itself.
type typeEditor interface {
	RenameType(f entry.Facet, from, to string) error
	MergeTypes(f entry.Facet, into string, from ...string) error
}

// personMerger is P9's merging and splitting of cards (State.PersonMerges). Until the curator has it, the page keeps
// the merge record itself: it is the plugin's own note that several person entries are one person, never written to
// the hub, and undoable.
type personMerger interface {
	MergePersons(persons []string, name string) (string, error)
	SplitPersons(id string) error
}

// v4Action handles the v4 pages' actions; handled false leaves the action to the older pages. Call it without the
// store's lock; it takes it for changes.
func (a *app) v4Action(ctx context.Context, s *page, act string, values map[string]string, t tr) (toast string, handled bool, err error) {
	c := a.c
	switch {
	case act == "facet":
		s.Facet, s.Open = values["facet"], ""
		return "", true, nil
	case act == "check":
		s.Check, s.Sub, s.Open = values["check"], "", ""
		return "", true, nil
	}
	write := func(f func(st *store.State) (bool, error)) error {
		c.St.Lock()
		defer c.St.Unlock()
		withdraw, err := f(c.St.S)
		if err != nil {
			return err
		}
		if withdraw {
			a.withdraw(ctx)
		}
		return c.St.Save()
	}
	switch {
	case act == "ob:save":
		use := values["use"]
		if use != entry.Work && use != entry.Life {
			use = "both"
		}
		err = write(func(st *store.State) (bool, error) {
			return true, a.saveOnboarding(ctx, use, lines(values["musts"], 0), lines(values["focus"], 3))
		})
		if err == nil {
			toast = t.s("记下了；随时可以回来改", "Saved; change it here any time")
			s.Open = ""
		}
	case act == "ob:skip":
		err = write(func(st *store.State) (bool, error) {
			now := time.Now().UTC().Format(time.RFC3339)
			if st.Onboarding == nil {
				st.Onboarding = &entry.Onboarding{Shown: now}
			}
			st.Onboarding.Skipped = true
			return false, nil
		})
		toast, s.Open = t.s("跳过了；以后在「起步」里还能回答", "Skipped; you can still answer under Start"), ""
	case strings.HasPrefix(act, "act:"):
		verb, id, _ := strings.Cut(strings.TrimPrefix(act, "act:"), ":")
		value := ""
		if verb == curate.ActEdit {
			value = strings.TrimSpace(values["text"])
			if value == "" {
				return t.s("正文不能是空的；要去掉这条请用「删掉」", "The text cannot be empty; use Delete to remove the entry"), true, nil
			}
		}
		err = write(func(st *store.State) (bool, error) {
			return verb == curate.ActDelete, c.UserAct(id, verb, value)
		})
		if err == nil {
			toast = map[string]string{curate.ActEdit: t.s("改好了：以后整理不会覆盖你的版本", "Saved: curation never overwrites your wording"),
				curate.ActPin: t.s("钉住了", "Pinned"), curate.ActUnpin: t.s("取消钉住了", "Unpinned"),
				curate.ActDelete:  t.s("删掉了，也撤出了出口；可以恢复", "Deleted and withdrawn from the exits; it can be restored"),
				curate.ActRestore: t.s("恢复了；下一次整理后回到出口", "Restored; back in the exits after the next run")}[verb]
		}
	case strings.HasPrefix(act, "pick:"):
		keep, drop, _ := strings.Cut(strings.TrimPrefix(act, "pick:"), ":")
		err = write(func(st *store.State) (bool, error) {
			if err := c.UserAct(keep, curate.ActPin, ""); err != nil {
				return false, err
			}
			return true, c.UserAct(drop, curate.ActWrong, "picked the other in a conflict")
		})
		toast = t.s("按你选的那条：另一条撤出出口，还留着", "Kept the one you picked; the other is withdrawn from the exits and kept")
	case act == "mark":
		n := 0
		err = write(func(st *store.State) (bool, error) {
			withdraw := false
			for k, v := range values {
				if v == "" || v == "none" {
					continue
				}
				switch {
				case strings.HasPrefix(k, "m:"):
					id := strings.TrimPrefix(k, "m:")
					e := st.Entries[id]
					if e == nil || lastMark(e.Verdicts) == v {
						continue
					}
					verb, note := v, ""
					if v == "unsupported" {
						verb, note = curate.ActWrong, "unsupported"
					}
					if err := c.UserAct(id, verb, note); err != nil {
						return false, err
					}
					n++
					withdraw = withdraw || verb == curate.ActWrong
				case strings.HasPrefix(k, "s:"):
					for _, chk := range st.SpeakerChecks {
						if speakerKey(chk) == k {
							if err := c.MarkSpeakerCheck(chk.Ref, chk.Day, chk.N, v); err != nil {
								return false, err
							}
							n++
							break
						}
					}
				}
			}
			return withdraw, nil
		})
		if err == nil && n > 0 {
			toast = t.s("记下了", "Noted")
			if values != nil && slices.ContainsFunc(mapValues(values), func(v string) bool { return v == "wrong" || v == "unsupported" }) {
				toast = t.s("记成不对：已撤出出口，留作反例", "Marked wrong: withdrawn from the exits and kept as a counter-example")
			}
		}
	case act == "switch":
		for k, v := range values {
			key, ok := strings.CutPrefix(k, "sw:")
			if !ok {
				continue
			}
			value := map[string]string{"true": "on", "false": "off"}[v]
			if err = switchKeyOK(key, value); err != nil {
				return "", true, err
			}
			err = write(func(st *store.State) (bool, error) {
				setSwitch(st, key, value, time.Now())
				return value == "off" || strings.HasPrefix(key, entry.K13Sensitive), nil
			})
			toast = t.f("%s 改成%s", "%s set %s", key, onOff(t, value))
		}
	case act == "k2" || act == "k17":
		for k, v := range values {
			key := ""
			switch {
			case act == "k17" && k == "k17":
				key = entry.K17DigScope
			case act == "k2" && strings.HasPrefix(k, "k2:"):
				key = entry.K2Judgement + "." + strings.TrimPrefix(k, "k2:")
			default:
				continue
			}
			if err = switchKeyOK(key, v); err != nil {
				return "", true, err
			}
			err = write(func(st *store.State) (bool, error) {
				setSwitch(st, key, v, time.Now())
				return v == "off", nil
			})
			toast = t.f("%s 改成%s", "%s set %s", key, onOff(t, v))
		}
	case act == "people:merge":
		persons := splitMulti(values["merge_people"])
		if len(persons) < 2 {
			return t.s("至少选两张卡", "Pick at least two cards"), true, nil
		}
		err = write(func(st *store.State) (bool, error) {
			if m, ok := any(c).(personMerger); ok {
				_, err := m.MergePersons(persons, strings.TrimSpace(values["merge_name"]))
				return false, err
			}
			mergePersons(st, persons, strings.TrimSpace(values["merge_name"]), time.Now())
			return false, nil
		})
		toast = t.s("合成一个人了：只记在插件里，可以拆开", "Merged: kept in the curator only, and can be split again")
	case strings.HasPrefix(act, "people:split:"):
		id := strings.TrimPrefix(act, "people:split:")
		err = write(func(st *store.State) (bool, error) {
			if m, ok := any(c).(personMerger); ok {
				return false, m.SplitPersons(id)
			}
			st.PersonMerges = slices.DeleteFunc(st.PersonMerges, func(m entry.PersonMerge) bool { return m.ID == id })
			return false, nil
		})
		toast, s.Open = t.s("拆开了", "Split"), ""
	case act == "people:add" || strings.HasPrefix(act, "people:addone:") || strings.HasPrefix(act, "people:remove:"):
		who, choice := values["add_person"], people.Added
		switch {
		case strings.HasPrefix(act, "people:addone:"):
			who = strings.TrimPrefix(act, "people:addone:")
		case strings.HasPrefix(act, "people:remove:"):
			who, choice = strings.TrimPrefix(act, "people:remove:"), people.Removed
		}
		if !strings.HasPrefix(who, "person:") {
			return t.s("选一个人", "Pick someone"), true, nil
		}
		err = write(func(st *store.State) (bool, error) {
			people.Set(st.Contacts, who, personName(st, who, t), choice, time.Now().In(c.Loc).Format("2006-01-02"))
			return choice == people.Removed, nil
		})
		toast = map[string]string{people.Added: t.s("加到名单了", "Added to the list"), people.Removed: t.s("移出名单了", "Removed from the list")}[choice]
	case strings.HasPrefix(act, "type:"):
		op, f, _ := strings.Cut(strings.TrimPrefix(act, "type:"), ":")
		te, ok := any(c).(typeEditor)
		if !ok {
			return t.s("改类型要等整理包（P3）合入后才能用：现在不改条目", "Renaming and merging types needs the consolidation package (P3); nothing was changed"), true, nil
		}
		err = write(func(st *store.State) (bool, error) {
			if op == "rename" {
				to := strings.TrimSpace(values["type_to"])
				if to == "" {
					return false, fmt.Errorf("%s", t.s("新名字不能是空的", "the new name is empty"))
				}
				return false, te.RenameType(entry.Facet(f), values["type_from"], to)
			}
			from := slices.DeleteFunc(splitMulti(values["type_merge"]), func(x string) bool { return x == values["type_into"] })
			if len(from) == 0 {
				return false, fmt.Errorf("%s", t.s("选要合进去的类型", "pick the types to merge"))
			}
			return false, te.MergeTypes(entry.Facet(f), values["type_into"], from...)
		})
		toast = t.s("改好了", "Done")
	default:
		return "", false, nil
	}
	return toast, true, err
}

func mapValues(m map[string]string) []string {
	var out []string
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// splitMulti reads a multichoice field's value: the apps send the chosen values joined by commas (or newlines).
func splitMulti(v string) []string {
	var out []string
	for _, x := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == '\n' }) {
		if x = strings.TrimSpace(x); x != "" && !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// mergePersons records that several person entries are one person (5.7): a person already in a merge brings it
// along. Kept in the curator only.
func mergePersons(st *store.State, persons []string, name string, now time.Time) {
	var all []string
	keep := st.PersonMerges[:0]
	for _, m := range st.PersonMerges {
		if slices.ContainsFunc(m.Persons, func(p string) bool { return slices.Contains(persons, p) }) {
			all = append(all, m.Persons...)
			if name == "" {
				name = m.Name
			}
			continue
		}
		keep = append(keep, m)
	}
	all = uniq(append(all, persons...))
	st.PersonMerges = append(keep, entry.PersonMerge{ID: "pm-" + hashKey(all...), Persons: all, Name: name, At: now.UTC().Format(time.RFC3339)})
}

func uniq(xs []string) []string {
	var out []string
	for _, x := range xs {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// withdraw compiles the exits at once after a change that can only take things out ("wrong", delete, a switch
// off), so they are gone from the exits now rather than after the next daily pass. Only what is in the exits now is
// compiled (nothing new comes in here: new entries wait for the daily pass, which also rewrites them), and without a
// model (a line already rewritten is reused; nothing waits for the model, the hub gives a page 20 seconds). Call it
// with the store's lock held.
func (a *app) withdraw(ctx context.Context) {
	c := a.c
	s := c.St.S
	in := exitIndex(c.St.Dir)
	if len(in) == 0 {
		return // nothing in any exit yet
	}
	clone, err := s.Clone(time.Now())
	if err != nil {
		a.logf("enkeep-memory: withdraw: %v", err)
		return
	}
	for id, e := range clone.Entries {
		// Cards stay: their evaluations and inferences make never_quote.txt, and their tips exit E.
		if len(in[id]) == 0 && e.Facet != entry.People {
			delete(clone.Entries, id)
		}
	}
	lang := c.Cfg.Language
	if lang == "" {
		lang = c.Lang
	}
	rep, err := exits.Compile(ctx, clone, exits.Options{Dir: c.St.Dir, Lang: lang, Loc: c.Loc, Hub: c.Hub, Run: "page",
		Event: func(ev entry.Event) { _ = c.St.AppendEvents(ev) }, Logf: a.logf})
	if err != nil {
		a.logf("enkeep-memory: withdraw: %v", err)
		return
	}
	s.Exits = clone.Exits
	if rep.Deferred {
		a.logf("enkeep-memory: withdraw: exit A held back (it would add entries)")
	}
}
