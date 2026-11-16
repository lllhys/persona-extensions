package templates

import (
	"encoding/json"
	"io/fs"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
)

var testNow = time.Date(2026, time.November, 10, 9, 30, 0, 0, time.UTC)

// applied creates a base holding the template with its samples and returns the base, its only space and its world.
func applied(t *testing.T, key string, samples bool) (*kernel.Base, *kernel.Space, *kernel.World) {
	t.Helper()
	b := &kernel.Base{Format: 1}
	sp, err := Apply(b, key, "", samples, testNow)
	if err != nil {
		t.Fatalf("Apply(%q, samples=%v): %v", key, samples, err)
	}
	return b, sp, kernel.Open(sp)
}

// entityTitled returns the entity with the title, failing the test when there is none.
func entityTitled(t *testing.T, sp *kernel.Space, title string) *kernel.Entity {
	t.Helper()
	for _, e := range sp.Entities {
		if e.Title == title {
			return e
		}
	}
	t.Fatalf("no entity titled %q", title)
	return nil
}

// number is the float value of a field, failing the test when the field is not a number.
func number(t *testing.T, w *kernel.World, e *kernel.Entity, key string) float64 {
	t.Helper()
	f, ok := w.Value(e, key).(float64)
	if !ok {
		t.Fatalf("%s.%s = %#v, want a number", e.Title, key, w.Value(e, key))
	}
	return f
}

func TestTemplatesInOrder(t *testing.T) {
	var keys []string
	for _, tpl := range All() {
		keys = append(keys, tpl.Key)
	}
	want := []string{"project", "reading", "inventory", "travel", "habit", "people", "subscription", "blank"}
	if !slices.Equal(keys, want) {
		t.Fatalf("All() keys = %v, want %v", keys, want)
	}
	for _, key := range keys {
		tpl, ok := Get(key)
		if !ok || tpl.Key != key {
			t.Errorf("Get(%q) = %q, %v", key, tpl.Key, ok)
		}
		if tpl.Name == "" || tpl.Icon == "" || tpl.About == "" {
			t.Errorf("template %q has an empty name, icon or about", key)
		}
	}
	if _, ok := Get("nope"); ok {
		t.Error("Get of an unknown key found a template")
	}
}

func TestAllReturnsACopy(t *testing.T) {
	ts := All()
	ts[0] = Template{}
	if All()[0].Key != "project" {
		t.Error("All shares its slice with the caller")
	}
}

func TestEveryTemplateAppliesWithAndWithoutSamples(t *testing.T) {
	for _, tpl := range All() {
		for _, samples := range []bool{false, true} {
			b, sp, _ := applied(t, tpl.Key, samples)
			if err := b.Check(); err != nil {
				t.Errorf("%s samples=%v: Check: %v", tpl.Key, samples, err)
			}
			if sp.Name != tpl.Name || sp.Icon != tpl.Icon || sp.About != tpl.About {
				t.Errorf("%s: space = %q %q %q, want the template's name, icon and about", tpl.Key, sp.Name, sp.Icon, sp.About)
			}
			if len(sp.Types) != len(tpl.Types) {
				t.Errorf("%s: %d types, want %d", tpl.Key, len(sp.Types), len(tpl.Types))
			}
			want := 0
			if samples {
				want = len(tpl.Samples)
			}
			if len(sp.Entities) != want {
				t.Errorf("%s samples=%v: %d entities, want %d", tpl.Key, samples, len(sp.Entities), want)
			}
		}
	}
}

func TestUnionUsesEveryFeature(t *testing.T) {
	kinds := map[string]bool{}
	views := map[string]bool{}
	var viaProp, viaBacklink, derived bool
	for _, tpl := range All() {
		for _, typ := range tpl.Types {
			for _, p := range typ.Props {
				kinds[p.Kind] = true
				if p.Kind == kernel.KindRollup {
					if strings.Contains(p.Via, ".") {
						viaBacklink = true
					} else {
						viaProp = true
					}
				}
			}
			for _, v := range typ.Views {
				views[v.Kind] = true
			}
		}
		for _, s := range tpl.Samples {
			if s.From != "" {
				derived = true
			}
		}
	}
	for _, k := range kernel.Kinds {
		if !kinds[k.Name] {
			t.Errorf("no template uses the property kind %q", k.Name)
		}
	}
	for _, v := range []string{"list", "board", "table", "progress", "feed"} {
		if !views[v] {
			t.Errorf("no template uses the view kind %q", v)
		}
	}
	if !viaProp {
		t.Error("no rollup counts its own relation (Via without a dot)")
	}
	if !viaBacklink {
		t.Error("no rollup counts the backlinks of a relation (Via with a dot)")
	}
	if !derived {
		t.Error("no sample is derived")
	}
}

