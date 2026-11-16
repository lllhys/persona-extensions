package kbsrc

// Person profiles (hub docs/assistant/knowledge.md "Person profiles", nwverse/persona#5934): what this extension knows
// of a person, in four parts kept apart (facts, the user's own words, inferences, commitments), written with kb.update
// {kind: people, id, profile} and read back from kb.get's profiles by writer. The types follow the hub's wire
// definitions (KBPersonProfile, KBProfileItem, KBProfileCommitment, KBIgnoredField); the copied SDK (sdk/go) predates
// them and is not edited here, so the calls go through ext.Hub.Call.
//
// The hub refuses a profile that breaks one of its rules as a whole (invalid_params) and cuts nothing: FitProfile
// brings one within them first. A hub without #5934 drops the profile silently; ReadProfile tells.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nwverse/persona/sdk/go/ext"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/wire"
)

// PersonProfile is one writer's profile of a person (wire.KBPersonProfile upstream).
type PersonProfile struct {
	Facts       []ProfileItem       `json:"facts,omitempty"`
	Views       []ProfileItem       `json:"views,omitempty"`
	Inferences  []ProfileItem       `json:"inferences,omitempty"`
	Commitments []ProfileCommitment `json:"commitments,omitempty"`
}

// ProfileItem is a fact, a view (the user's words, quoted) or an inference (with its basis).
type ProfileItem struct {
	Text    string   `json:"text"`
	Basis   string   `json:"basis,omitempty"`
	Sources []string `json:"sources"`
}

// ProfileCommitment is a promise either way: owes "user" (the user to them) or "them" (they to the user).
type ProfileCommitment struct {
	Text    string   `json:"text"`
	Owes    string   `json:"owes"`
	Due     string   `json:"due,omitempty"`
	Done    bool     `json:"done,omitempty"`
	Sources []string `json:"sources"`
}

// IgnoredField is a field an update carried that the hub did not apply, and why.
type IgnoredField struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

// ProfileRecord is a profile as the hub keeps it (kb.get's profiles).
type ProfileRecord struct {
	Profile PersonProfile `json:"profile"`
	At      time.Time     `json:"at"`
}

// Empty says whether a profile has no items (writing it removes the writer's profile).
func (p *PersonProfile) Empty() bool {
	return p == nil || len(p.Facts)+len(p.Views)+len(p.Inferences)+len(p.Commitments) == 0
}

// Owes values.
const (
	OwesUser = "user" // the user owes it to them
	OwesThem = "them" // they owe it to the user
)

// SourceUser is the source of the user's own word.
const SourceUser = "user"

// The hub's bounds (server/internal/kb/profile.go checkProfile). StoredMost is what the hub keeps of every text,
// basis and source: it stores them on one line cut to 200 characters (oneLine), so anything longer would be cut
// there with an ellipsis; FitProfile shortens to it here instead, so what is read back is what was sent.
const (
	ProfileItemsMost   = 30
	ProfileTextMost    = 500
	ProfileBasisMost   = 300
	ProfileSourcesMost = 5
	ProfileSourceMost  = 300
	ProfileBytesMost   = 8000
	ProfileStoredMost  = 200
)

// ErrInvalidProfile is a profile breaking one of the hub's rules: nothing was written.
var ErrInvalidProfile = errors.New("invalid profile")

// ProfileWriter is a source that takes person profiles. UpdateProfile replaces this extension's profile of a person
// (an empty one removes it) and answers the one it replaced (JSON null for none) and the fields the hub did not use;
// ReadProfile reads this extension's profile back (nil: none, or a hub that does not keep profiles).
type ProfileWriter interface {
	UpdateProfile(ctx context.Context, personID string, p *PersonProfile) (before json.RawMessage, ignored []IgnoredField, err error)
	ReadProfile(ctx context.Context, personID string) (*PersonProfile, error)
}

// ProfilesOf is the profile writer behind a source: the source itself, or the one a wrapper embeds (convsrc,
// sources); nil when there is none.
func ProfilesOf(src Source) ProfileWriter {
	for depth := 0; src != nil && depth < 8; depth++ {
		if w, ok := src.(ProfileWriter); ok {
			return w
		}
		v := reflect.ValueOf(src)
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return nil
			}
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct {
			return nil
		}
		f := v.FieldByName("Source")
		if !f.IsValid() || !f.CanInterface() {
			return nil
		}
		inner, ok := f.Interface().(Source)
		if !ok {
			return nil
		}
		src = inner
	}
	return nil
}

