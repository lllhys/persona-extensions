package speaker

import (
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona/sdk/go/wire"
)

func lines(ls ...string) []norm.Line {
	var out []norm.Line
	for i, l := range ls {
		out = append(out, norm.Parse(i+1, l))
	}
	return out
}

var owner = OwnerOf(wire.KBOwner{Name: "user-a", Aliases: []string{"ua"}, Handles: []wire.KBHandle{{Kind: "email", Value: "me@example.com"}}}, nil)

func TestGroupUniqueSelfAndSameName(t *testing.T) {
	u := Unit{Entity: "chats", Source: "lark", Kind: "group", Members: []Person{
		{ID: "lark-1", Name: "person-b"}, {ID: "lark-2", Name: "person-c"}, {ID: "lark-3", Name: "person-c"},
	}, Lines: lines("10:00 我: hello", "10:01 person-b: hi", "10:02 person-c: same name twice", "10:03 person-z: not a member", "10:04 ?: no name")}
	r := Resolve(u, owner)
	want := map[int]string{1: entry.WhoSelf, 2: "person:lark-1", 3: "", 4: "", 5: ""}
	for n, w := range want {
		if r.Who[n] != w {
			t.Errorf("line %d: who %q, want %q (%s)", n, r.Who[n], w, r.Why[n])
		}
	}
	if r.Why[3] != WhyAmbiguous {
		t.Errorf("same name: why %q, want ambiguous", r.Why[3])
	}
	if r.Why[4] != WhyNone {
		t.Errorf("not a member: why %q, want none (never looked up in the whole knowledge base)", r.Why[4])
	}
}

func TestRenamedPerson(t *testing.T) {
	// The display name changed: the knowledge base keeps the old one as an alias, so old and new lines are one
	// person. A rename it did not record leaves the old lines unresolved instead of guessing.
	u := Unit{Entity: "chats", Source: "lark", Kind: "group", Members: []Person{
		{ID: "lark-1", Name: "person-new", Aliases: []string{"person-old"}}, {ID: "lark-2", Name: "person-d"},
	}, Lines: lines("09:00 person-old: before the rename", "09:05 person-new: after the rename", "09:06 person-gone: renamed, not recorded")}
	r := Resolve(u, owner)
	if r.Who[1] != "person:lark-1" || r.Who[2] != "person:lark-1" {
		t.Fatalf("renamed: %v", r.Who)
	}
	if r.Who[3] != "" {
		t.Fatalf("unrecorded rename resolved to %q", r.Who[3])
	}
}

func TestDMOtherSide(t *testing.T) {
	u := Unit{Entity: "chats", Source: "wechat", Kind: "dm", Members: []Person{{ID: "wx-1", Name: "person-b"}},
		Lines: lines("10:00 我: hi", "10:01 remark-name: a remark name the member list does not have")}
	r := Resolve(u, owner)
	if r.Who[1] != entry.WhoSelf || r.Who[2] != "person:wx-1" {
		t.Fatalf("dm: %v", r.Who)
	}
}

func TestDMWithOwnEntryAmongMembers(t *testing.T) {
	// The user's own person entry at the source is a member too: it is recognised by name and left out.
	u := Unit{Entity: "chats", Source: "lark", Kind: "dm", Members: []Person{{ID: "lark-me", Name: "user-a"}, {ID: "lark-2", Name: "person-b"}},
		Lines: lines("10:00 person-b: hi")}
	if r := Resolve(u, owner); r.Who[1] != "person:lark-2" {
		t.Fatalf("dm with own entry: %v", r.Who)
	}
}

func TestWeChatGroupUnresolved(t *testing.T) {
	u := Unit{Entity: "chats", Source: "wechat", Kind: "group", Members: []Person{{ID: "wx-1", Name: "person-b"}},
		Lines: lines("10:00 person-b: hi", "10:01 我: mine")}
	r := Resolve(u, owner)
	if r.Who[1] != "" || r.Why[1] != WhyUnsupported {
		t.Fatalf("wechat group: %q %q", r.Who[1], r.Why[1])
	}
	if r.Who[2] != entry.WhoSelf {
		t.Fatalf("the user's own line in a wechat group: %q", r.Who[2])
	}
}

func TestBotsAndAssistantAreNobody(t *testing.T) {
	u := Unit{Entity: "chats", Source: "lark", Kind: "group", Members: []Person{{ID: "lark-1", Name: "alert-bot"}},
		Lines: lines("10:00 alert-bot: disk full", "10:01 助手: reply", "10:02 我·代写: drafted")}
	r := Resolve(u, owner)
	for n := 1; n <= 3; n++ {
		if r.Who[n] != "" {
			t.Errorf("line %d resolved to %q", n, r.Who[n])
		}
	}
}