func TestRollupValues(t *testing.T) {
	cases := []struct {
		key, title, prop string
		want             float64
	}{
		{"project", "连续写满 12 篇周复盘", "progress", 0.5},
		{"project", "连续写满 12 篇周复盘", "tasks", 4},
		{"project", "连续写满 12 篇周复盘", "effort", 7},
		{"project", "完成一次半程跑", "progress", 0},
		{"project", "完成一次半程跑", "tasks", 2},
		{"reading", "怎样读才留得住", "sources", 3},
		{"reading", "《慢读的方法》", "notes", 2},
		{"reading", "《阳台种菜手记》", "notes", 1},
		{"inventory", "周末露营", "ready", 0.75},
		{"inventory", "储物柜", "items", 4},
		{"inventory", "储物柜", "worth", 209},
		{"inventory", "书桌", "worth", 129},
		{"travel", "周末去杭州", "stops", 5},
		{"travel", "周末去杭州", "spent", 1370},
		{"travel", "周末去杭州", "bags", 4},
		{"travel", "周末去杭州", "packed", 2},
		{"travel", "古镇两日", "spent", 120},
		{"travel", "古镇两日", "packed", 1},
		{"travel", "去海边看一次日出", "stops", 0},
		{"habit", "晨跑", "checkins", 3},
		{"habit", "晨跑", "minutes", 85},
		{"habit", "睡前读书", "checkins", 3},
		{"habit", "睡前读书", "minutes", 80},
		{"habit", "冥想十分钟", "checkins", 1},
		{"people", "阿青", "talks", 2},
		{"people", "老周", "talks", 2},
		{"people", "表姐", "talks", 2},
		{"people", "小何", "talks", 1},
		{"subscription", "视频会员", "paid", 50},
		{"subscription", "视频会员", "payments", 2},
		{"subscription", "健身房月卡", "paid", 398},
		{"subscription", "云盘 200G", "paid", 89},
		{"subscription", "音乐会员", "paid", 0},
	}
	for _, c := range cases {
		_, sp, w := applied(t, c.key, true)
		e := entityTitled(t, sp, c.title)
		if got := number(t, w, e, c.prop); got != c.want {
			t.Errorf("%s %q.%s = %v, want %v", c.key, c.title, c.prop, got, c.want)
		}
	}
}

func TestSampleValuesAndProvenance(t *testing.T) {
	_, sp, w := applied(t, "project", true)
	idea := entityTitled(t, sp, "每周写一篇复盘")
	goal := entityTitled(t, sp, "连续写满 12 篇周复盘")
	if goal.Origin != idea.ID {
		t.Errorf("derived goal origin = %q, want the idea %q", goal.Origin, idea.ID)
	}
	if goal.Values["summary"] != idea.Values["summary"] || goal.Values["area"] != "学习" {
		t.Errorf("derived goal did not inherit summary and area: %v", goal.Values)
	}
	if goal.Status != "进行中" {
		t.Errorf("derived goal status = %q, want 进行中", goal.Status)
	}
	if got := w.Value(goal, "due"); got != "2027-01-09" {
		t.Errorf("due +60d = %v, want 2027-01-09", got)
	}
	if got := w.Value(entityTitled(t, sp, "完成一次半程跑"), "due"); got != "2027-03-10" {
		t.Errorf("due +120d = %v, want 2027-03-10", got)
	}
	if got := w.Value(entityTitled(t, sp, "写第 2 篇"), "due"); got != "2026-11-17" {
		t.Errorf("due +7d = %v, want 2026-11-17", got)
	}
	friend := entityTitled(t, sp, "约一位朋友互相督促")
	blocker := entityTitled(t, sp, "写第 2 篇")
	if got, _ := w.Value(friend, "blocked_by").([]string); !slices.Equal(got, []string{blocker.ID}) {
		t.Errorf("blocked_by = %v, want [%s]", got, blocker.ID)
	}
	if last := sp.Entities[len(sp.Entities)-1]; !last.Updated.Equal(testNow) || !last.Created.Equal(testNow) {
		t.Errorf("last sample stamped %v, want %v", last.Updated, testNow)
	}
	if first := sp.Entities[0]; !first.Created.Equal(testNow.Add(-time.Duration(len(sp.Entities)-1) * time.Second)) {
		t.Errorf("first sample stamped %v", first.Created)
	}

	_, sp, w = applied(t, "reading", true)
	clip := entityTitled(t, sp, "间隔复习的做法")
	note := entityTitled(t, sp, "间隔复习的要点")
	if note.Origin != clip.ID || note.Values["gist"] != clip.Values["gist"] {
		t.Errorf("derived note: origin %q gist %v, want clip %q and its gist", note.Origin, note.Values["gist"], clip.ID)
	}
	if got := w.Value(entityTitled(t, sp, "《阳台种菜手记》"), "finished"); got != "2026-10-21" {
		t.Errorf("finished -20d = %v, want 2026-10-21", got)
	}

	_, sp, w = applied(t, "inventory", true)
	var wish, chair *kernel.Entity
	for _, e := range sp.Entities {
		switch {
		case e.Type == "wish" && e.Title == "折叠椅":
			wish = e
		case e.Type == "item" && e.Title == "折叠椅":
			chair = e
		}
	}
	if wish == nil || chair == nil || chair.Origin != wish.ID {
		t.Fatalf("derived item = %v, want an item derived from the wish %q", chair, wish.ID)
	}
	if chair.Status != "充足" || chair.Values["category"] != "工具" || chair.Values["price"] != 150.0 {
		t.Errorf("derived item status %q values %v, want 充足 with the wish's category and price", chair.Status, chair.Values)
	}
	if got := w.Value(entityTitled(t, sp, "大米"), "expires"); got != "2027-05-29" {
		t.Errorf("expires +200d = %v, want 2027-05-29", got)
	}
}

