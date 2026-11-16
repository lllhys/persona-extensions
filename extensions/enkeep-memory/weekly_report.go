package main

// The weekly report (design §6.1 G): once a week, by the hub's time zone, after the weekly pass of the week before,
// a notification to the user only. Weeks with nothing new are not pushed. In the target language. It says what took
// effect (undoable), which fork types were switched to acting on their own and on which answers, the commitments
// coming due or possibly overdue, every facet's reading ("not measurable" when it is), exit warnings and the quiet
// confirmed entries asked about once; and, the first time, how the people cards are used (§14 item 12).

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
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/curate"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// weeklyLedgerFile keeps what the weekly report did, in the data directory (not in the state: the page owns it).
const weeklyLedgerFile = "weekly-report.json"

// flagAskedDormant is P3's flag on a quiet, confirmed entry the weekly report asks about once (curate thresholds).
const flagAskedDormant = "asked_dormant"

type weeklyLedger struct {
	Week      string         `json:"week"`             // the last week looked at
	At        string         `json:"at,omitempty"`     // when
	Pushed    bool           `json:"pushed"`           // whether it was sent (false: nothing new, or the notify failed)
	Err       string         `json:"error,omitempty"`  // the notify's error
	Asked     []string       `json:"asked,omitempty"`  // quiet entries already asked about
	ToldCards bool           `json:"told_cards"`       // the note on how the cards are used was sent once
	Counts    map[string]int `json:"counts,omitempty"` // the last report's sections
}

func loadLedger(dir string) *weeklyLedger {
	led := &weeklyLedger{}
	if raw, err := os.ReadFile(filepath.Join(dir, weeklyLedgerFile)); err == nil {
		_ = json.Unmarshal(raw, led)
	}
	return led
}