// profileUpdate is kb.update with the profile the copied SDK does not know yet.
type profileUpdate struct {
	wire.KBUpdateParams
	Profile *PersonProfile `json:"profile"`
}

// ProfileUpdateJSON is the kb.update params of a profile write, as recorded with what was applied.
func ProfileUpdateJSON(personID string, p *PersonProfile) json.RawMessage {
	if p == nil {
		p = &PersonProfile{}
	}
	b, _ := json.Marshal(profileUpdate{KBUpdateParams: wire.KBUpdateParams{Kind: "people", ID: personID}, Profile: p})
	return b
}

// UpdateProfile writes the profile with kb.update.
func (h Hub) UpdateProfile(ctx context.Context, personID string, p *PersonProfile) (json.RawMessage, []IgnoredField, error) {
	if p == nil {
		p = &PersonProfile{}
	}
	var out struct {
		Before  map[string]json.RawMessage `json:"before"`
		Ignored []IgnoredField             `json:"ignored"`
	}
	err := h.H.Call(ctx, "kb.update", profileUpdate{KBUpdateParams: wire.KBUpdateParams{Kind: "people", ID: personID}, Profile: p}, &out)
	if err != nil {
		return nil, nil, mapErr(err)
	}
	before := out.Before["profile"]
	if len(before) == 0 {
		before = json.RawMessage("null")
	}
	return before, out.Ignored, nil
}

// ReadProfile reads this extension's profile back with kb.get (its profiles by writer).
func (h Hub) ReadProfile(ctx context.Context, personID string) (*PersonProfile, error) {
	zero := 0
	var out struct {
		Detail struct {
			Profiles map[string]ProfileRecord `json:"profiles"`
		} `json:"detail"`
	}
	if err := h.H.Call(ctx, "kb.get", wire.KBGetParams{Kind: "people", ID: personID, RecentChars: &zero}, &out); err != nil {
		return nil, mapErr(err)
	}
	r, ok := out.Detail.Profiles[ProfileWriterOf(h.Ext)]
	if !ok {
		return nil, nil
	}
	return &r.Profile, nil
}

// ProfileWriterOf is how the hub names an extension's profile ("ext:<id>").
func ProfileWriterOf(ext string) string {
	if ext == "" {
		ext = "enkeep-memory"
	}
	return "ext:" + ext
}

// DryRun records a profile write instead of making it; reading back gives what it recorded.
func (d *DryRun) UpdateProfile(ctx context.Context, personID string, p *PersonProfile) (json.RawMessage, []IgnoredField, error) {
	before, _ := d.ReadProfile(ctx, personID)
	if d.Profiles == nil {
		d.Profiles = map[string]*PersonProfile{}
	}
	cp := clonePerson(p)
	d.Profiles[personID] = cp
	d.ProfileWrites = append(d.ProfileWrites, ProfileWrite{Person: personID, Profile: cp})
	return marshalOrNull(before), nil, nil
}

// ReadProfile is the profile recorded last, else the underlying source's.
func (d *DryRun) ReadProfile(ctx context.Context, personID string) (*PersonProfile, error) {
	if p, ok := d.Profiles[personID]; ok {
		if p.Empty() {
			return nil, nil
		}
		return clonePerson(p), nil
	}
	if w := ProfilesOf(d.Source); w != nil {
		return w.ReadProfile(ctx, personID)
	}
	return nil, nil
}

// ProfileWrite is one profile write a dry run recorded.
type ProfileWrite struct {
	Person  string         `json:"person"`
	Profile *PersonProfile `json:"profile"`
}

func clonePerson(p *PersonProfile) *PersonProfile {
	if p == nil {
		return &PersonProfile{}
	}
	var out PersonProfile
	b, _ := json.Marshal(p)
	_ = json.Unmarshal(b, &out)
	return &out
}

func marshalOrNull(p *PersonProfile) json.RawMessage {
	if p == nil || p.Empty() {
		return json.RawMessage("null")
	}
	b, _ := json.Marshal(p)
	return b
}

// ---- the hub's rules ------------------------------------------------------------------------------------------