// derivedFrom returns the entity titled title and checks that it was derived from the entity titled src.
func derivedFrom(t *testing.T, sp *kernel.Space, title, src string) *kernel.Entity {
	t.Helper()
	e, s := entityTitled(t, sp, title), entityTitled(t, sp, src)
	if e.Origin != s.ID {
		t.Errorf("%q origin = %q, want %q (%s)", title, e.Origin, s.ID, src)
	}
	return e
}

// ids are the ids of the entities with the titles, in order.
func ids(t *testing.T, sp *kernel.Space, titles ...string) []string {
	t.Helper()
	out := make([]string, 0, len(titles))
	for _, title := range titles {
		out = append(out, entityTitled(t, sp, title).ID)
	}
	return out
}

// viewNamed runs the view of the type with the name over the world, failing the test when it is missing or fails.
func viewNamed(t *testing.T, sp *kernel.Space, w *kernel.World, typ, name string) kernel.Result {
	t.Helper()
	for _, ty := range sp.Types {
		if ty.Key != typ {
			continue
		}
		for _, v := range ty.Views {
			if v.Name == name {
				res, err := w.Run(kernel.Query{Type: typ, Lens: v.Lens, Limit: kernel.MaxLimit})
				if err != nil {
					t.Fatalf("view %s/%s: %v", typ, name, err)
				}
				return res
			}
		}
	}
	t.Fatalf("no view %s/%s", typ, name)
	return kernel.Result{}
}

// aggValues are the values of the aggregates of a result; an undefined one is -1.
func aggValues(res kernel.Result) []float64 {
	out := make([]float64, 0, len(res.Aggs))
	for _, c := range res.Aggs {
		if c.Value == nil {
			out = append(out, -1)
			continue
		}
		out = append(out, *c.Value)
	}
	return out
}

// titles are the titles of the entities of a result, in order.
func titles(res kernel.Result) []string {
	var out []string
	for _, g := range res.Groups {
		for _, e := range g.Items {
			out = append(out, e.Title)
		}
	}
	return out
}

// sortedTitles are the titles of a result in sorted order, for a view whose order is not the point.
func sortedTitles(res kernel.Result) []string {
	out := titles(res)
	slices.Sort(out)
	return out
}

func TestTravelSamples(t *testing.T) {
	_, sp, w := applied(t, "travel", true)
	hz := entityTitled(t, sp, "周末去杭州")
	if got := w.Value(hz, "start"); got != "2026-11-20" {
		t.Errorf("start +10d = %v, want 2026-11-20", got)
	}
	if got := w.Value(entityTitled(t, sp, "古镇两日"), "end"); got != "2026-10-02" {
		t.Errorf("end -39d = %v, want 2026-10-02", got)
	}
	if r, ok := w.Ratio(hz); !ok || r != 0.5 {
		t.Errorf("packing progress = %v, %v; want 0.5", r, ok)
	}
	cable := derivedFrom(t, sp, "充电宝和数据线", "充电宝")
	if got, _ := w.Value(cable, "trip").([]string); !slices.Equal(got, []string{hz.ID}) || cable.Values["kind"] != "电子" ||
		cable.Values["packed"] != false {
		t.Errorf("derived packing item values %v, want the trip replaced, the kind kept and not packed", cable.Values)
	}
	if got := aggValues(viewNamed(t, sp, w, "plan", "花费")); !slices.Equal(got, []float64{6, 1490}) {
		t.Errorf("itinerary cost table aggregates = %v, want [6 1490]", got)
	}
	if got := titles(viewNamed(t, sp, w, "trip", "收拾行李")); !slices.Equal(got, []string{"周末去杭州"}) {
		t.Errorf("packing progress view = %v, want the trip being planned", got)
	}
	if got := sortedTitles(viewNamed(t, sp, w, "pack", "还没装")); !slices.Equal(got, []string{"充电宝和数据线", "薄外套"}) {
		t.Errorf("not yet packed = %v", got)
	}
}