func saveLedger(dir string, led *weeklyLedger) error {
	b, _ := json.MarshalIndent(led, "", " ")
	tmp := filepath.Join(dir, weeklyLedgerFile+".tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, weeklyLedgerFile))
}

// weeklyReport is one week's report.
type weeklyReport struct {
	Title   string         `json:"title"`
	Body    string         `json:"body"`
	Changed bool           `json:"changed"` // anything new: a week without is not pushed
	Counts  map[string]int `json:"counts"`  // section → items
	Asked   []string       `json:"asked,omitempty"`
}

// readingFacet maps a reading code to its facet's number.
var readingFacet = []struct{ code, facet string }{
	{"R1", "①"}, {"R2", "①"}, {"R3", "②"}, {"R4", "③"}, {"R5", "④"}, {"R6", "⑤"}, {"F1", "⑤"}, {"R8", "⑤′"}, {"R7", "⑥"},
	{"U1", "①"}, {"U2", "②"}, {"U3", "③"}, {"U4", "④"}, {"U5", "⑤"}, {"U7", "⑤′"}, {"U8", "⑤′"}, {"U6", "⑥"},
}

func readingLabel(t tr, code string) string {
	switch code {
	case "R1":
		return t.s("每百条消息里纠正 AI 的次数", "corrections per 100 messages")
	case "R2":
		return t.s("请示被回「说过了」", "questions answered \"I told you\"")
	case "R3":
		return t.s("问你机器、账号、用哪个工具", "questions about machines, accounts, tools")
	case "R4":
		return t.s("「按以往会选」被采纳", "\"you would usually pick\" taken")
	case "R5":
		return t.s("为做法不对被纠正", "corrections of how things were done")
	case "R6":
		return t.s("知识库里查到插件写回的", "lookups that hit what the curator wrote")
	case "R7":
		return t.s("为说法、语气被纠正", "corrections of wording and tone")
	case "R8":
		return t.s("承诺提醒被认可", "commitment reminders accepted")
	case "F1":
		return t.s("事实核对对的比例", "facts checked right")
	}
	if strings.HasPrefix(code, "U") {
		return t.s("你核对「对」的比例", "share you marked right")
	}
	return code
}

// buildWeekly is the report of week (ISO, "2026-W45") whose days are days, as of today, by loc, in lang.
func buildWeekly(st *store.State, week string, days []string, today, lang string, loc *time.Location, led *weeklyLedger) weeklyReport {
	t := trOf(lang)
	rep := weeklyReport{Counts: map[string]int{}}
	in := func(stamp string) bool {
		ts, err := time.Parse(time.RFC3339, stamp)
		if err != nil {
			return false
		}
		d := ts.In(loc).Format("2006-01-02")
		return d >= days[0] && d <= days[len(days)-1]
	}
	var b strings.Builder
	line := func(s string) { b.WriteString(s + "\n") }

	// 1. New in effect this week: what went into an exit for the first time.
	var fresh []*entry.Entry
	for _, e := range st.Entries {
		if e.Live() && e.Effective != "" && in(e.Effective) && e.Sensitive == "" && e.Part != entry.PartUserEval && e.Part != entry.PartInferred {
			fresh = append(fresh, e)
		}
	}
	sortEntries(fresh)
	rep.Counts["effective"] = len(fresh)
	if len(fresh) > 0 {
		line(t.f("## 新生效的 %d 条", "## %d new in effect", len(fresh)))
		for i, e := range fresh {
			if i >= 10 {
				line(t.f("……还有 %d 条，在插件页上看", "…%d more on the page", len(fresh)-i))
				break
			}
			line(fmt.Sprintf("- %s %s", facetShort(e.Facet), short(e.Shown(), 80)))
		}
		line(t.s("不对的话在插件页上点「不对」或删掉，立刻撤回。", "If one is wrong, mark it wrong or delete it on the page: it is withdrawn at once."))
	}

	// 2. Fork types switched to acting on reversible things by themselves (K2), and on which answers.
	var k2 []string
	for _, k := range sortedKeys(st.Switches) {
		s := st.Switches[k]
		base, typ, ok := strings.Cut(k, ".")
		if !ok || base != entry.K2Judgement || s.By != "auto" || s.Value != "act" {
			continue
		}
		// At is a time, or the day of the reading that moved it.
		if in(s.At) || (len(s.At) == 10 && s.At >= days[0] && s.At <= days[len(days)-1]) {
			k2 = append(k2, t.f("- 「%s」：%s", "- \"%s\": %s", typ, s.Why))
		}
	}
	rep.Counts["k2_auto"] = len(k2)
	if len(k2) > 0 {
		line(t.s("## 这些岔口类型开始按建议直接办（只限能撤回的事）", "## These fork types now act on the suggestion (reversible things only)"))
		for _, l := range k2 {
			line(l)
		}
		line(t.s("在插件页「开关」里一键改回「只附建议」。", "Switch any back to \"suggest\" on the page under Switches."))
	}

	// 3. Commitments coming due and possibly overdue.
	due, overdue := curate.CommitmentsDue(st, today)
	rep.Counts["commitments_due"], rep.Counts["commitments_overdue"] = len(due), len(overdue)
	if len(due)+len(overdue) > 0 {
		line(t.s("## 承诺", "## Commitments"))
		for i, e := range due {
			if i >= 8 {
				break
			}
			line(t.f("- 快到期（%s）：%s", "- due %s: %s", e.Commitment.Due, short(e.Shown(), 70)))
		}
		for i, e := range overdue {
			if i >= 8 {
				break
			}
			line(t.f("- 可能逾期（%s）：%s", "- possibly overdue (%s): %s", e.Commitment.Due, short(e.Shown(), 70)))
		}
	}

	// 4. Exit warnings.
	var warns []string
	if st.Exits != nil {
		warns = st.Exits.Warnings
	}
	rep.Counts["warnings"] = len(warns)
	if len(warns) > 0 {
		line(t.s("## 出口告警", "## Exit warnings"))
		for _, w := range warns {
			line("- " + w)
		}
	}

	// 5. Quiet confirmed entries, asked about once.
	var asks []*entry.Entry
	for _, e := range st.Entries {
		if e.Live() && slices.Contains(e.Flags, flagAskedDormant) && e.Sensitive == "" && (led == nil || !slices.Contains(led.Asked, e.ID)) {
			asks = append(asks, e)
		}
	}
	sortEntries(asks)
	rep.Counts["asks"] = len(asks)
	if len(asks) > 0 {
		line(t.s("## 好久没见到，还成立吗？", "## Not seen for a while: still true?"))
		for i, e := range asks {
			if i >= 8 {
				break
			}
			line(fmt.Sprintf("- %s %s", facetShort(e.Facet), short(e.Shown(), 80)))
			rep.Asked = append(rep.Asked, e.ID)
		}
		line(t.s("在插件页上点「对」或「不对」；不回答就照旧。", "Mark them right or wrong on the page; unanswered, they stay as they are."))
	}
	rep.Changed = len(fresh)+len(k2)+len(due)+len(overdue)+len(warns)+len(asks) > 0

	// 6. Every facet's reading; "not measurable" when it is.
	line(t.s("## 各方面读数（两周）", "## Readings (two weeks)"))
	latest := map[string]entry.Reading{}
	for _, r := range st.Readings {
		base, _, _ := strings.Cut(r.Code, ".")
		if base != r.Code {
			continue
		}
		if x, ok := latest[r.Code]; !ok || r.Period > x.Period {
			latest[r.Code] = r
		}
	}
	byFacet := map[string][]string{}
	for _, rf := range readingFacet {
		r, ok := latest[rf.code]
		if !ok {
			continue
		}
		v := t.s("量不出", "not measurable")
		if r.Measure && r.Den > 0 {
			v = fmt.Sprintf("%.1f（%d/%d）", r.Value, r.Num, r.Den)
		}
		byFacet[rf.facet] = append(byFacet[rf.facet], rf.code+" "+readingLabel(t, rf.code)+t.s("：", ": ")+v)
	}
	n := 0
	for _, f := range []string{"①", "②", "③", "④", "⑤", "⑤′", "⑥"} {
		if rs := byFacet[f]; len(rs) > 0 {
			line(fmt.Sprintf("- %s %s", f, strings.Join(rs, t.s("；", "; "))))
			n += len(rs)
		} else {
			line(fmt.Sprintf("- %s %s", f, t.s("量不出（还没有读数）", "not measurable (no reading yet)")))
		}
	}
	rep.Counts["readings"] = n

	// 7. Once: how the cards are used.
	if rep.Changed && (led == nil || !led.ToldCards) {
		line(t.s("## 画像卡会怎么用（只说这一次）", "## How the people cards are used (said once)"))
		line(t.s("画像卡只在你自己的知识库和插件页上。起草回复时只用来调语气和重点，评价和推断的原话不会写进发出去的内容；但语气本身可能让对方感觉到。不想要可以在「开关」里关掉 K14（整块）或 K15（推断）。",
			"The cards live only in your own knowledge base and on the page. When a reply is drafted they only tune tone and emphasis, and evaluations and inferences are never written into what is sent; the tone itself may still show. Turn off K14 (all of it) or K15 (inferences) under Switches if you prefer."))
		rep.Counts["cards_note"] = 1
	}
	rep.Title = t.f("enkeep 记忆 · %s 周报", "enkeep memory · week %s", week)
	rep.Body = strings.TrimSpace(b.String())
	return rep
}

// weeklyTick sends the report of the week before, by the hub's time zone, once its weekly pass has run. A week is
// looked at once: with nothing new it is not pushed; a failed notify is noted and not retried every minute.
func (a *app) weeklyTick(ctx context.Context, now time.Time) error {
	loc := a.hubLoc
	if loc == nil {
		loc = a.c.Loc
	}
	now = now.In(loc)
	monday := now.AddDate(0, 0, -int((now.Weekday()+6)%7))
	week := curate.ISOWeek(monday.AddDate(0, 0, -1).Format("2006-01-02"))
	if !a.ran("weekly", week) {
		return nil
	}
	dir := a.c.St.Dir
	led := loadLedger(dir)
	if led.Week == week {
		return nil
	}
	days, err := curate.WeekDays(week)
	if err != nil {
		return err
	}
	lang := a.c.Cfg.Language
	if lang == "" {
		lang = a.c.Lang
	}
	st, done := a.read()
	rep := buildWeekly(st, week, days, now.Format("2006-01-02"), lang, loc, led)
	done()
	led.Week, led.At, led.Pushed, led.Err, led.Counts = week, now.UTC().Format(time.RFC3339), false, "", rep.Counts
	if rep.Changed {
		if a.hubAPI == nil {
			err = errors.New("no hub")
		} else {
			err = a.hubAPI.Notify(ctx, rep.Title, rep.Body)
		}
		if err != nil {
			led.Err = err.Error()
			a.logf("enkeep-memory: weekly report %s not sent: %v", week, err)
		} else {
			led.Pushed = true
			led.Asked = append(led.Asked, rep.Asked...)
			if rep.Counts["cards_note"] > 0 {
				led.ToldCards = true
			}
		}
	}
	sort.Strings(led.Asked)
	return saveLedger(dir, led)
}