// CheckProfile is why the hub would refuse p (nil when it takes it), wrapping ErrInvalidProfile: the same rules as
// the hub's checkProfile.
func CheckProfile(p *PersonProfile) error {
	if p == nil {
		return nil
	}
	item := func(part string, i int, text, basis string, sources []string, needBasis bool) error {
		where := fmt.Sprintf("profile.%s[%d]", part, i)
		switch n := utf8.RuneCountInString(strings.TrimSpace(text)); {
		case n == 0:
			return fmt.Errorf("%w: %s: text is empty", ErrInvalidProfile, where)
		case n > ProfileTextMost:
			return fmt.Errorf("%w: %s: text is %d characters, at most %d", ErrInvalidProfile, where, n, ProfileTextMost)
		}
		switch n := utf8.RuneCountInString(strings.TrimSpace(basis)); {
		case needBasis && n == 0:
			return fmt.Errorf("%w: %s: an inference needs its basis", ErrInvalidProfile, where)
		case !needBasis && n > 0:
			return fmt.Errorf("%w: %s: basis is for inferences", ErrInvalidProfile, where)
		case n > ProfileBasisMost:
			return fmt.Errorf("%w: %s: basis is %d characters, at most %d", ErrInvalidProfile, where, n, ProfileBasisMost)
		}
		if len(sources) == 0 || len(sources) > ProfileSourcesMost {
			return fmt.Errorf("%w: %s: 1 to %d sources", ErrInvalidProfile, where, ProfileSourcesMost)
		}
		for _, s := range sources {
			if n := utf8.RuneCountInString(strings.TrimSpace(s)); n == 0 || n > ProfileSourceMost {
				return fmt.Errorf("%w: %s: a source is empty or longer than %d characters", ErrInvalidProfile, where, ProfileSourceMost)
			}
		}
		return nil
	}
	parts := []struct {
		name string
		list []ProfileItem
	}{{"facts", p.Facts}, {"views", p.Views}, {"inferences", p.Inferences}}
	for _, pt := range parts {
		if len(pt.list) > ProfileItemsMost {
			return fmt.Errorf("%w: profile.%s: %d items, at most %d", ErrInvalidProfile, pt.name, len(pt.list), ProfileItemsMost)
		}
		for i, it := range pt.list {
			if err := item(pt.name, i, it.Text, it.Basis, it.Sources, pt.name == "inferences"); err != nil {
				return err
			}
		}
	}
	if len(p.Commitments) > ProfileItemsMost {
		return fmt.Errorf("%w: profile.commitments: %d items, at most %d", ErrInvalidProfile, len(p.Commitments), ProfileItemsMost)
	}
	for i, c := range p.Commitments {
		if c.Owes != OwesUser && c.Owes != OwesThem {
			return fmt.Errorf("%w: profile.commitments[%d]: owes is user or them", ErrInvalidProfile, i)
		}
		if c.Due != "" {
			if _, err := time.Parse("2006-01-02", c.Due); err != nil {
				return fmt.Errorf("%w: profile.commitments[%d]: due is YYYY-MM-DD", ErrInvalidProfile, i)
			}
		}
		if err := item("commitments", i, c.Text, "", c.Sources, false); err != nil {
			return err
		}
	}
	if n := ProfileBytes(p); n > ProfileBytesMost {
		return fmt.Errorf("%w: profile: %d bytes as shown, at most %d", ErrInvalidProfile, n, ProfileBytesMost)
	}
	return nil
}

// ProfileBytes is the size of a profile as the hub's summary.md shows it (renderProfile without the heading): the
// measure of its 8000-byte bound.
func ProfileBytes(p *PersonProfile) int {
	if p == nil {
		return 0
	}
	var b strings.Builder
	src := func(sources []string) string { return "（出处：" + strings.Join(sources, "；") + "）" }
	if len(p.Facts) > 0 {
		b.WriteString("### 事实\n")
		for _, it := range p.Facts {
			fmt.Fprintf(&b, "- [事实] %s%s\n", it.Text, src(it.Sources))
		}
	}
	if len(p.Views) > 0 {
		b.WriteString("### 用户说过的评价（原话）\n")
		for _, it := range p.Views {
			fmt.Fprintf(&b, "- [用户原话] “%s”%s\n", it.Text, src(it.Sources))
		}
	}
	if len(p.Inferences) > 0 {
		b.WriteString("### 推断（仅供参考，未经确认）\n")
		for _, it := range p.Inferences {
			fmt.Fprintf(&b, "- [推断·仅供参考] %s（依据：%s）%s\n", it.Text, it.Basis, src(it.Sources))
		}
	}
	if len(p.Commitments) > 0 {
		b.WriteString("### 承诺和待办\n")
		for _, c := range p.Commitments {
			who := "对方答应用户"
			if c.Owes == OwesUser {
				who = "用户答应对方"
			}
			extra := ""
			if c.Due != "" {
				extra += "，截止 " + c.Due
			}
			if c.Done {
				extra += "，已完成"
			}
			fmt.Fprintf(&b, "- [承诺·%s%s] %s%s\n", who, extra, c.Text, src(c.Sources))
		}
	}
	return b.Len()
}