func TestHabitSamples(t *testing.T) {
	_, sp, w := applied(t, "habit", true)
	run := entityTitled(t, sp, "晨跑")
	if r, ok := w.Ratio(run); !ok || r != 0.1 {
		t.Errorf("habit progress = %v, %v; want 0.1 (3 of 30)", r, ok)
	}
	again := derivedFrom(t, sp, "又读了一章", "睡前读完第三章")
	read := entityTitled(t, sp, "睡前读书")
	if got, _ := w.Value(again, "habit").([]string); !slices.Equal(got, []string{read.ID}) || again.Values["minutes"] != 30.0 {
		t.Errorf("derived check-in values %v, want the habit and the minutes of its source", again.Values)
	}
	if got := w.Value(again, "date"); got != "2026-11-10" {
		t.Errorf("date +0d = %v, want 2026-11-10", got)
	}
	if got := aggValues(viewNamed(t, sp, w, "checkin", "表格")); !slices.Equal(got, []float64{7, 175}) {
		t.Errorf("check-in table aggregates = %v, want [7 175]", got)
	}
	if got := sortedTitles(viewNamed(t, sp, w, "habit", "进度")); !slices.Equal(got, []string{"晨跑", "睡前读书"}) {
		t.Errorf("habit progress view = %v, want the habits in progress", got)
	}
}

func TestPeopleSamples(t *testing.T) {
	_, sp, w := applied(t, "people", true)
	if got := w.Value(entityTitled(t, sp, "阿青"), "follow"); got != "2026-11-13" {
		t.Errorf("follow +3d = %v, want 2026-11-13", got)
	}
	hotpot := entityTitled(t, sp, "三个人吃火锅")
	if got, _ := w.Value(hotpot, "people").([]string); !slices.Equal(got, ids(t, sp, "阿青", "老周")) {
		t.Errorf("hotpot people = %v, want 阿青 and 老周", got)
	}
	back := derivedFrom(t, sp, "给表姐回电话", "表姐打来电话")
	if got, _ := w.Value(back, "people").([]string); !slices.Equal(got, ids(t, sp, "表姐")) || back.Values["way"] != "电话" {
		t.Errorf("derived contact values %v, want the person and the way of its source", back.Values)
	}
	if got := titles(viewNamed(t, sp, w, "person", "待联系")); !slices.Equal(got, []string{"阿青", "小何", "老周"}) {
		t.Errorf("to follow up = %v, want the people with a date, soonest first", got)
	}
	if got := aggValues(viewNamed(t, sp, w, "person", "表格")); !slices.Equal(got, []float64{4, 7}) {
		t.Errorf("people table aggregates = %v, want [4 7]", got)
	}
}

func TestSubscriptionSamples(t *testing.T) {
	_, sp, w := applied(t, "subscription", true)
	if got := w.Value(entityTitled(t, sp, "视频会员"), "renew"); got != "2026-11-16" {
		t.Errorf("renew +6d = %v, want 2026-11-16", got)
	}
	again := derivedFrom(t, sp, "视频会员 又续一个月", "视频会员 自动续费")
	if again.Values["amount"] != 25.0 || w.Value(again, "date") != "2026-10-17" {
		t.Errorf("derived payment values %v, want the amount of its source and the date -24d", again.Values)
	}
	if got := aggValues(viewNamed(t, sp, w, "sub", "月付")); !slices.Equal(got, []float64{3, 254}) {
		t.Errorf("monthly table aggregates = %v, want [3 254] (cancelled left out)", got)
	}
	if got := aggValues(viewNamed(t, sp, w, "sub", "年付")); !slices.Equal(got, []float64{1, 89}) {
		t.Errorf("yearly table aggregates = %v, want [1 89]", got)
	}
	if got := titles(viewNamed(t, sp, w, "sub", "快续费了")); !slices.Equal(got, []string{"健身房月卡", "背单词课", "视频会员", "云盘 200G"}) {
		t.Errorf("renewing soon = %v, want the live subscriptions by renewal date", got)
	}
	if got := aggValues(viewNamed(t, sp, w, "payment", "表格")); !slices.Equal(got, []float64{5, 537}) {
		t.Errorf("payment table aggregates = %v, want [5 537]", got)
	}
}

