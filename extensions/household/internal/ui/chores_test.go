package ui

import (
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
	"github.com/nwverse/persona-extensions/extensions/household/internal/rules"
)

// TestChoresDueComeBeforeUpcoming checks the 到期 rows are the chores due, and 以后 holds the rest, after them.
func TestChoresDueComeBeforeUpcoming(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	h := home(t, st)
	v := render(t, a, state{T: choreTab})
	checkView(t, "chores", v, false)

	due := rules.ChoresDue(h, testToday)
	if len(due) == 0 || len(due) == len(h.Chores) {
		t.Fatalf("samples have %d of %d chores due: the test needs both kinds", len(due), len(h.Chores))
	}
	dueRows := rowsTitled(t, v, "到期")
	if len(dueRows) != len(due) {
		t.Fatalf("%d due rows, want %d", len(dueRows), len(due))
	}
	for i, r := range dueRows {
		if formID(r.Action) != due[i].Chore.ID {
			t.Fatalf("due row %d is %q, want the chore %q in due order", i, r.Title, due[i].Chore.Title)
		}
	}
	later := rowsTitled(t, v, "以后")
	if len(later) != len(h.Chores)-len(due) {
		t.Fatalf("%d upcoming rows, want %d", len(later), len(h.Chores)-len(due))
	}
	if sectionIndex(v, "到期") > sectionIndex(v, "以后") {
		t.Fatal("以后 comes before 到期")
	}
}

// sectionIndex is the position of the section with the title, -1 for none.
func sectionIndex(v *wire.View, title string) int {
	for i, s := range v.Sections {
		if s.Title == title {
			return i
		}
	}
	return -1
}

// TestChoreRowsCompleteEditAndDelete checks every chore row offers 完成, 编辑 and a confirmed 删除.
func TestChoreRowsCompleteEditAndDelete(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	v := render(t, a, state{T: choreTab})
	rows := 0
	for _, s := range v.Sections {
		if s.Title != "到期" && s.Title != "以后" {
			continue
		}
		for _, r := range rowsOf(s) {
			rows++
			id := formID(r.Action)
			done, ok := rowAction(r, "完成")
			if !ok || done.Action.ID != "chore:"+id || !done.Swipe {
				t.Fatalf("row %q 完成 action %+v", r.Title, done)
			}
			edit, ok := rowAction(r, "编辑")
			if !ok || formID(&edit.Action) != id {
				t.Fatalf("row %q 编辑 action %+v", r.Title, edit)
			}
			del, ok := rowAction(r, "删除")
			if !ok || del.Action.ID != "delete:"+id || del.Action.Confirm == "" || del.Role != wire.ButtonRoleDestructive {
				t.Fatalf("row %q 删除 action %+v", r.Title, del)
			}
		}
	}
	if rows != len(home(t, st).Chores) {
		t.Fatalf("%d chore rows checked, want one per chore", rows)
	}
}

// TestChoreDoneFromTheTabSchedulesTheNextRound checks 完成 sets the last day and the next day from the period.
func TestChoreDoneFromTheTabSchedulesTheNextRound(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	var target model.Chore
	for _, ch := range home(t, st).Chores {
		if ch.Every > 0 {
			target = ch
			break
		}
	}
	if target.ID == "" {
		t.Fatal("samples have no periodic chore")
	}
	res := act(t, a, state{T: choreTab}, nil, "chore:"+target.ID)
	toastHas(t, res, "已完成")
	got := findChore(home(t, st), target.ID)
	if got.Last != testToday {
		t.Fatalf("last %q, want today", got.Last)
	}
	if want := testToday.Add(int(target.Every)); got.Next != want {
		t.Fatalf("next %q, want %q", got.Next, want)
	}
}

// TestUpcomingChoreSaysInHowManyDays checks a chore not yet due says how many days away it is.
func TestUpcomingChoreSaysInHowManyDays(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	h := home(t, st)
	later, _ := sectionTitled(render(t, a, state{T: choreTab}), "以后")
	seen := false
	for _, r := range rowsOf(later) {
		ch := findChore(h, formID(r.Action))
		if ch.Next == "" {
			continue
		}
		seen = true
		if r.Badge == nil || !strings.HasSuffix(r.Badge.Text, "天后") {
			t.Fatalf("upcoming chore %q badge %+v, want N 天后", r.Title, r.Badge)
		}
	}
	if !seen {
		t.Fatal("no upcoming chore with a next date in the samples")
	}
}

// TestChoresEmptyStateOffersAdd checks an empty household shows a quiet line and the add button.
func TestChoresEmptyStateOffersAdd(t *testing.T) {
	a, _ := newApp(newStore(t, false))
	v := render(t, a, state{T: choreTab})
	checkView(t, "empty chores", v, false)
	if got := rowsTitled(t, v, "到期"); len(got) != 0 {
		t.Fatalf("%d due rows in an empty household", len(got))
	}
	b, ok := buttonTitled(v, "添加家务")
	if !ok || b.Action == nil || b.Action.Push == nil {
		t.Fatalf("添加家务 button %+v, want a push to its form", b)
	}
}