// ---- fitting a profile within the bounds ----------------------------------------------------------------------

// Profile parts.
const (
	PartFacts       = "facts"
	PartViews       = "views"
	PartInferences  = "inferences"
	PartCommitments = "commitments"
)

// ProfileCandidate is one item that may go into a profile, with how long it is kept when the profile is over a
// bound: the lowest Keep is dropped first.
type ProfileCandidate struct {
	Part       string
	Item       ProfileItem       // facts, views, inferences
	Commitment ProfileCommitment // commitments
	Keep       int
}

// FitStats is what fitting did.
type FitStats struct {
	Shortened int // texts or bases cut to fit
	Dropped   int // items left out: over 30 in a part or over 8000 bytes
	NoSource  int // items left out for having no usable source
}

// FitProfile makes a profile the hub takes from candidates: texts and bases shortened first (to what the hub
// keeps), sources cleaned (1 to 5; one too long to keep whole is not a source), then items dropped, the lowest Keep
// first (ties: the later one), until every part has at most 30 and the whole fits 8000 bytes. Items keep their
// order within a part.
func FitProfile(cands []ProfileCandidate) (PersonProfile, FitStats) {
	var st FitStats
	type live struct {
		c   ProfileCandidate
		idx int
	}
	var ls []live
	for i, c := range cands {
		text, basis := &c.Item.Text, &c.Item.Basis
		srcs := &c.Item.Sources
		if c.Part == PartCommitments {
			text, srcs, basis = &c.Commitment.Text, &c.Commitment.Sources, nil
		}
		if t, cut := shorten(*text); t == "" {
			st.Dropped++
			continue
		} else {
			*text = t
			if cut {
				st.Shortened++
			}
		}
		if basis != nil {
			b, cut := shorten(*basis)
			if c.Part == PartInferences && b == "" {
				st.Dropped++
				continue
			}
			if c.Part != PartInferences {
				b = ""
			}
			*basis = b
			if cut {
				st.Shortened++
			}
		}
		*srcs = cleanSources(*srcs)
		if len(*srcs) == 0 {
			st.NoSource++
			continue
		}
		if c.Part == PartCommitments {
			if c.Commitment.Owes != OwesUser && c.Commitment.Owes != OwesThem {
				st.Dropped++
				continue
			}
			if c.Commitment.Due != "" {
				if _, err := time.Parse("2006-01-02", c.Commitment.Due); err != nil {
					c.Commitment.Due = ""
				}
			}
		}
		ls = append(ls, live{c, i})
	}
	build := func() PersonProfile {
		var p PersonProfile
		for _, l := range ls {
			switch l.c.Part {
			case PartFacts:
				p.Facts = append(p.Facts, l.c.Item)
			case PartViews:
				p.Views = append(p.Views, l.c.Item)
			case PartInferences:
				p.Inferences = append(p.Inferences, l.c.Item)
			case PartCommitments:
				p.Commitments = append(p.Commitments, l.c.Commitment)
			}
		}
		return p
	}
	// dropOrder: the lowest Keep first, the later one first among equals.
	dropOrder := func(sel func(live) bool) []int {
		var is []int
		for i, l := range ls {
			if sel(l) {
				is = append(is, i)
			}
		}
		sort.SliceStable(is, func(a, b int) bool {
			if ls[is[a]].c.Keep != ls[is[b]].c.Keep {
				return ls[is[a]].c.Keep < ls[is[b]].c.Keep
			}
			return ls[is[a]].idx > ls[is[b]].idx
		})
		return is
	}
	remove := func(drop map[int]bool) {
		out := ls[:0]
		for i, l := range ls {
			if !drop[i] {
				out = append(out, l)
			}
		}
		ls = out
	}
	for _, part := range []string{PartFacts, PartViews, PartInferences, PartCommitments} {
		is := dropOrder(func(l live) bool { return l.c.Part == part })
		if over := len(is) - ProfileItemsMost; over > 0 {
			drop := map[int]bool{}
			for _, i := range is[:over] {
				drop[i] = true
			}
			remove(drop)
			st.Dropped += over
		}
	}
	p := build()
	for ProfileBytes(&p) > ProfileBytesMost && len(ls) > 0 {
		is := dropOrder(func(live) bool { return true })
		remove(map[int]bool{is[0]: true})
		st.Dropped++
		p = build()
	}
	return p, st
}