func TestEveryTemplateAppliesBesideOtherSpaces(t *testing.T) {
	b := &kernel.Base{Format: 1}
	for round := range 2 {
		for _, tpl := range All() {
			sp, err := Apply(b, tpl.Key, "", true, testNow)
			if err != nil {
				t.Fatalf("round %d: Apply(%q) beside %d spaces: %v", round, tpl.Key, len(b.Spaces)-1, err)
			}
			if len(sp.Entities) != len(tpl.Samples) {
				t.Errorf("round %d: %s: %d entities, want %d", round, tpl.Key, len(sp.Entities), len(tpl.Samples))
			}
		}
	}
	if err := b.Check(); err != nil {
		t.Fatalf("Check of a base with every template twice: %v", err)
	}
	if want := 2 * len(All()); len(b.Spaces) != want {
		t.Fatalf("%d spaces, want %d", len(b.Spaces), want)
	}
	// Every relation of a space points into that space, so a second copy of a template never links to the first.
	for _, sp := range b.Spaces {
		own := map[string]bool{}
		for _, e := range sp.Entities {
			own[e.ID] = true
		}
		w := kernel.Open(sp)
		for _, e := range sp.Entities {
			for _, l := range w.Out(e) {
				if !own[l.To] {
					t.Errorf("%s: %q links outside its space to %q", sp.Name, e.Title, l.To)
				}
			}
		}
	}
}

// TestEverySampleIsUseful checks the samples of every template as a whole: every rollup has a non-zero value
// somewhere, every view runs and shows something, every relation value names an earlier sample of its target type,
// every date value is relative or a day, and no type holds two samples with the same title.
func TestEverySampleIsUseful(t *testing.T) {
	for _, tpl := range All() {
		if len(tpl.Samples) == 0 {
			continue
		}
		_, sp, w := applied(t, tpl.Key, true)
		for _, typ := range sp.Types {
			for _, p := range typ.Props {
				if p.Kind != kernel.KindRollup {
					continue
				}
				nonzero := false
				for _, e := range sp.Entities {
					if f, ok := w.Value(e, p.Key).(float64); ok && e.Type == typ.Key && f != 0 {
						nonzero = true
					}
				}
				if !nonzero {
					t.Errorf("%s: rollup %s.%s is zero or unset on every sample", tpl.Key, typ.Key, p.Key)
				}
			}
			if len(typ.Views) == 0 {
				t.Errorf("%s: type %s declares no view", tpl.Key, typ.Key)
			}
			for _, v := range typ.Views {
				if res := viewNamed(t, sp, w, typ.Key, v.Name); res.Total == 0 {
					t.Errorf("%s: view %s/%s shows no sample", tpl.Key, typ.Key, v.Name)
				}
			}
		}
		refType := map[string]string{}
		seen := map[[2]string]bool{}
		for _, s := range tpl.Samples {
			if key := [2]string{s.Type, s.Title}; seen[key] {
				t.Errorf("%s: two samples of type %s are titled %q", tpl.Key, s.Type, s.Title)
			} else {
				seen[key] = true
			}
			i := slices.IndexFunc(tpl.Types, func(k kernel.Type) bool { return k.Key == s.Type })
			if i < 0 {
				t.Fatalf("%s: sample %q has an unknown type %q", tpl.Key, s.Title, s.Type)
			}
			for key, raw := range s.Values {
				j := slices.IndexFunc(tpl.Types[i].Props, func(p kernel.Prop) bool { return p.Key == key })
				if j < 0 {
					t.Errorf("%s: sample %q has an unknown property %q", tpl.Key, s.Title, key)
					continue
				}
				p := tpl.Types[i].Props[j]
				switch p.Kind {
				case kernel.KindRelation:
					vals, ok := raw.([]any)
					if !ok {
						vals = []any{raw}
					}
					for _, v := range vals {
						ref, _ := v.(string)
						name, isRef := strings.CutPrefix(ref, "@")
						if !isRef || refType[name] != p.Target {
							t.Errorf("%s: sample %q.%s = %v, want @ref of an earlier %s sample", tpl.Key, s.Title, key, v, p.Target)
						}
					}
				case kernel.KindDate:
					d, _ := raw.(string)
					_, rel := relativeDate(d, testNow)
					if _, err := time.Parse(time.DateOnly, d); !rel && err != nil {
						t.Errorf("%s: sample %q.%s = %v, want +Nd, -Nd or YYYY-MM-DD", tpl.Key, s.Title, key, raw)
					}
				}
			}
			refType[s.Ref] = s.Type
		}
	}
}

// TestDatedFeedsReadInSampleOrder: a feed ordered by a date of the record shows the samples newest first by that
// date, the same order their creation stamps give, so the feed tells the story in the order the bundle does.
func TestDatedFeedsReadInSampleOrder(t *testing.T) {
	for _, tpl := range All() {
		if len(tpl.Samples) == 0 {
			continue
		}
		_, sp, w := applied(t, tpl.Key, true)
		for _, typ := range sp.Types {
			for _, v := range typ.Views {
				if v.Kind != "feed" || len(v.Sort) == 0 {
					continue
				}
				by := v.Sort[0].By
				if p, ok := sp.Field(typ, by); !ok || p.Kind != kernel.KindDate {
					continue
				}
				prev := ""
				for _, e := range sp.Entities {
					if e.Type != typ.Key {
						continue
					}
					d, _ := w.Value(e, by).(string)
					if d == "" {
						t.Errorf("%s: %q has no %s, which its feed is ordered by", tpl.Key, e.Title, by)
					} else if d < prev {
						t.Errorf("%s: %q is dated %s, before the sample created ahead of it (%s)", tpl.Key, e.Title, d, prev)
					}
					prev = max(prev, d)
				}
			}
		}
	}
}

