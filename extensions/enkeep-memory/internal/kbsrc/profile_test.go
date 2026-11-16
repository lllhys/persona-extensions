package kbsrc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// Placeholders only.

func fact(text string, keep int, src ...string) ProfileCandidate {
	return ProfileCandidate{Part: PartFacts, Item: ProfileItem{Text: text, Sources: src}, Keep: keep}
}

func guessC(text, basis string, keep int, src ...string) ProfileCandidate {
	return ProfileCandidate{Part: PartInferences, Item: ProfileItem{Text: text, Basis: basis, Sources: src}, Keep: keep}
}

func view(text string, keep int, src ...string) ProfileCandidate {
	return ProfileCandidate{Part: PartViews, Item: ProfileItem{Text: text, Sources: src}, Keep: keep}
}

// Over every bound at once: texts and bases shortened to what the hub keeps, at most 30 a part, at most 8000 bytes;
// the hub's own rules take the result.
func TestFitProfileBounds(t *testing.T) {
	long := strings.Repeat("长", 600)
	var cands []ProfileCandidate
	for i := 0; i < 40; i++ {
		cands = append(cands, fact(fmt.Sprintf("fact-%02d %s", i, long), 3_000_000+i, "chats/c1/messages/2026-11-01.md"))
		cands = append(cands, guessC(fmt.Sprintf("guess-%02d", i), strings.Repeat("据", 400), i, "chats/c2/messages/2026-11-02.md"))
	}
	cands = append(cands, view("words", 4_000_000, "user"))
	cands = append(cands, ProfileCandidate{Part: PartCommitments, Commitment: ProfileCommitment{Text: "send-x", Owes: "them", Due: "bad", Sources: []string{"user"}}, Keep: 2_000_000})
	p, st := FitProfile(cands)
	if err := CheckProfile(&p); err != nil {
		t.Fatal(err)
	}
	for _, list := range [][]ProfileItem{p.Facts, p.Views, p.Inferences} {
		if len(list) > ProfileItemsMost {
			t.Fatalf("%d items", len(list))
		}
		for _, it := range list {
			if n := utf8.RuneCountInString(it.Text); n > ProfileStoredMost || n > ProfileTextMost {
				t.Fatalf("text %d", n)
			}
			if n := utf8.RuneCountInString(it.Basis); n > ProfileStoredMost || n > ProfileBasisMost {
				t.Fatalf("basis %d", n)
			}
		}
	}
	if n := ProfileBytes(&p); n > ProfileBytesMost {
		t.Fatalf("%d bytes", n)
	}
	if len(p.Views) != 1 || (len(p.Commitments) == 1 && p.Commitments[0].Due != "") {
		t.Fatalf("views %d commitments %+v", len(p.Views), p.Commitments)
	}
	// Commitments outrank inferences, facts outrank commitments.
	if len(p.Commitments) == 0 && len(p.Inferences) > 0 {
		t.Fatal("a commitment dropped before an inference")
	}
	// Facts outrank inferences: every inference goes before a fact does.
	if len(p.Inferences) > 0 && len(p.Facts) < 30 {
		t.Fatalf("an inference kept while facts were dropped: %d facts, %d inferences", len(p.Facts), len(p.Inferences))
	}
	if st.Shortened == 0 || st.Dropped == 0 {
		t.Fatalf("stats %+v", st)
	}
	t.Logf("facts %d views %d inferences %d commitments %d, %d bytes, %+v", len(p.Facts), len(p.Views), len(p.Inferences), len(p.Commitments), ProfileBytes(&p), st)
}

// Over 8000 bytes with room in every part: the inferences with least evidence go first, then the rest by Keep; the
// user's words and facts stay.
func TestFitProfilePriority(t *testing.T) {
	text := strings.Repeat("字", 190)
	var cands []ProfileCandidate
	for i := 0; i < 10; i++ {
		cands = append(cands, view(fmt.Sprintf("v%d %s", i, text), 4_000_000, "user"))
		cands = append(cands, fact(fmt.Sprintf("f%d %s", i, text), 3_000_000+i, "recordings/r1"))
		cands = append(cands, guessC(fmt.Sprintf("g%d %s", i, text), "basis", i*10, "recordings/r2"))
	}
	p, st := FitProfile(cands)
	if ProfileBytes(&p) > ProfileBytesMost || st.Dropped == 0 {
		t.Fatalf("%d bytes, %+v", ProfileBytes(&p), st)
	}
	if len(p.Views) != 10 {
		t.Fatalf("views %d", len(p.Views))
	}
	// The kept inferences are the best evidenced ones, in their order.
	for i, g := range p.Inferences {
		want := fmt.Sprintf("g%d ", 10-len(p.Inferences)+i)
		if !strings.HasPrefix(g.Text, want) {
			t.Fatalf("inference %d %q, want %q…", i, g.Text[:4], want)
		}
	}
	if len(p.Inferences) > 0 && len(p.Facts) != 10 {
		t.Fatalf("facts dropped before inferences: %d", len(p.Facts))
	}
}

// No source, or only one too long for the hub to keep whole: not written, counted.
func TestFitProfileSources(t *testing.T) {
	p, st := FitProfile([]ProfileCandidate{
		fact("a", 1),
		fact("b", 1, "https://example.com/"+strings.Repeat("x", 300)),
		fact("c", 1, "s1", "s1", "s2", "s3", "s4", "s5", "s6"),
		guessC("d", "", 1, "s1"),
	})
	if st.NoSource != 2 || len(p.Facts) != 1 || len(p.Facts[0].Sources) != 5 || len(p.Inferences) != 0 {
		t.Fatalf("%+v %+v", p, st)
	}
}