// shorten puts a text on one line and cuts it to what the hub keeps, with an ellipsis when cut.
func shorten(s string) (string, bool) {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= ProfileStoredMost {
		return s, false
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:ProfileStoredMost-1])) + "…", true
}

// cleanSources are the sources a profile item can carry: on one line, distinct, none longer than the hub keeps
// whole (a cut path or URL points nowhere), at most five in the order given.
func cleanSources(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.Join(strings.Fields(s), " ")
		if s == "" || seen[s] || utf8.RuneCountInString(s) > ProfileStoredMost {
			continue
		}
		seen[s] = true
		out = append(out, s)
		if len(out) == ProfileSourcesMost {
			break
		}
	}
	return out
}

// ProfileProber tells whether the hub takes person profiles without writing anything to a person.
type ProfileProber interface {
	ProbeProfile(ctx context.Context, personID string) (bool, error)
}

// ProberOf is the profile prober behind a source (as ProfilesOf), nil when there is none.
func ProberOf(src Source) ProfileProber {
	w := ProfilesOf(src)
	if p, ok := w.(ProfileProber); ok {
		return p
	}
	return nil
}

// probeProfile is a profile the hub's rules refuse (an inference needs its basis): a hub with profiles answers it
// with invalid_params before writing anything; an older hub ignores the field.
var probeProfile = &PersonProfile{Inferences: []ProfileItem{{Text: "probe", Sources: []string{"user"}}}}

// ProbeProfile asks the hub whether it takes person profiles. A refusal naming the profile means yes (and nothing
// was written); an answer that lists the profile as ignored, or no refusal at all, means an older hub that drops
// it: no. Any other error is returned as it is (the person gone, no grant): unknown.
func (h Hub) ProbeProfile(ctx context.Context, personID string) (bool, error) {
	var out struct {
		Ignored []IgnoredField `json:"ignored"`
	}
	err := h.H.Call(ctx, "kb.update", profileUpdate{KBUpdateParams: wire.KBUpdateParams{Kind: "people", ID: personID}, Profile: probeProfile}, &out)
	if err != nil {
		if profileRefused(err) {
			return true, nil
		}
		var e *ext.Error
		if errors.As(err, &e) && strings.Contains(strings.ToLower(e.Message), "unknown field") {
			return false, nil // a hub before #5934 decodes strictly and refuses the field before touching anything
		}
		return false, mapErr(err)
	}
	return false, nil
}

// ProbeProfile on the fixture: a hub with profiles unless NoProfiles plays an older one.
func (f *Fixture) ProbeProfile(context.Context, string) (bool, error) { return !f.NoProfiles, nil }

// ProbeProfile on a dry run asks the source it wraps (a probe writes nothing).
func (d *DryRun) ProbeProfile(ctx context.Context, personID string) (bool, error) {
	if p := ProberOf(d.Source); p != nil {
		return p.ProbeProfile(ctx, personID)
	}
	return false, nil
}

// profileRefused says whether an error is a hub with profiles refusing the probe by its rules ("invalid profile: …
// an inference needs its basis"), not an older hub refusing an unknown field.
func profileRefused(err error) bool {
	var e *ext.Error
	if !errors.As(err, &e) {
		return false
	}
	m := strings.ToLower(e.Message)
	return !strings.Contains(m, "unknown field") && (strings.Contains(m, "invalid profile") || strings.Contains(m, "basis"))
}

// ProbeParams is the whole request a probe sends: the person it is addressed to and the profile, nothing else.
func ProbeParams(personID string) json.RawMessage {
	b, _ := json.Marshal(profileUpdate{KBUpdateParams: wire.KBUpdateParams{Kind: "people", ID: personID}, Profile: probeProfile})
	return b
}