var (
	urlPattern   = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s"]*`)
	emailPattern = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+`)
)

// TestBundlesHoldNoPrivateData scans every embedded bundle: this repository is public, so sample data must be
// fictional. Links are on example.com only, an "@" only starts a relation reference to a sample of the same bundle,
// and nothing looks like an email address.
func TestBundlesHoldNoPrivateData(t *testing.T) {
	files, err := fs.Glob(bundles, "*.json")
	if err != nil || len(files) != len(order) {
		t.Fatalf("embedded bundles %v (%v), want one per template", files, err)
	}
	for _, name := range files {
		data, err := fs.ReadFile(bundles, name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, raw := range urlPattern.FindAllString(text, -1) {
			u, err := url.Parse(raw)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") ||
				(u.Hostname() != "example.com" && !strings.HasSuffix(u.Hostname(), ".example.com")) {
				t.Errorf("%s: link %q is not on example.com", name, raw)
			}
		}
		if m := emailPattern.FindAllString(text, -1); len(m) > 0 {
			t.Errorf("%s: email-like text %v", name, m)
		}
		var tpl Template
		if err := json.Unmarshal(data, &tpl); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		refs := map[string]bool{}
		for _, s := range tpl.Samples {
			refs[s.Ref] = true
		}
		var walk func(v any)
		walk = func(v any) {
			switch v := v.(type) {
			case string:
				if !strings.Contains(v, "@") {
					return
				}
				if ref, ok := strings.CutPrefix(v, "@"); !ok || !refs[ref] {
					t.Errorf("%s: %q holds an \"@\" that is not a sample reference", name, v)
				}
			case []any:
				for _, x := range v {
					walk(x)
				}
			case map[string]any:
				for k, x := range v {
					walk(k)
					walk(x)
				}
			}
		}
		var doc any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		walk(doc)
	}
}

func TestBundleScanCatchesPrivateData(t *testing.T) {
	// The patterns of the scan, on text they must and must not match.
	if got := urlPattern.FindAllString(`"u": "https://example.com/a", "v": "ftp://intranet.test/x"`, -1); len(got) != 2 {
		t.Errorf("url pattern found %v", got)
	}
	if !emailPattern.MatchString("someone@example.com") || emailPattern.MatchString(`"@trip_hz"`) {
		t.Error("email pattern does not tell an address from a sample reference")
	}
}

func TestNameDefaultsAndGiven(t *testing.T) {
	b := &kernel.Base{Format: 1}
	sp, err := Apply(b, "reading", "我的书架", false, testNow)
	if err != nil || sp.Name != "我的书架" {
		t.Fatalf("Apply with a name: %v, %q", err, sp.Name)
	}
	b = &kernel.Base{Format: 1}
	sp, err = Apply(b, "reading", "", false, testNow)
	if err != nil || sp.Name != "阅读与知识" {
		t.Fatalf("Apply without a name: %v, %q", err, sp.Name)
	}
}

func TestApplyUnknownKey(t *testing.T) {
	b := &kernel.Base{Format: 1}
	if _, err := Apply(b, "nope", "", true, testNow); err == nil {
		t.Fatal("Apply of an unknown template succeeded")
	}
	if len(b.Spaces) != 0 {
		t.Error("a failed Apply changed the base")
	}
}

func TestRelativeDate(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"+30d", "2026-12-10", true},
		{"-20d", "2026-10-21", true},
		{"+0d", "2026-11-10", true},
		{"-0d", "2026-11-10", true},
		{"+1d", "2026-11-11", true},
		{"30d", "", false},
		{"+d", "", false},
		{"+3x", "", false},
		{"+x3d", "", false},
		{"+1-2d", "", false},
		{"+99999999999999999999d", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := relativeDate(c.in, testNow)
		if got != c.want || ok != c.ok {
			t.Errorf("relativeDate(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// Custom bundles for the error paths and the two-pass rule.

var (
	noteT = kernel.Type{Key: "note", Name: "笔记", Props: []kernel.Prop{
		{Key: "gist", Name: "要点", Kind: kernel.KindText},
		{Key: "page", Name: "页码", Kind: kernel.KindNumber},
		{Key: "due", Name: "截止", Kind: kernel.KindDate},
	}}
	taskT = kernel.Type{Key: "task", Name: "任务",
		Flow: []kernel.Stage{
			{Name: "待办", Phase: kernel.Todo},
			{Name: "完成", Phase: kernel.Done},
			{Name: "取消", Phase: kernel.Dropped},
		},
		Props: []kernel.Prop{
			{Key: "gist", Name: "要点", Kind: kernel.KindText},
			{Key: "to", Name: "到", Kind: kernel.KindRelation, Target: "note", Many: true},
		},
	}
)

func custom(samples ...Sample) Template {
	return Template{Key: "custom", Name: "自定义", Icon: "list", About: "测试", Types: []kernel.Type{noteT, taskT}, Samples: samples}
}

func TestApplyFailureLeavesBaseUnchanged(t *testing.T) {
	cases := map[string]Template{
		"duplicate ref": custom(Sample{Ref: "a", Type: "note", Title: "一"}, Sample{Ref: "a", Type: "note", Title: "二"}),
		"empty ref":     custom(Sample{Type: "note", Title: "一"}),
		"unknown type":  custom(Sample{Ref: "a", Type: "nope", Title: "一"}),
		"unknown property": custom(Sample{Ref: "a", Type: "note", Title: "一",
			Values: map[string]any{"missing": "x"}}),
		"date not a date": custom(Sample{Ref: "a", Type: "note", Title: "一",
			Values: map[string]any{"due": 5.0}}),
		"bad relative date": custom(Sample{Ref: "a", Type: "note", Title: "一",
			Values: map[string]any{"due": "+xd"}}),
		"unknown ref in a relation": custom(Sample{Ref: "a", Type: "task", Title: "一",
			Values: map[string]any{"to": "@missing"}}),
		"unknown ref in a relation list": custom(Sample{Ref: "a", Type: "task", Title: "一",
			Values: map[string]any{"to": []any{"@missing"}}}),
		"derive from a sample not yet created": custom(Sample{Ref: "a", Type: "task", From: "later", Title: "一"}),
		"derive into an unknown type":          custom(Sample{Ref: "a", Type: "note", Title: "一"}, Sample{Ref: "b", Type: "nope", From: "a", Title: "二"}),
		"derive to an unreachable stage":       custom(Sample{Ref: "a", Type: "note", Title: "一"}, Sample{Ref: "b", Type: "task", From: "a", Status: "不存在", Title: "二"}),
		"non-string relation item": {Key: "custom", Name: "自定义", Types: []kernel.Type{
			{Key: "a", Name: "甲", Props: []kernel.Prop{{Key: "link", Name: "链接", Kind: kernel.KindRelation, Target: "a"}}},
		}, Samples: []Sample{{Ref: "x", Type: "a", Title: "一", Values: map[string]any{"link": []any{5.0}}}}},
		"relation not a list": {Key: "custom", Name: "自定义", Types: []kernel.Type{
			{Key: "a", Name: "甲", Props: []kernel.Prop{{Key: "link", Name: "链接", Kind: kernel.KindRelation, Target: "a"}}},
		}, Samples: []Sample{{Ref: "x", Type: "a", Title: "一", Values: map[string]any{"link": 5.0}}}},
		"bad type key":  {Key: "custom", Name: "自定义", Types: []kernel.Type{{Key: "BAD", Name: "坏"}}},
		"name too long": {Key: "custom", Name: "自定义", Types: []kernel.Type{noteT}, Samples: nil},
	}
	for name, tpl := range cases {
		b := &kernel.Base{Format: 1}
		before, _ := json.Marshal(b)
		var err error
		if name == "name too long" {
			long := strings.Repeat("长", 41)
			_, err = apply(b, tpl, long, true, testNow)
		} else {
			_, err = apply(b, tpl, "", true, testNow)
		}
		if err == nil {
			t.Errorf("%s: apply succeeded, want an error", name)
			continue
		}
		after, _ := json.Marshal(b)
		if string(before) != string(after) {
			t.Errorf("%s: a failed apply changed the base", name)
		}
	}
}

func TestDerivedStatusWalksThroughTheFlow(t *testing.T) {
	// A note derived into a task starts at the first stage; 完成 is one move away.
	tpl := custom(
		Sample{Ref: "n", Type: "note", Title: "笔记", Values: map[string]any{"gist": "要点"}},
		Sample{Ref: "t", Type: "task", From: "n", Status: "完成", Title: "任务"},
	)
	b := &kernel.Base{Format: 1}
	sp, err := apply(b, tpl, "", true, testNow)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	task := entityTitled(t, sp, "任务")
	if task.Status != "完成" || task.Values["gist"] != "要点" {
		t.Errorf("derived task status %q values %v", task.Status, task.Values)
	}
}

func TestDerivedValuesAreApplied(t *testing.T) {
	// The derived sample's own values are applied after the derivation: a date is resolved, and a text value that
	// looks like a relative date stays text.
	tpl := custom(
		Sample{Ref: "n", Type: "note", Title: "笔记"},
		Sample{Ref: "m", Type: "note", From: "n", Title: "副本", Values: map[string]any{"gist": "+5d", "due": "+1d"}},
	)
	b := &kernel.Base{Format: 1}
	sp, err := apply(b, tpl, "", true, testNow)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	dup := entityTitled(t, sp, "副本")
	if dup.Values["gist"] != "+5d" || dup.Values["due"] != "2026-11-11" {
		t.Errorf("derived values %v", dup.Values)
	}
}

func TestRelationReferencesAreResolved(t *testing.T) {
	tpl := custom(
		Sample{Ref: "a", Type: "note", Title: "一"},
		Sample{Ref: "b", Type: "task", Title: "二", Values: map[string]any{"to": "@a", "gist": "+5d"}},
		Sample{Ref: "c", Type: "task", Title: "三", Values: map[string]any{"to": []any{"@a"}}},
	)
	b := &kernel.Base{Format: 1}
	sp, err := apply(b, tpl, "", true, testNow)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	note := entityTitled(t, sp, "一")
	for _, title := range []string{"二", "三"} {
		e := entityTitled(t, sp, title)
		if got, _ := e.Values["to"].([]string); !slices.Equal(got, []string{note.ID}) {
			t.Errorf("%s: to = %v, want [%s]", title, e.Values["to"], note.ID)
		}
	}
	// a text value is never resolved as a reference or a date
	if got := entityTitled(t, sp, "二").Values["gist"]; got != "+5d" {
		t.Errorf("text value = %v, want +5d", got)
	}
}

// TestSkeletonDefersLaterRelations builds types that refer to each other: the first type points to a type declared
// after it, and the second points back.
func TestSkeletonDefersLaterRelations(t *testing.T) {
	a := kernel.Type{Key: "a", Name: "甲", Props: []kernel.Prop{
		{Key: "to_b", Name: "到乙", Kind: kernel.KindRelation, Target: "b"},
		{Key: "self", Name: "自己", Kind: kernel.KindRelation, Target: "a"},
		{Key: "n", Name: "数", Kind: kernel.KindRollup, Via: "b.to_a", Op: "count"},
	}, Card: kernel.Card{Lead: "self"}, Views: []kernel.View{{Name: "列表", Kind: "list"}}}
	b := kernel.Type{Key: "b", Name: "乙", Props: []kernel.Prop{
		{Key: "to_a", Name: "到甲", Kind: kernel.KindRelation, Target: "a", Back: "乙"},
	}}
	tpl := Template{Key: "mutual", Name: "互相", Types: []kernel.Type{a, b}}
	base := &kernel.Base{Format: 1}
	sp, err := apply(base, tpl, "", false, testNow)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(sp.Types) != 2 || len(sp.Types[0].Props) != 3 || sp.Types[0].Views == nil {
		t.Errorf("types after two passes = %+v", sp.Types)
	}

	// The skeleton of a type leaves out what the first pass cannot resolve.
	have := map[string]bool{}
	s := skeleton(a, have)
	if len(s.Props) != 1 || s.Props[0].Key != "self" || s.Card.Lead != "" || s.Views != nil {
		t.Errorf("skeleton of a with nothing added = %+v", s)
	}
	have["b"] = true
	if s := skeleton(a, have); len(s.Props) != 2 {
		t.Errorf("skeleton of a with b added keeps %d props, want 2", len(s.Props))
	}
}

func TestLoadRefusesBadBundles(t *testing.T) {
	good := `{"key":"x","name":"甲","icon":"i","about":"a","types":[]}`
	cases := map[string]fstest.MapFS{
		"missing file":  {},
		"not json":      {"x.json": {Data: []byte("{")}},
		"unknown field": {"x.json": {Data: []byte(`{"key":"x","nmae":"甲","types":[]}`)}},
		"wrong key":     {"x.json": {Data: []byte(`{"key":"y","name":"甲","types":[]}`)}},
	}
	for name, fsys := range cases {
		if _, err := load(fsys, []string{"x"}); err == nil {
			t.Errorf("%s: load succeeded", name)
		}
	}
	ts, err := load(fstest.MapFS{"x.json": {Data: []byte(good)}}, []string{"x"})
	if err != nil || len(ts) != 1 || ts[0].Name != "甲" {
		t.Fatalf("load of a good bundle = %v, %v", ts, err)
	}
}

func TestMustLoadPanicsOnBadBundle(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("mustLoad of a missing bundle did not panic")
		}
	}()
	mustLoad(fstest.MapFS{}, []string{"missing"})
}

func TestEmbeddedBundlesDecode(t *testing.T) {
	ts, err := load(bundles, order)
	if err != nil {
		t.Fatalf("embedded bundles: %v", err)
	}
	if len(ts) != len(order) {
		t.Fatalf("%d embedded bundles, want %d", len(ts), len(order))
	}
}