// The fixture holds a profile write to the hub's rules: refused whole, replaced, removed, read back.
func TestFixtureProfiles(t *testing.T) {
	fx := sample(t)
	ctx := context.Background()
	if _, _, err := fx.UpdateProfile(ctx, "nobody", &PersonProfile{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown person: %v", err)
	}
	var many PersonProfile
	for i := 0; i < 31; i++ {
		many.Facts = append(many.Facts, ProfileItem{Text: "x", Sources: []string{"user"}})
	}
	for _, bad := range []*PersonProfile{
		&many,
		{Inferences: []ProfileItem{{Text: "x", Sources: []string{"user"}}}},
		{Facts: []ProfileItem{{Text: "x"}}},
		{Commitments: []ProfileCommitment{{Text: "x", Owes: "both", Sources: []string{"user"}}}},
		{Facts: []ProfileItem{{Text: strings.Repeat("x", 501), Sources: []string{"user"}}}},
	} {
		if _, _, err := fx.UpdateProfile(ctx, "lark-f0", bad); !errors.Is(err, ErrInvalidProfile) {
			t.Fatalf("taken: %v", err)
		}
	}
	one := &PersonProfile{Facts: []ProfileItem{{Text: "one", Sources: []string{"user"}}}}
	before, _, err := fx.UpdateProfile(ctx, "lark-f0", one)
	if err != nil || string(before) != "null" {
		t.Fatalf("%s %v", before, err)
	}
	two := &PersonProfile{Views: []ProfileItem{{Text: "two", Sources: []string{"user"}}}}
	before, _, _ = fx.UpdateProfile(ctx, "lark-f0", two)
	if b, _ := json.Marshal(one); string(before) != string(b) {
		t.Fatalf("before %s", before)
	}
	if got, _ := fx.ReadProfile(ctx, "lark-f0"); got == nil || got.Views[0].Text != "two" {
		t.Fatalf("read back %+v", got)
	}
	if _, ok := fx.Profiles("lark-f0")["ext:"+Extension]; !ok {
		t.Fatal("not under ext:<id>")
	}
	_, _, _ = fx.UpdateProfile(ctx, "lark-f0", &PersonProfile{})
	if got, _ := fx.ReadProfile(ctx, "lark-f0"); got != nil {
		t.Fatalf("empty did not remove: %+v", got)
	}
}

type wrapped struct {
	Source
	extra int
}

// The profile writer is found behind wrappers that embed the source; a dry run records instead of writing.
func TestProfilesOf(t *testing.T) {
	fx := sample(t)
	if ProfilesOf(&wrapped{Source: &wrapped{Source: fx}}) != ProfileWriter(fx) {
		t.Fatal("not found behind wrappers")
	}
	if ProfilesOf(&wrapped{}) != nil {
		t.Fatal("a writer from nothing")
	}
	ctx := context.Background()
	d := &DryRun{Source: &wrapped{Source: fx}}
	if ProfilesOf(d) != ProfileWriter(d) {
		t.Fatal("dry run not its own writer")
	}
	p := &PersonProfile{Facts: []ProfileItem{{Text: "one", Sources: []string{"user"}}}}
	if _, _, err := d.UpdateProfile(ctx, "lark-f0", p); err != nil {
		t.Fatal(err)
	}
	if got, _ := fx.ReadProfile(ctx, "lark-f0"); got != nil {
		t.Fatal("a dry run wrote")
	}
	if got, _ := d.ReadProfile(ctx, "lark-f0"); got == nil || len(d.ProfileWrites) != 1 {
		t.Fatal("a dry run does not read its own write back")
	}
}

// sample is the plugin's synthetic sample knowledge base.
func sample(t *testing.T) *Fixture {
	t.Helper()
	fx, err := LoadFixture("../../testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	return fx
}

// On a knowledge base without profiles the probe changes nothing: the person's files keep their bytes and times,
// and no write is recorded.
func TestProbeLeavesOldHubUntouched(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, "people", "person-a")
	if err := os.MkdirAll(pdir, 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"meta.json": `{"id":"person-a","name":"user-a"}`, "summary.md": "user-a\n"}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(pdir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	for name := range files {
		if err := os.Chtimes(filepath.Join(pdir, name), old, old); err != nil {
			t.Fatal(err)
		}
	}
	fx, err := LoadFixture(dir)
	if err != nil {
		t.Fatal(err)
	}
	fx.NoProfiles = true
	dry := &DryRun{Source: fx}
	pr := ProberOf(dry)
	if pr == nil {
		t.Fatal("no prober behind a dry run")
	}
	ok, err := pr.ProbeProfile(context.Background(), "person-a")
	if err != nil || ok {
		t.Fatalf("probe on a knowledge base without profiles: %v %v", ok, err)
	}
	for name, body := range files {
		st, err := os.Stat(filepath.Join(pdir, name))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(filepath.Join(pdir, name))
		if !st.ModTime().Equal(old) || string(b) != body {
			t.Fatalf("%s changed: %s %q", name, st.ModTime(), b)
		}
	}
	if len(fx.Writes()) != 0 || len(dry.Writes) != 0 || len(dry.ProfileWrites) != 0 {
		t.Fatal("the probe recorded a write")
	}
	if p, _ := fx.ReadProfile(context.Background(), "person-a"); p != nil {
		t.Fatalf("a profile appeared: %+v", p)
	}
}
