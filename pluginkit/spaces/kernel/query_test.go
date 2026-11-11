package kernel

import (
	"encoding/json"
	"reflect"
	"testing"
)

// runIDs runs a query and returns the ids of the page, group by group.
func runIDs(t *testing.T, w *World, q Query) []string {
	t.Helper()
	res, err := w.Run(q)
	if err != nil {
		t.Fatalf("Run(%+v): %v", q, err)
	}
	var out []string
	for _, g := range res.Groups {
		for _, e := range g.Items {
			out = append(out, e.ID)
		}
	}
	return out
}

func TestQueryFilters(t *testing.T) {
	w, _ := sampleWorld(t)
	tests := []struct {
		name  string
		q     Query
		want  []string
		fails bool
	}{
		{"no filter lists every task in id order", Query{Type: "task"}, []string{"t1", "t2", "t3", "t4", "t5"}, false},
		{"enum eq", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "status", Op: "eq", Value: "进行"}}}}, []string{"t2"}, false},
		{"number lt excludes unset", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "effort", Op: "lt", Value: 3}}}}, []string{"t3"}, false},
		{"number ge", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "effort", Op: "ge", Value: 3.0}}}}, []string{"t1", "t2"}, false},
		{"number eq from a string", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "effort", Op: "eq", Value: "5"}}}}, []string{"t2"}, false},
		{"number eq refuses a malformed argument", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "effort", Op: "eq", Value: "abc"}}}}, nil, true},
		{"number ne matches unset", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "effort", Op: "ne", Value: 3}}}}, []string{"t2", "t3", "t4", "t5"}, false},
		{"empty true is unset", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "effort", Op: "empty"}}}}, []string{"t4", "t5"}, false},
		{"empty false is set", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "effort", Op: "empty", Value: false}}}}, []string{"t1", "t2", "t3"}, false},
		{"empty refuses a non boolean", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "effort", Op: "empty", Value: "x"}}}}, nil, true},
		{"empty with a text value parsed as boolean", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "effort", Op: "empty", Value: "false"}}}}, []string{"t1", "t2", "t3"}, false},
		{"empty with an empty string means unset", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "effort", Op: "empty", Value: ""}}}}, []string{"t4", "t5"}, false},
		{"text has is a substring", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "title", Op: "has", Value: "任"}}}}, []string{"t1", "t2", "t3", "t4", "t5"}, false},
		{"text has narrows", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "title", Op: "has", Value: "一"}}}}, []string{"t1"}, false},
		{"text ne", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "title", Op: "ne", Value: "任务一"}}}}, []string{"t2", "t3", "t4", "t5"}, false},
		{"text lt compares code points", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "title", Op: "lt", Value: "任务二"}}}}, []string{"t1", "t3"}, false},
		{"text le", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "title", Op: "le", Value: "任务一"}}}}, []string{"t1"}, false},
		{"text gt", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "title", Op: "gt", Value: "任务四"}}}}, []string{"t5"}, false},
		{"text ge", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "title", Op: "ge", Value: "任务四"}}}}, []string{"t4", "t5"}, false},
		{"phase eq", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "phase", Op: "eq", Value: "doing"}}}}, []string{"t2"}, false},
		{"status has a stage name", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "status", Op: "has", Value: "进行"}}}}, []string{"t2"}, false},
		{"enum has refuses an unknown option", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "status", Op: "has", Value: "乱"}}}}, nil, true},
		{"relation has an id", Query{Type: "goal", Lens: Lens{Where: []Cond{{Field: "picks", Op: "has", Value: "t1"}}}}, []string{"g1"}, false},
		{"relation has a missing id", Query{Type: "goal", Lens: Lens{Where: []Cond{{Field: "picks", Op: "has", Value: "zz"}}}}, nil, false},
		{"relation eq a single id", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "goal", Op: "eq", Value: "g1"}}}}, []string{"t1", "t2", "t3"}, false},
		{"relation eq unset", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "goal", Op: "eq"}}}}, []string{"t4", "t5"}, false},
		{"relation empty", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "blocked_by", Op: "empty"}}}}, []string{"t1", "t3", "t4", "t5"}, false},
		{"origin has", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "origin", Op: "has", Value: "t1"}}}}, []string{"t5"}, false},
		{"relation has needs every id", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "goal", Op: "has", Value: []any{"g1", "g2"}}}}}, nil, false},
		{"boolean eq true", Query{Type: "note", Lens: Lens{Where: []Cond{{Field: "read", Op: "eq", Value: true}}}}, []string{"n1"}, false},
		{"boolean eq false", Query{Type: "note", Lens: Lens{Where: []Cond{{Field: "read", Op: "eq", Value: "false"}}}}, nil, false},
		{"boolean refuses a number", Query{Type: "note", Lens: Lens{Where: []Cond{{Field: "read", Op: "eq", Value: 3}}}}, nil, true},
		{"url eq", Query{Type: "note", Lens: Lens{Where: []Cond{{Field: "link", Op: "eq", Value: "https://example.com/a"}}}}, []string{"n1"}, false},
		{"url has with a partial link is refused", Query{Type: "note", Lens: Lens{Where: []Cond{{Field: "link", Op: "has", Value: "example"}}}}, nil, true},
		{"url has with a full link is equality", Query{Type: "note", Lens: Lens{Where: []Cond{{Field: "link", Op: "has", Value: "https://example.com/a"}}}}, []string{"n1"}, false},
		{"url refuses a non link", Query{Type: "note", Lens: Lens{Where: []Cond{{Field: "link", Op: "eq", Value: "not a link"}}}}, nil, true},
		{"date lt", Query{Type: "goal", Lens: Lens{Where: []Cond{{Field: "due", Op: "lt", Value: "2027-01-01"}}}}, []string{"g1"}, false},
		{"date eq", Query{Type: "goal", Lens: Lens{Where: []Cond{{Field: "due", Op: "eq", Value: "2026-12-31"}}}}, []string{"g1"}, false},
		{"date eq empty matches unset", Query{Type: "goal", Lens: Lens{Where: []Cond{{Field: "due", Op: "eq", Value: ""}}}}, []string{"g2"}, false},
		{"date refuses a malformed date", Query{Type: "goal", Lens: Lens{Where: []Cond{{Field: "due", Op: "le", Value: "bad"}}}}, nil, true},
		{"enum eq", Query{Type: "goal", Lens: Lens{Where: []Cond{{Field: "area", Op: "eq", Value: "工作"}}}}, []string{"g1"}, false},
		{"enum ne", Query{Type: "goal", Lens: Lens{Where: []Cond{{Field: "area", Op: "ne", Value: "工作"}}}}, []string{"g2"}, false},
		{"enum refuses an unknown option", Query{Type: "goal", Lens: Lens{Where: []Cond{{Field: "area", Op: "eq", Value: "乱"}}}}, nil, true},
		{"rollup count eq zero", Query{Type: "goal", Lens: Lens{Where: []Cond{{Field: "tasks", Op: "eq", Value: 0}}}}, []string{"g2"}, false},
		{"rollup count ge", Query{Type: "goal", Lens: Lens{Where: []Cond{{Field: "tasks", Op: "ge", Value: 3}}}}, []string{"g1"}, false},
		{"rollup sum eq", Query{Type: "goal", Lens: Lens{Where: []Cond{{Field: "effort", Op: "eq", Value: 10}}}}, []string{"g1"}, false},
		{"rollup percent ne matches unset", Query{Type: "goal", Lens: Lens{Where: []Cond{{Field: "progress", Op: "ne", Value: 0.5}}}}, []string{"g2"}, false},
		{"rollup percent eq accepts a percentage text", Query{Type: "goal", Lens: Lens{Where: []Cond{{Field: "progress", Op: "eq", Value: "50%"}}}}, nil, true},
		{"rollup average eq", Query{Type: "goal", Lens: Lens{Where: []Cond{{Field: "picked", Op: "eq", Value: 4}}}}, []string{"g1"}, false},
		{"unknown field", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "nope", Op: "eq", Value: 1}}}}, nil, true},
		{"unknown operator", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "effort", Op: "like", Value: 1}}}}, nil, true},
		{"ordering needs a value", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "effort", Op: "lt"}}}}, nil, true},
		{"has needs a value", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "title", Op: "has"}}}}, nil, true},
		{"eq without a value means unset", Query{Type: "task", Lens: Lens{Where: []Cond{{Field: "effort", Op: "eq"}}}}, []string{"t4", "t5"}, false},
		{"unknown type", Query{Type: "nope"}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := w.Check(tt.q)
			if (err != nil) != tt.fails {
				t.Fatalf("Check = %v, want failure %v", err, tt.fails)
			}
			if tt.fails {
				if _, err := w.Run(tt.q); err == nil {
					t.Fatalf("Run accepted a query Check refused")
				}
				return
			}
			if got := runIDs(t, w, tt.q); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ids = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQueryTextSearch(t *testing.T) {
	w, _ := sampleWorld(t)
	tests := []struct {
		name string
		q    Query
		want []string
	}{
		{"title", Query{Type: "task", Text: "任务"}, []string{"t1", "t2", "t3", "t4", "t5"}},
		{"case insensitive in a text property", Query{Text: "示例"}, []string{"g1"}},
		{"a url is not searched", Query{Type: "note", Text: "EXAMPLE"}, nil},
		{"blank text is no filter", Query{Type: "note", Text: "   "}, []string{"n1"}},
		{"no match", Query{Text: "没有这个词"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runIDs(t, w, tt.q); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ids = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQueryCrossType(t *testing.T) {
	w, _ := sampleWorld(t)
	tests := []struct {
		name  string
		q     Query
		want  []string
		fails bool
	}{
		{"built-in phase across types", Query{Lens: Lens{Where: []Cond{{Field: "phase", Op: "eq", Value: "done"}}}}, []string{"t1"}, false},
		{"title across types", Query{Lens: Lens{Where: []Cond{{Field: "title", Op: "has", Value: "一"}}}}, []string{"g1", "n1", "t1"}, false},
		{"status across types skips a type without that stage", Query{Lens: Lens{Where: []Cond{{Field: "status", Op: "eq", Value: "进行"}}}}, []string{"g1", "t2"}, false},
		{"status no type accepts", Query{Lens: Lens{Where: []Cond{{Field: "status", Op: "eq", Value: "乱"}}}}, nil, true},
		{"declared property across types", Query{Lens: Lens{Where: []Cond{{Field: "effort", Op: "eq", Value: 3}}}}, nil, true},
		{"group by a declared property across types", Query{Lens: Lens{Group: "goal"}}, nil, true},
		{"sort by a declared property across types", Query{Lens: Lens{Sort: []Order{{By: "summary"}}}}, nil, true},
		{"aggregate of a declared property across types", Query{Lens: Lens{Agg: []Agg{{Op: "sum", Of: "effort"}}}}, nil, true},
		{"aggregate of a built-in that is not numeric", Query{Lens: Lens{Agg: []Agg{{Op: "sum", Of: "phase"}}}}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if (w.Check(tt.q) != nil) != tt.fails {
				t.Fatalf("Check = %v, want failure %v", w.Check(tt.q), tt.fails)
			}
			if tt.fails {
				return
			}
			if got := runIDs(t, w, tt.q); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ids = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQueryCrossTypeGroupsAndAggregates(t *testing.T) {
	w, _ := sampleWorld(t)
	res, err := w.Run(Query{Lens: Lens{Group: "type"}})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	var counts []int
	for _, g := range res.Groups {
		keys = append(keys, g.Key)
		counts = append(counts, g.Count)
	}
	if !reflect.DeepEqual(keys, []string{"goal", "task", "note"}) || !reflect.DeepEqual(counts, []int{2, 5, 1}) {
		t.Errorf("groups by type = %v %v, want the space's type order", keys, counts)
	}

	res, err = w.Run(Query{Lens: Lens{Group: "status"}})
	if err != nil {
		t.Fatal(err)
	}
	keys = nil
	counts = nil
	for _, g := range res.Groups {
		keys = append(keys, g.Key)
		counts = append(counts, g.Count)
	}
	wantKeys := []string{"规划", "进行", "待办", "完成", "取消", ""}
	wantCounts := []int{1, 2, 2, 1, 1, 1}
	if !reflect.DeepEqual(keys, wantKeys) || !reflect.DeepEqual(counts, wantCounts) {
		t.Errorf("groups by status = %v %v, want %v %v", keys, counts, wantKeys, wantCounts)
	}

	res, err = w.Run(Query{Lens: Lens{Agg: []Agg{{Op: "count"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Aggs) != 1 || res.Aggs[0].Value == nil || *res.Aggs[0].Value != 8 {
		t.Errorf("count across types = %+v, want 8", res.Aggs)
	}
}

func TestQueryGroups(t *testing.T) {
	w, _ := sampleWorld(t)
	type group struct {
		key   string
		name  string
		count int
		ids   []string
	}
	collect := func(t *testing.T, q Query) []group {
		t.Helper()
		res, err := w.Run(q)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		var out []group
		for _, g := range res.Groups {
			gr := group{key: g.Key, name: g.Name, count: g.Count}
			for _, e := range g.Items {
				gr.ids = append(gr.ids, e.ID)
			}
			out = append(out, gr)
		}
		return out
	}
	tests := []struct {
		name string
		q    Query
		want []group
	}{
		{"enum follows the enum order", Query{Type: "goal", Lens: Lens{Group: "area"}}, []group{
			{"工作", "工作", 1, []string{"g1"}},
			{"生活", "生活", 1, []string{"g2"}},
		}},
		{"stages follow the flow, unset last", Query{Type: "task", Lens: Lens{Group: "status"}}, []group{
			{"待办", "待办", 2, []string{"t4", "t5"}},
			{"进行", "进行", 1, []string{"t2"}},
			{"完成", "完成", 1, []string{"t1"}},
			{"取消", "取消", 1, []string{"t3"}},
		}},
		{"relation groups are named by the target, unset last", Query{Type: "task", Lens: Lens{Group: "goal"}}, []group{
			{"g1", "目标一", 3, []string{"t1", "t2", "t3"}},
			{"", "", 2, []string{"t4", "t5"}},
		}},
		{"a many relation puts an entity in each group", Query{Type: "goal", Lens: Lens{Group: "picks"}}, []group{
			{"t1", "任务一", 1, []string{"g1"}},
			{"t2", "任务二", 1, []string{"g1"}},
			{"", "", 1, []string{"g2"}},
		}},
		{"numbers order by value", Query{Type: "task", Lens: Lens{Group: "effort"}}, []group{
			{"2", "2 小时", 1, []string{"t3"}},
			{"3", "3 小时", 1, []string{"t1"}},
			{"5", "5 小时", 1, []string{"t2"}},
			{"", "", 2, []string{"t4", "t5"}},
		}},
		{"booleans", Query{Type: "note", Lens: Lens{Group: "read"}}, []group{
			{"true", "是", 1, []string{"n1"}},
		}},
		{"rollup values", Query{Type: "goal", Lens: Lens{Group: "progress"}}, []group{
			{"0.5", "50%", 1, []string{"g1"}},
			{"", "", 1, []string{"g2"}},
		}},
		{"no matches gives no groups", Query{Type: "task", Lens: Lens{Group: "status", Where: []Cond{{Field: "effort", Op: "gt", Value: 100}}}}, []group{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collect(t, tt.q)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("groups = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestQuerySortOrder(t *testing.T) {
	w, _ := sampleWorld(t)
	tests := []struct {
		name string
		sort []Order
		want []string
	}{
		{"default is id ascending among equal updates", nil, []string{"t1", "t2", "t3", "t4", "t5"}},
		{"ascending puts unset first", []Order{{By: "effort"}}, []string{"t4", "t5", "t3", "t1", "t2"}},
		{"descending puts unset last", []Order{{By: "effort", Desc: true}}, []string{"t2", "t1", "t3", "t4", "t5"}},
		{"sort on a built-in by text", []Order{{By: "status"}}, []string{"t3", "t1", "t4", "t5", "t2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := Query{Type: "task", Lens: Lens{Sort: tt.sort}}
			if got := runIDs(t, w, q); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ids = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQueryPaging(t *testing.T) {
	w, _ := sampleWorld(t)
	res, err := w.Run(Query{Type: "task", Limit: 2, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 5 || len(res.Groups) != 1 || res.Groups[0].Count != 5 {
		t.Fatalf("total %d, groups %+v", res.Total, res.Groups)
	}
	if ids := runIDs(t, w, Query{Type: "task", Limit: 2, Offset: 1}); !reflect.DeepEqual(ids, []string{"t2", "t3"}) {
		t.Errorf("page = %v, want t2 t3", ids)
	}
	if ids := runIDs(t, w, Query{Type: "task", Offset: 10}); len(ids) != 0 {
		t.Errorf("a page past the end = %v", ids)
	}

	// Paging the flattened groups keeps every group's count and aggregates over the whole group.
	res, err = w.Run(Query{Type: "task", Limit: 2, Lens: Lens{Group: "status", Agg: []Agg{{Op: "count"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 5 || len(res.Groups) != 4 {
		t.Fatalf("total %d, groups %d", res.Total, len(res.Groups))
	}
	first := res.Groups[0]
	if first.Key != "待办" || first.Count != 2 || len(first.Items) != 2 {
		t.Errorf("first group = %+v", first)
	}
	for _, g := range res.Groups[1:] {
		if len(g.Items) != 0 {
			t.Errorf("group %q should have no share of the page: %v", g.Key, g.Items)
		}
	}
	if g := res.Groups[1]; g.Count != 1 || len(g.Aggs) != 1 || *g.Aggs[0].Value != 1 {
		t.Errorf("paging changed a group's count or aggregates: %+v", g)
	}

	res, err = w.Run(Query{Type: "task", Limit: 3, Offset: 2, Lens: Lens{Group: "status"}})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, g := range res.Groups {
		for _, e := range g.Items {
			got = append(got, g.Key+":"+e.ID)
		}
	}
	if want := []string{"进行:t2", "完成:t1", "取消:t3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("flattened page = %v, want %v", got, want)
	}

	for _, q := range []Query{{Limit: -1}, {Limit: MaxLimit + 1}, {Offset: -1}} {
		if _, err := w.Run(q); err == nil {
			t.Errorf("Run accepted %+v", q)
		}
	}
	if res, _ := w.Run(Query{Type: "task", Limit: MaxLimit}); res.Total != 5 {
		t.Errorf("largest page: total %d", res.Total)
	}
}

func TestQueryLimitsRefused(t *testing.T) {
	w, _ := sampleWorld(t)
	many := make([]Cond, MaxWhere+1)
	for i := range many {
		many[i] = Cond{Field: "title", Op: "has", Value: "a"}
	}
	tests := []struct {
		name string
		q    Query
	}{
		{"where", Query{Type: "task", Lens: Lens{Where: many}}},
		{"sort", Query{Type: "task", Lens: Lens{Sort: make([]Order, MaxSort+1)}}},
		{"aggregates", Query{Type: "task", Lens: Lens{Agg: make([]Agg, MaxAgg+1)}}},
	}
	for _, tt := range tests {
		if err := w.Check(tt.q); err == nil {
			t.Errorf("%s: Check accepted too many", tt.name)
		}
	}
}

func TestQueryAggregates(t *testing.T) {
	w, _ := sampleWorld(t)
	// A lens keeps at most MaxAgg aggregates, so the six are asked in two queries.
	first, err := w.Run(Query{Type: "task", Lens: Lens{Agg: []Agg{
		{Op: "count"}, {Op: "sum", Of: "effort"}, {Op: "avg", Of: "effort"}, {Op: "min", Of: "effort"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := w.Run(Query{Type: "task", Lens: Lens{Agg: []Agg{
		{Op: "max", Of: "effort"}, {Op: "percent_done"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	res := Result{Aggs: append(first.Aggs, second.Aggs...)}
	want := []struct {
		name, text string
		value      float64
	}{
		{"数量", "5", 5},
		{"合计 预估", "10 小时", 10},
		{"平均 预估", "3.3333333333333335 小时", 10.0 / 3},
		{"最小 预估", "2 小时", 2},
		{"最大 预估", "5 小时", 5},
		{"完成度", "25%", 0.25},
	}
	if len(res.Aggs) != len(want) {
		t.Fatalf("%d aggregates, want %d", len(res.Aggs), len(want))
	}
	for i, c := range res.Aggs {
		if c.Name != want[i].name || c.Text != want[i].text || c.Value == nil || *c.Value != want[i].value {
			t.Errorf("aggregate %d = %+v, want %+v", i, c, want[i])
		}
	}

	// An undefined aggregate has no value and no text.
	res, err = w.Run(Query{Type: "goal", Lens: Lens{Where: []Cond{{Field: "title", Op: "eq", Value: "目标二"}},
		Agg: []Agg{{Op: "avg", Of: "progress"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if c := res.Aggs[0]; c.Value != nil || c.Text != "" {
		t.Errorf("undefined aggregate = %+v", c)
	}

	// Per group, over the whole group: the sum of effort in each status.
	res, err = w.Run(Query{Type: "task", Lens: Lens{Group: "status", Agg: []Agg{{Op: "sum", Of: "effort"}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range res.Groups {
		if len(g.Aggs) != 1 || g.Aggs[0].Value == nil {
			t.Fatalf("group %q aggregates = %+v", g.Key, g.Aggs)
		}
		if g.Key == "进行" && *g.Aggs[0].Value != 5 {
			t.Errorf("sum in 进行 = %v, want 5", *g.Aggs[0].Value)
		}
		if g.Key == "待办" && *g.Aggs[0].Value != 0 {
			t.Errorf("sum over unset values = %v, want 0", *g.Aggs[0].Value)
		}
	}

	refused := []Query{
		{Type: "task", Lens: Lens{Agg: []Agg{{Op: "median", Of: "effort"}}}},
		{Type: "task", Lens: Lens{Agg: []Agg{{Op: "sum"}}}},
		{Type: "task", Lens: Lens{Agg: []Agg{{Op: "count", Of: "effort"}}}},
		{Type: "task", Lens: Lens{Agg: []Agg{{Op: "sum", Of: "title"}}}},
		{Type: "task", Lens: Lens{Agg: []Agg{{Op: "sum", Of: "nope"}}}},
	}
	for _, q := range refused {
		if err := w.Check(q); err == nil {
			t.Errorf("Check accepted aggregate %+v", q.Agg)
		}
	}
}

// TestRollupEqualsRunAggregate: a rollup is the same aggregate asked through a query over its relation.
func TestRollupEqualsRunAggregate(t *testing.T) {
	w, _ := sampleWorld(t)
	g1 := w.Entity("g1")
	res, err := w.Run(Query{Type: "task", Lens: Lens{
		Where: []Cond{{Field: "goal", Op: "eq", Value: "g1"}},
		Agg:   []Agg{{Op: "sum", Of: "effort"}, {Op: "percent_done"}, {Op: "count"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for i, prop := range []string{"effort", "progress", "tasks"} {
		got, want := res.Aggs[i].Value, w.Value(g1, prop)
		if got == nil || want == nil || *got != want.(float64) {
			t.Errorf("%s: query %v, rollup %v", prop, got, want)
		}
	}
}

func TestQueryEmptySpace(t *testing.T) {
	w := Open(&Space{ID: "s", Name: "s"})
	res, err := w.Run(Query{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 0 || len(res.Groups) != 1 || res.Groups[0].Count != 0 {
		t.Errorf("empty ungrouped result = %+v", res)
	}
	if err := w.Check(Query{Lens: Lens{Where: []Cond{{Field: "status", Op: "eq", Value: "x"}}}}); err == nil {
		t.Errorf("a bad argument is refused even on an empty space")
	}
	if err := w.Check(Query{Lens: Lens{Where: []Cond{{Field: "title", Op: "has", Value: "x"}}}}); err != nil {
		t.Errorf("a valid query on an empty space: %v", err)
	}
}

func TestQueryUnknownKindIsRefused(t *testing.T) {
	s := &Space{ID: "s", Name: "s", Types: []*Type{{Key: "a", Name: "a", Props: []Prop{{Key: "x", Name: "x", Kind: "zzz"}}}}}
	w := Open(s)
	if err := w.Check(Query{Type: "a", Lens: Lens{Where: []Cond{{Field: "x", Op: "eq", Value: 1}}}}); err == nil {
		t.Errorf("a property of an unknown kind was accepted")
	}
}

func TestQueryHelpers(t *testing.T) {
	if len(opNames) != len(ops) {
		t.Errorf("opNames has %d entries, the operator table %d", len(opNames), len(ops))
	}
	for _, name := range opNames {
		if _, ok := ops[name]; !ok {
			t.Errorf("operator %q is not in the table", name)
		}
	}
	rel, _ := KindOf(KindRelation)
	txt, _ := KindOf(KindText)
	num, _ := KindOf(KindNumber)
	p := Prop{Kind: KindText}
	if hasOp(rel, &Prop{}, []string{"a"}, nil) {
		t.Errorf("has with no ids matched")
	}
	if !hasOp(txt, &p, "Abc", "b") {
		t.Errorf("text has is not a case-insensitive substring")
	}
	if !hasOp(num, &Prop{}, 3.0, 3.0) {
		t.Errorf("has on a number is not equality")
	}
}

func TestMembershipsAndKeys(t *testing.T) {
	if got := scalarKey(3); got != "3" {
		t.Errorf("scalarKey(int) = %q", got)
	}
	if got := scalarKey(2.5); got != "2.5" {
		t.Errorf("scalarKey(2.5) = %q", got)
	}
	if got := scalarKey(true); got != "true" {
		t.Errorf("scalarKey(true) = %q", got)
	}
	k, _ := KindOf(KindRelation)
	if got := memberships(k, nil); len(got) != 1 || got[0].key != "" {
		t.Errorf("memberships of an unset relation = %v", got)
	}
	if got := memberships(k, []string{"a", "b"}); len(got) != 2 || got[1].key != "b" {
		t.Errorf("memberships of a relation = %v", got)
	}
	n, _ := KindOf(KindNumber)
	if got := memberships(n, nil); len(got) != 1 || got[0].key != "" {
		t.Errorf("memberships of an unset number = %v", got)
	}
	if got := rank([]string{"a", "b"}, "zz"); got != 2 {
		t.Errorf("rank of a value outside the order = %d", got)
	}
	if got := appendMissing([]string{"a"}, []string{"a", "b", "b"}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("appendMissing = %v", got)
	}
}

func TestCondArgRules(t *testing.T) {
	s := &Space{ID: "s", Name: "s", Types: []*Type{{Key: "a", Name: "a", Props: []Prop{
		{Key: "t", Name: "文", Kind: KindText},
		{Key: "r", Name: "关", Kind: KindRelation, Target: "a", Many: true},
	}}}}
	tt, _ := s.typeByKey("a")
	p, k, err := bindField(s, tt, "t")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := condArg("empty", k, &p, "x"); err == nil {
		t.Errorf("empty accepted a text argument")
	}
	if arg, err := condArg("empty", k, &p, ""); err != nil || arg != true {
		t.Errorf("empty with an empty string = %v, %v", arg, err)
	}
	if _, _, err := bindField(s, tt, "nope"); err == nil {
		t.Errorf("bindField found an unknown field")
	}
	if _, err := bindCond(s, tt, Cond{Field: "t", Op: "zz"}); err == nil {
		t.Errorf("bindCond accepted an unknown operator")
	}
	rp, rk, _ := bindField(s, tt, "r")
	if _, err := condArg("has", rk, &rp, []any{"x", "y"}); err != nil {
		t.Errorf("has with a list of ids: %v", err)
	}
}

// TestQueryDecodedSeeds runs decoded queries, the way a tool would send them.
func TestQueryDecodedSeeds(t *testing.T) {
	seeds := []string{
		`{}`,
		`{"type":"task","where":[{"field":"effort","op":"lt","value":3}],"group":"status"}`,
		`{"agg":[{"op":"sum","of":"effort"}],"limit":500}`,
		`{"sort":[{"by":"title","desc":true}],"offset":-2}`,
		`{"where":[{"field":"status","op":"empty"}]}`,
	}
	w, _ := sampleWorld(t)
	for _, s := range seeds {
		var q Query
		if err := json.Unmarshal([]byte(s), &q); err != nil {
			t.Fatalf("seed %s: %v", s, err)
		}
		_, _ = w.Run(q)
	}
}

// FuzzRun: arbitrary decoded queries never panic.
func FuzzRun(f *testing.F) {
	f.Add(`{"type":"task","where":[{"field":"effort","op":"gt","value":2}],"group":"status","agg":[{"op":"avg","of":"effort"}]}`)
	f.Add(`{"where":[{"field":"title","op":"has","value":"任"}],"text":"一"}`)
	f.Add(`{"type":"goal","group":"picks","sort":[{"by":"progress","desc":true}],"limit":3,"offset":1}`)
	f.Fuzz(func(t *testing.T, data string) {
		var q Query
		if err := json.Unmarshal([]byte(data), &q); err != nil {
			return
		}
		w, _ := sampleWorld(t)
		_, _ = w.Run(q)
	})
}
