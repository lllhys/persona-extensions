package people

import (
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
)

func ls(ss ...string) []norm.Line {
	var out []norm.Line
	for i, s := range ss {
		out = append(out, norm.Parse(i+1, s))
	}
	return out
}

var owner = []string{"user-a"}

func TestEmptyList(t *testing.T) {
	list := map[string]*entry.Contact{}
	Observe(list, "2026-01-01", nil, owner)
	if len(Ranked(list)) != 0 || On(list, "person:x") {
		t.Fatal("an empty start gives an empty list")
	}
}

func TestDealings(t *testing.T) {
	list := map[string]*entry.Contact{}
	names := map[string]string{"p1": "person-b", "p2": "person-c", "p3": "person-d", "p4": "person-e", "p5": "person-f"}
	us := []Unit{
		{Entity: "chats", Source: "lark", Kind: "dm", Members: []string{"p1"}, Names: names,
			Lines: ls("10:00 person-b: hi"), Who: map[int]string{1: "person:p1"}},
		{Entity: "chats", Source: "lark", Kind: "group", Members: []string{"p2", "p3", "p4"}, Names: names,
			Lines: ls("10:00 我: hello all", "10:01 person-c: hi", "10:02 person-d: talking among ourselves"),
			Who:   map[int]string{1: entry.WhoSelf, 2: "person:p2", 3: "person:p3"}},
		// A group the user did not speak in: only the person who @-mentioned the user is a dealing.
		{Entity: "chats", Source: "lark", Kind: "group", Members: []string{"p4", "p5"}, Names: names,
			Lines: ls("11:00 person-e: @user-a can you look", "11:01 person-f: unrelated"),
			Who:   map[int]string{1: "person:p4", 2: "person:p5"}},
		{Entity: "chats", Source: "mail", Kind: "mail", Mine: true, Members: []string{"p5"}, Names: names,
			Lines: ls("12:00 person-f: re: the plan"), Who: map[int]string{1: "person:p5"}},
		// Unresolved lines teach nothing.
		{Entity: "chats", Source: "lark", Kind: "group", Members: []string{"p9"},
			Lines: ls("13:00 我: x", "13:01 same-name: y"), Who: map[int]string{1: entry.WhoSelf, 2: ""}},
	}
	Observe(list, "2026-01-01", us, owner)
	want := map[string]map[string]int{
		"person:p1": {TieDM: 1}, "person:p2": {TieGroup: 1}, "person:p3": {TieGroup: 1},
		"person:p4": {TieGroup: 1}, "person:p5": {TieMail: 1},
	}
	if len(list) != len(want) {
		t.Fatalf("list %v", keys(list))
	}
	for who, ties := range want {
		c := list[who]
		if c == nil {
			t.Fatalf("%s missing", who)
		}
		for k, v := range ties {
			if c.Ties[k] != v {
				t.Errorf("%s %s = %d, want %d", who, k, c.Ties[k], v)
			}
		}
	}
	if list["person:p1"].Name != "person-b" {
		t.Errorf("name %q", list["person:p1"].Name)
	}
	if r := Ranked(list); r[0].Person != "person:p1" {
		t.Errorf("a direct chat ranks first: %s", r[0].Person)
	}
}

func TestUserMentionIsADealing(t *testing.T) {
	list := map[string]*entry.Contact{}
	names := map[string]string{"p1": "person-b"}
	Observe(list, "2026-01-02", []Unit{{Entity: "chats", Kind: "group", Members: []string{"p1"}, Names: names,
		Lines: ls("10:00 我: @person-b please check"), Who: map[int]string{1: entry.WhoSelf}}}, owner)
	if list["person:p1"] == nil || list["person:p1"].Ties[TieGroup] != 1 {
		t.Fatalf("the user's @-mention: %v", list["person:p1"])
	}
	names["p2"] = "person-c"
	Observe(list, "2026-01-03", []Unit{{Entity: "chats", Kind: "group", Members: []string{"p2"}, Names: names,
		Lines: ls("10:00 我: （回复 person-c：the old message）agreed"), Who: map[int]string{1: entry.WhoSelf}}}, owner)
	if list["person:p2"] == nil || list["person:p2"].Ties[TieGroup] != 1 {
		t.Fatalf("the user's reply: %v", list["person:p2"])
	}
}

func TestManualChoicesKept(t *testing.T) {
	list := map[string]*entry.Contact{}
	Observe(list, "2026-01-01", []Unit{{Entity: "chats", Kind: "dm", Lines: ls("10:00 person-b: hi"), Who: map[int]string{1: "person:p1"}}}, owner)
	Set(list, "person:p1", "", Removed, "2026-01-02")
	Set(list, "person:p7", "person-g", Added, "2026-01-02")
	Observe(list, "2026-01-03", []Unit{{Entity: "chats", Kind: "dm", Lines: ls("10:00 person-b: again"), Who: map[int]string{1: "person:p1"}}}, owner)
	if On(list, "person:p1") {
		t.Fatal("a removed person came back with new dealings")
	}
	if list["person:p1"].Ties[TieDM] != 2 {
		t.Fatal("counts go on behind the user's choice")
	}
	if !On(list, "person:p7") || len(Ranked(list)) != 1 {
		t.Fatalf("added person: %v", Ranked(list))
	}
}

func TestCommitmentsAndMeetings(t *testing.T) {
	list := map[string]*entry.Contact{}
	Observe(list, "2026-01-01", []Unit{{Entity: "meetings", Members: []string{"p3"}}}, owner)
	entries := map[string]*entry.Entry{
		"w-1": {Commitment: &entry.Commitment{From: entry.WhoSelf, To: "person:p2", What: "send the draft"}, First: "2026-01-01", Last: "2026-01-01"},
		"w-2": {Commitment: &entry.Commitment{From: "person:p2", To: entry.WhoSelf, What: "review"}, First: "2026-01-02", Last: "2026-01-02"},
		"w-3": {Commitment: &entry.Commitment{From: "person:p4", To: "person:p5", What: "not the user's"}},
	}
	Commitments(list, entries, func(string) string { return "person-c" })
	Commitments(list, entries, nil) // idempotent
	if c := list["person:p2"]; c == nil || c.Ties[TieCommitment] != 2 || c.Name != "person-c" {
		t.Fatalf("commitments: %+v", c)
	}
	if list["person:p4"] != nil || list["person:p5"] != nil {
		t.Fatal("a promise between two others is no dealing of the user's")
	}
	if list["person:p3"].Ties[TieMeeting] != 1 {
		t.Fatal("meeting")
	}
}

func keys(m map[string]*entry.Contact) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