func TestConversationRoleUserIsSelfWithoutOwner(t *testing.T) {
	empty := OwnerOf(wire.KBOwner{}, nil)
	u := Unit{Entity: "agents", Source: "persona", Lines: lines("10:00 user: please do x", "10:01 助手: done")}
	r := Resolve(u, empty)
	if r.Who[1] != entry.WhoSelf || r.Who[2] != "" {
		t.Fatalf("agent session: %v", r.Who)
	}
	if SelfOff(u, empty) != "" {
		t.Fatal("conversations are used whatever kb.owner says")
	}
}

func TestMailBySenderAndQuotedOlderMail(t *testing.T) {
	body := "10:00 person-b: Sounds good, shipping friday.\nOn Mon, person-c <c@example.com> wrote:\n> the old plan was monday"
	u := Unit{Entity: "chats", Source: "mail", Kind: "mail", Members: []Person{
		{ID: "mail-b@example.com", Name: "person-b", Handles: []string{"b@example.com"}},
		{ID: "mail-c@example.com", Name: "person-c", Handles: []string{"c@example.com"}},
	}, Lines: []norm.Line{norm.Parse(1, body), norm.Parse(5, "11:00 person-b: ---------- Forwarded message ---------\nFrom: person-c <c@example.com>\nold text"),
		norm.Parse(9, "12:00 me@example.com: from my own address")}}
	r := Resolve(u, owner)
	if r.Who[1] != "person:mail-b@example.com" {
		t.Fatalf("sender: %v", r.Who)
	}
	l := u.Lines[0]
	if Quoted(l.Body, "Sounds good, shipping friday") {
		t.Fatal("the sender's own words were taken as quoted")
	}
	if !Quoted(l.Body, "the old plan was monday") {
		t.Fatal("the quoted older mail counted as the outer sender's")
	}
	if r.Who[5] != "" || r.Why[5] != WhyQuoted {
		t.Fatalf("a pure forward is not the forwarder's words: %q %q", r.Who[5], r.Why[5])
	}
	if r.Who[9] != entry.WhoSelf {
		t.Fatalf("the owner's address: %q", r.Who[9])
	}
}

func TestRecordings(t *testing.T) {
	memoUnit := Unit{Entity: "recordings", Kind: "memo", Lines: lines("thinking aloud about project-x", "and the next step")}
	r := Resolve(memoUnit, owner)
	if r.Who[1] != entry.WhoSelf || r.Who[2] != entry.WhoSelf {
		t.Fatalf("voice memo: %v", r.Who)
	}
	meeting := Unit{Entity: "recordings", Members: []Person{{ID: "lark-1", Name: "person-b"}},
		Lines: lines("decision: ship on friday (who: person-b)", "we talked about the launch", "next: draft the plan (who: user-a)")}
	r = Resolve(meeting, owner)
	if r.Who[1] != "person:lark-1" || r.Who[3] != entry.WhoSelf {
		t.Fatalf("minutes with who: %v", r.Who)
	}
	if r.Who[2] != "" {
		t.Fatalf("a line without who resolved to %q", r.Who[2])
	}
	// A recording that is not marked as a memo is not taken as the user's, even with no other member.
	plain := Unit{Entity: "recordings", Lines: lines("someone talking")}
	if r := Resolve(plain, owner); r.Who[1] != "" {
		t.Fatalf("unmarked recording: %v", r.Who)
	}
}

func TestWeakOwner(t *testing.T) {
	for _, tc := range []struct {
		o    wire.KBOwner
		weak bool
	}{
		{wire.KBOwner{}, true},
		{wire.KBOwner{Name: "user-a"}, true},
		{wire.KBOwner{Name: "用户"}, true},
		{wire.KBOwner{Name: "user-a", Aliases: []string{"user-a"}}, true},
		{wire.KBOwner{Name: "user-a", Aliases: []string{"ua"}}, false},
		{wire.KBOwner{Name: "user-a", Handles: []wire.KBHandle{{Kind: "lark", Value: "ou_x"}}}, false},
	} {
		if got := OwnerOf(tc.o, nil).Weak; got != tc.weak {
			t.Errorf("%+v: weak %v, want %v", tc.o, got, tc.weak)
		}
	}
	weak := OwnerOf(wire.KBOwner{Name: "user-a"}, nil)
	for kind, off := range map[string]bool{"group": true, "mail": true, "dm": false} {
		if got := SelfOff(Unit{Entity: "chats", Source: "lark", Kind: kind}, weak) != ""; got != off {
			t.Errorf("%s: self off %v, want %v", kind, got, off)
		}
	}
	if SelfOff(Unit{Entity: "chats", Source: "lark", Kind: "group"}, owner) != "" {
		t.Error("a full kb.owner keeps groups")
	}
}
