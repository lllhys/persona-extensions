package remind

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
)

// cst is a zone east of UTC, so that the local clock differs from the UTC clock in the tests.
var cst = time.FixedZone("CST", 8*3600)

// fakeAPI is an in-memory hub. It grants the scopes it was built with and can fail on demand.
type fakeAPI struct {
	scopes map[string]bool

	notices   []Notice
	notifyErr error

	creates     int    // CreateTodo calls, failed ones included
	failCreate  int    // the 1-based CreateTodo call that fails; 0 for none
	failCreates bool   // every CreateTodo fails
	todos       []Todo // created to-dos, in order
	nextID      int

	done     []string // ids marked done, in order
	failDone string   // id whose DoneTodo fails; "" for none
}

func newFake(scopes ...string) *fakeAPI {
	f := &fakeAPI{scopes: map[string]bool{}}
	for _, s := range scopes {
		f.scopes[s] = true
	}
	return f
}

func (f *fakeAPI) Can(scope string) bool { return f.scopes[scope] }

func (f *fakeAPI) Notify(_ context.Context, n Notice) error {
	if f.notifyErr != nil {
		return f.notifyErr
	}
	f.notices = append(f.notices, n)
	return nil
}

func (f *fakeAPI) CreateTodo(_ context.Context, t Todo) (string, error) {
	f.creates++
	if f.failCreates || f.creates == f.failCreate {
		return "", errors.New("hub 连不上")
	}
	f.nextID++
	f.todos = append(f.todos, t)
	return fmt.Sprintf("todo-%d", f.nextID), nil
}

func (f *fakeAPI) DoneTodo(_ context.Context, id string) error {
	if id == f.failDone {
		return errors.New("hub 连不上")
	}
	f.done = append(f.done, id)
	return nil
}

// busyHome has one item of each buyable kind, one expiring item, one item that needs nothing, and three chores,
// two of them due. All dates are relative to 2026-11-10.
func busyHome() *model.Home {
	return &model.Home{
		Items: []model.Item{
			{ID: "i-milk", Title: "纯牛奶", Category: "食材", HasQty: true, Qty: 0},
			{ID: "i-yogurt", Title: "酸奶", Category: "食材", Expires: "2026-11-08"},
			{ID: "i-egg", Title: "鸡蛋", Category: "食材", HasQty: true, Qty: 2, HasMin: true, Min: 3},
			{ID: "i-diaper", Title: "纸尿裤", Category: model.CategoryBaby, HasQty: true, Qty: 10, Daily: 2, Counted: "2026-11-10"},
			{ID: "i-soy", Title: "生抽", Category: "调味", Expires: "2026-11-12"},
			{ID: "i-rice", Title: "大米", Category: "食材", HasQty: true, Qty: 5},
		},
		Buys: []model.Buy{
			{ID: "b-egg", Title: "鸡蛋", Status: model.BuyTodo, ItemID: "i-egg"},
		},
		Chores: []model.Chore{
			{ID: "c-mop", Title: "拖地", Next: "2026-11-09", Every: 7},
			{ID: "c-trash", Title: "倒垃圾", Next: "2026-11-10", Every: 3},
			{ID: "c-water", Title: "浇花", Next: "2026-11-20", Every: 5},
		},
	}
}

// busyAlertKeys are the keys of the alerts of busyHome, in order.
var busyAlertKeys = []string{
	"item:i-yogurt",
	"item:i-milk",
	"item:i-egg",
	"item:i-diaper",
	"chore:c-mop:2026-11-09",
	"chore:c-trash:2026-11-10",
}

// manyOutHome has n items that are all used up.
func manyOutHome(n int) *model.Home {
	h := &model.Home{}
	for i := range n {
		h.Items = append(h.Items, model.Item{
			ID: fmt.Sprintf("i-%03d", i), Title: fmt.Sprintf("物品%03d", i), HasQty: true, Qty: 0,
		})
	}
	return h
}

func at(hour, minute int) time.Time {
	return time.Date(2026, 11, 10, hour, minute, 0, 0, cst)
}

func TestAlerts(t *testing.T) {
	today := model.Day("2026-11-10")
	tests := []struct {
		name string
		home *model.Home
		want []Alert
	}{
		{
			name: "nothing to do",
			home: &model.Home{Items: []model.Item{{ID: "i-rice", Title: "大米", HasQty: true, Qty: 5}}},
			want: nil,
		},
		{
			name: "expiring items and chores not yet due are not alerts",
			home: &model.Home{
				Items:  []model.Item{{ID: "i-soy", Title: "生抽", Expires: "2026-11-12"}},
				Chores: []model.Chore{{ID: "c-water", Title: "浇花", Next: "2026-11-20"}},
			},
			want: nil,
		},
		{
			name: "busy household in rules order, open purchases do not hide an alert",
			home: busyHome(),
			want: []Alert{
				{Key: "item:i-yogurt", Title: "补货：酸奶（已过期）"},
				{Key: "item:i-milk", Title: "补货：纯牛奶（已用完）"},
				{Key: "item:i-egg", Title: "补货：鸡蛋（低库存）"},
				{Key: "item:i-diaper", Title: "补货：纸尿裤（用量预警）"},
				{Key: "chore:c-mop:2026-11-09", Title: "家务：拖地"},
				{Key: "chore:c-trash:2026-11-10", Title: "家务：倒垃圾"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Alerts(tt.home, today)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d alerts, want %d: %+v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i].Key != tt.want[i].Key || got[i].Title != tt.want[i].Title {
					t.Errorf("alert %d = {%q %q}, want {%q %q}", i, got[i].Key, got[i].Title, tt.want[i].Key, tt.want[i].Title)
				}
			}
		})
	}
}

func TestDigest(t *testing.T) {
	today := model.Day("2026-11-10")
	tests := []struct {
		name      string
		home      *model.Home
		wantOK    bool
		wantTitle string
		wantBody  string
	}{
		{name: "empty home", home: &model.Home{}, wantOK: false},
		{
			name:   "nothing needs attention",
			home:   &model.Home{Items: []model.Item{{ID: "i-rice", Title: "大米", HasQty: true, Qty: 5}}},
			wantOK: false,
		},
		{
			name:   "only an expiring item",
			home:   &model.Home{Items: []model.Item{{ID: "i-soy", Title: "生抽", Expires: "2026-11-12"}}},
			wantOK: true, wantTitle: "家庭管家：1 项需要处理", wantBody: "即将过期：生抽",
		},
		{
			name:   "only a chore due today",
			home:   &model.Home{Chores: []model.Chore{{ID: "c-trash", Title: "倒垃圾", Next: "2026-11-10"}}},
			wantOK: true, wantTitle: "家庭管家：1 项需要处理", wantBody: "到期家务：倒垃圾",
		},
		{
			name:      "grouped by kind in severity order, chores last",
			home:      busyHome(),
			wantOK:    true,
			wantTitle: "家庭管家：7 项需要处理",
			wantBody: strings.Join([]string{
				"已过期：酸奶",
				"已用完：纯牛奶",
				"低库存：鸡蛋",
				"用量预警：纸尿裤",
				"即将过期：生抽",
				"到期家务：拖地、倒垃圾",
			}, "\n"),
		},
		{
			name: "several items of one kind share a line",
			home: &model.Home{Items: []model.Item{
				{ID: "i-a", Title: "纯牛奶", HasQty: true, Qty: 0},
				{ID: "i-b", Title: "鸡蛋", HasQty: true, Qty: 0},
			}},
			wantOK: true, wantTitle: "家庭管家：2 项需要处理", wantBody: "已用完：纯牛奶、鸡蛋",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, body, ok := Digest(tt.home, today)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if title != tt.wantTitle {
				t.Errorf("title = %q, want %q", title, tt.wantTitle)
			}
			if body != tt.wantBody {
				t.Errorf("body = %q, want %q", body, tt.wantBody)
			}
		})
	}
}

func TestDigestLimits(t *testing.T) {
	today := model.Day("2026-11-10")

	// Long titles: the body is cut to 1000 runes with a trailing ellipsis.
	long := &model.Home{}
	for i := range 100 {
		long.Items = append(long.Items, model.Item{
			ID: fmt.Sprintf("i-%03d", i), Title: strings.Repeat("奶", 20) + fmt.Sprintf("%03d", i),
			Category: "食材", Expires: "2026-11-01",
		})
	}
	_, body, ok := Digest(long, today)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if n := utf8.RuneCountInString(body); n != 1000 {
		t.Errorf("body has %d runes, want 1000", n)
	}
	if !strings.HasSuffix(body, "…") {
		t.Errorf("cut body %q does not end with an ellipsis", body[len(body)-10:])
	}
	if !utf8.ValidString(body) {
		t.Error("cut body is not valid UTF-8")
	}

	// A short body is not cut.
	_, short, _ := Digest(busyHome(), today)
	if strings.HasSuffix(short, "…") {
		t.Errorf("short body %q was cut", short)
	}

	// The title is at most 120 runes, whatever the count.
	title, _, _ := Digest(manyOutHome(100000), today)
	if n := utf8.RuneCountInString(title); n > 120 {
		t.Errorf("title has %d runes, want at most 120", n)
	}
}

// readStateFile decodes the state file as the plain JSON it is on disk.
func readStateFile(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("state file is not JSON: %v\n%s", err, raw)
	}
	return m
}

func TestRunNotify(t *testing.T) {
	tests := []struct {
		name       string
		config     Config
		scopes     []string
		home       *model.Home
		now        time.Time
		wantNotice bool
	}{
		{name: "default mode and time, at 08:30 sends", scopes: []string{ScopeNotify}, home: busyHome(), now: at(8, 30), wantNotice: true},
		{name: "default mode without the scope sends nothing", home: busyHome(), now: at(8, 30)},
		{name: "before the time sends nothing", home: busyHome(), now: at(8, 29)},
		{name: "explicit notify mode", config: Config{Mode: "notify", At: "08:30"}, scopes: []string{ScopeNotify}, home: busyHome(), now: at(9, 0), wantNotice: true},
		{name: "both sends the notification", config: Config{Mode: "both"}, scopes: []string{ScopeNotify, ScopeTodos}, home: busyHome(), now: at(9, 0), wantNotice: true},
		{name: "todos only sends no notification", config: Config{Mode: "todos"}, scopes: []string{ScopeNotify, ScopeTodos}, home: busyHome(), now: at(9, 0)},
		{name: "off sends nothing", config: Config{Mode: "off"}, scopes: []string{ScopeNotify, ScopeTodos}, home: busyHome(), now: at(9, 0)},
		{name: "missing notify scope sends nothing and is no error", config: Config{Mode: "notify"}, scopes: []string{ScopeTodos}, home: busyHome(), now: at(9, 0)},
		{name: "nothing needs attention sends nothing", config: Config{Mode: "notify"}, scopes: []string{ScopeNotify}, home: &model.Home{}, now: at(9, 0)},
		{name: "custom time not yet reached", config: Config{At: "09:00"}, scopes: []string{ScopeNotify}, home: busyHome(), now: at(8, 59)},
		{name: "custom time reached", config: Config{At: "09:00"}, scopes: []string{ScopeNotify}, home: busyHome(), now: at(9, 0), wantNotice: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newFake(tt.scopes...)
			r := &Reminder{Hub: api, Path: filepath.Join(t.TempDir(), "remind.json"), Config: tt.config}
			if err := r.Run(context.Background(), tt.home, tt.now, cst); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := len(api.notices) == 1; got != tt.wantNotice {
				t.Fatalf("sent %d notices, want notice = %v", len(api.notices), tt.wantNotice)
			}
			if tt.wantNotice {
				n := api.notices[0]
				if n.ID != "household-2026-11-10" || n.UI != "household" {
					t.Errorf("notice ID/UI = %q/%q, want household-2026-11-10/household", n.ID, n.UI)
				}
				if n.Title != "家庭管家：7 项需要处理" || !strings.Contains(n.Body, "已过期：酸奶") {
					t.Errorf("notice = %+v", n)
				}
			}
		})
	}
}

func TestRunNotifyOncePerDay(t *testing.T) {
	api := newFake(ScopeNotify)
	path := filepath.Join(t.TempDir(), "remind.json")
	r := &Reminder{Hub: api, Path: path}
	ctx := context.Background()

	if err := r.Run(ctx, busyHome(), at(8, 30), cst); err != nil {
		t.Fatal(err)
	}
	if err := r.Run(ctx, busyHome(), at(8, 45), cst); err != nil {
		t.Fatal(err)
	}
	if err := r.Run(ctx, busyHome(), at(23, 59), cst); err != nil {
		t.Fatal(err)
	}
	if len(api.notices) != 1 {
		t.Fatalf("same day: sent %d notices, want 1", len(api.notices))
	}
	if got := readStateFile(t, path)["notified"]; got != "2026-11-10" {
		t.Errorf("notified = %v, want 2026-11-10", got)
	}

	// A new day sends again, with its own collapse id.
	next := time.Date(2026, 11, 11, 8, 30, 0, 0, cst)
	if err := r.Run(ctx, busyHome(), next, cst); err != nil {
		t.Fatal(err)
	}
	if len(api.notices) != 2 || api.notices[1].ID != "household-2026-11-11" {
		t.Fatalf("next day: notices = %+v", api.notices)
	}
}

func TestRunNotifyUsesLocalClock(t *testing.T) {
	// 00:30 UTC is 08:30 in the hub's zone: the reminder is due.
	api := newFake(ScopeNotify)
	r := &Reminder{Hub: api, Path: filepath.Join(t.TempDir(), "remind.json")}
	now := time.Date(2026, 11, 10, 0, 30, 0, 0, time.UTC)
	if err := r.Run(context.Background(), busyHome(), now, cst); err != nil {
		t.Fatal(err)
	}
	if len(api.notices) != 1 {
		t.Fatalf("sent %d notices, want 1", len(api.notices))
	}
}

func TestRunNotifyFailureRetried(t *testing.T) {
	api := newFake(ScopeNotify)
	path := filepath.Join(t.TempDir(), "remind.json")
	r := &Reminder{Hub: api, Path: path}
	ctx := context.Background()

	api.notifyErr = errors.New("hub 连不上")
	if err := r.Run(ctx, busyHome(), at(9, 0), cst); err == nil {
		t.Fatal("Run returned nil, want the notify error")
	}
	if _, ok := readStateFile(t, path)["notified"]; ok {
		t.Error("a failed notification was marked sent")
	}

	api.notifyErr = nil
	if err := r.Run(ctx, busyHome(), at(9, 10), cst); err != nil {
		t.Fatal(err)
	}
	if len(api.notices) != 1 {
		t.Fatalf("retry: sent %d notices, want 1", len(api.notices))
	}
}

func TestRunTodos(t *testing.T) {
	ctx := context.Background()

	t.Run("one to-do per alert, due today, created once", func(t *testing.T) {
		api := newFake(ScopeTodos)
		path := filepath.Join(t.TempDir(), "remind.json")
		r := &Reminder{Hub: api, Path: path, Config: Config{Mode: "todos"}}

		if err := r.Run(ctx, busyHome(), at(9, 0), cst); err != nil {
			t.Fatal(err)
		}
		if len(api.todos) != len(busyAlertKeys) {
			t.Fatalf("created %d to-dos, want %d", len(api.todos), len(busyAlertKeys))
		}
		for _, td := range api.todos {
			if td.DueDate != "2026-11-10" {
				t.Errorf("to-do %q due %q, want 2026-11-10", td.Title, td.DueDate)
			}
		}
		if api.todos[0].Title != "补货：酸奶（已过期）" {
			t.Errorf("first to-do = %q", api.todos[0].Title)
		}

		// The same state again creates nothing.
		if err := r.Run(ctx, busyHome(), at(9, 10), cst); err != nil {
			t.Fatal(err)
		}
		if len(api.todos) != len(busyAlertKeys) || len(api.done) != 0 {
			t.Fatalf("second run: %d to-dos, %d done; want no change", len(api.todos), len(api.done))
		}

		state := readStateFile(t, path)["todos"].(map[string]any)
		if len(state) != len(busyAlertKeys) {
			t.Errorf("state has %d keys, want %d", len(state), len(busyAlertKeys))
		}
		for _, k := range busyAlertKeys {
			if _, ok := state[k]; !ok {
				t.Errorf("state lacks key %q", k)
			}
		}
	})

	t.Run("at most 20 created per run, the rest next run", func(t *testing.T) {
		api := newFake(ScopeTodos)
		path := filepath.Join(t.TempDir(), "remind.json")
		r := &Reminder{Hub: api, Path: path, Config: Config{Mode: "todos"}}
		home := manyOutHome(25)

		if err := r.Run(ctx, home, at(9, 0), cst); err != nil {
			t.Fatal(err)
		}
		if len(api.todos) != 20 {
			t.Fatalf("first run created %d to-dos, want 20", len(api.todos))
		}
		if err := r.Run(ctx, home, at(9, 10), cst); err != nil {
			t.Fatal(err)
		}
		if len(api.todos) != 25 {
			t.Fatalf("second run: %d to-dos in total, want 25", len(api.todos))
		}
		if len(readStateFile(t, path)["todos"].(map[string]any)) != 25 {
			t.Error("state does not hold all 25 to-dos")
		}
	})

	t.Run("a finished alert marks its to-do done and drops it", func(t *testing.T) {
		api := newFake(ScopeTodos)
		path := filepath.Join(t.TempDir(), "remind.json")
		r := &Reminder{Hub: api, Path: path, Config: Config{Mode: "todos"}}

		if err := r.Run(ctx, busyHome(), at(9, 0), cst); err != nil {
			t.Fatal(err)
		}
		// Bought and done: only the rice is left, and the chores are no longer due.
		rest := &model.Home{
			Items:  []model.Item{{ID: "i-rice", Title: "大米", HasQty: true, Qty: 5}},
			Chores: []model.Chore{{ID: "c-mop", Title: "拖地", Next: "2026-11-17"}},
		}
		if err := r.Run(ctx, rest, at(9, 10), cst); err != nil {
			t.Fatal(err)
		}
		if len(api.done) != len(busyAlertKeys) {
			t.Fatalf("marked %d to-dos done, want %d", len(api.done), len(busyAlertKeys))
		}
		if len(api.todos) != len(busyAlertKeys) {
			t.Errorf("created %d to-dos, want no new ones", len(api.todos))
		}
		if got := readStateFile(t, path)["todos"]; got != nil {
			t.Errorf("state keeps to-dos %v, want none", got)
		}
	})

	t.Run("missing todos scope or off mode does nothing", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			config Config
			scopes []string
		}{
			{name: "scope missing", config: Config{Mode: "todos"}, scopes: []string{ScopeNotify}},
			{name: "mode notify", config: Config{Mode: "notify"}, scopes: []string{ScopeTodos}},
			{name: "mode off", config: Config{Mode: "off"}, scopes: []string{ScopeTodos, ScopeNotify}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				api := newFake(tc.scopes...)
				r := &Reminder{Hub: api, Path: filepath.Join(t.TempDir(), "remind.json"), Config: tc.config}
				if err := r.Run(ctx, busyHome(), at(9, 0), cst); err != nil {
					t.Fatalf("Run: %v", err)
				}
				if len(api.todos) != 0 || len(api.done) != 0 {
					t.Errorf("created %d, done %d to-dos; want none", len(api.todos), len(api.done))
				}
			})
		}
	})

	t.Run("both mode notifies and creates to-dos", func(t *testing.T) {
		api := newFake(ScopeNotify, ScopeTodos)
		r := &Reminder{Hub: api, Path: filepath.Join(t.TempDir(), "remind.json"), Config: Config{Mode: "both"}}
		if err := r.Run(ctx, busyHome(), at(9, 0), cst); err != nil {
			t.Fatal(err)
		}
		if len(api.notices) != 1 || len(api.todos) != len(busyAlertKeys) {
			t.Fatalf("notices %d, to-dos %d", len(api.notices), len(api.todos))
		}
	})
}

func TestRunTodoCreateFailureRetried(t *testing.T) {
	ctx := context.Background()
	api := newFake(ScopeTodos)
	api.failCreate = 3
	path := filepath.Join(t.TempDir(), "remind.json")
	r := &Reminder{Hub: api, Path: path, Config: Config{Mode: "todos"}}

	if err := r.Run(ctx, busyHome(), at(9, 0), cst); err == nil {
		t.Fatal("Run returned nil, want the create error")
	}
	state := readStateFile(t, path)["todos"].(map[string]any)
	if len(state) != len(busyAlertKeys)-1 {
		t.Fatalf("after the failure the state has %d to-dos, want the %d that succeeded", len(state), len(busyAlertKeys)-1)
	}
	if _, ok := state[busyAlertKeys[2]]; ok {
		t.Error("the failed to-do was marked as created")
	}

	// The next round creates the failed one and the rest.
	if err := r.Run(ctx, busyHome(), at(9, 10), cst); err != nil {
		t.Fatal(err)
	}
	if got := len(readStateFile(t, path)["todos"].(map[string]any)); got != len(busyAlertKeys) {
		t.Errorf("state has %d to-dos, want %d", got, len(busyAlertKeys))
	}
	if len(api.todos) != len(busyAlertKeys) {
		t.Errorf("created %d to-dos in total, want %d", len(api.todos), len(busyAlertKeys))
	}
}

func TestRunTodoDoneFailureRetried(t *testing.T) {
	ctx := context.Background()
	api := newFake(ScopeTodos)
	path := filepath.Join(t.TempDir(), "remind.json")
	r := &Reminder{Hub: api, Path: path, Config: Config{Mode: "todos"}}

	if err := r.Run(ctx, busyHome(), at(9, 0), cst); err != nil {
		t.Fatal(err)
	}

	// Every alert is gone. The to-dos are marked done in key order: chore:c-mop (todo-5),
	// chore:c-trash (todo-6), item:i-diaper (todo-4), item:i-egg (todo-3), item:i-milk (todo-2), item:i-yogurt (todo-1).
	// The one for item:i-milk fails; the others are still marked done, and only that one stays in the state.
	api.failDone = "todo-2"
	if err := r.Run(ctx, &model.Home{}, at(9, 10), cst); err == nil {
		t.Fatal("Run returned nil, want the done error")
	}
	state := readStateFile(t, path)["todos"].(map[string]any)
	wantLeft := []string{"item:i-milk"}
	if len(state) != len(wantLeft) {
		t.Fatalf("state keeps %v, want %v", state, wantLeft)
	}
	for _, k := range wantLeft {
		if _, ok := state[k]; !ok {
			t.Errorf("state lacks %q", k)
		}
	}

	api.failDone = ""
	if err := r.Run(ctx, &model.Home{}, at(9, 20), cst); err != nil {
		t.Fatal(err)
	}
	if len(api.done) != len(busyAlertKeys) {
		t.Errorf("marked %d to-dos done in total, want %d", len(api.done), len(busyAlertKeys))
	}
	if got := readStateFile(t, path)["todos"]; got != nil {
		t.Errorf("state keeps to-dos %v, want none", got)
	}
}

func TestRunStateFile(t *testing.T) {
	ctx := context.Background()

	t.Run("corrupt state is an empty state", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "remind.json")
		if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		api := newFake(ScopeNotify, ScopeTodos)
		r := &Reminder{Hub: api, Path: path, Config: Config{Mode: "both"}}
		if err := r.Run(ctx, busyHome(), at(9, 0), cst); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if len(api.notices) != 1 || len(api.todos) != len(busyAlertKeys) {
			t.Fatalf("notices %d, to-dos %d; want 1 and %d", len(api.notices), len(api.todos), len(busyAlertKeys))
		}
		readStateFile(t, path) // the file is valid JSON again
	})

	t.Run("a state of the wrong shape is an empty state", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "remind.json")
		if err := os.WriteFile(path, []byte(`{"notified": 7, "todos": []}`), 0o600); err != nil {
			t.Fatal(err)
		}
		api := newFake(ScopeNotify)
		r := &Reminder{Hub: api, Path: path, Config: Config{Mode: "notify"}}
		if err := r.Run(ctx, busyHome(), at(9, 0), cst); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if len(api.notices) != 1 {
			t.Fatalf("sent %d notices, want 1", len(api.notices))
		}
	})

	t.Run("a missing state file is an empty state and is created", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "remind.json")
		api := newFake(ScopeNotify)
		r := &Reminder{Hub: api, Path: path, Config: Config{Mode: "notify"}}
		if err := r.Run(ctx, busyHome(), at(9, 0), cst); err != nil {
			t.Fatal(err)
		}
		readStateFile(t, path)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		if !slices.Equal(names, []string{"remind.json"}) {
			t.Errorf("directory holds %v, want only remind.json (no temporary file left)", names)
		}
	})
}

func TestRunInvalidTime(t *testing.T) {
	api := newFake(ScopeNotify)
	r := &Reminder{Hub: api, Path: filepath.Join(t.TempDir(), "remind.json"), Config: Config{At: "25:99"}}
	if err := r.Run(context.Background(), busyHome(), at(9, 0), cst); err == nil {
		t.Fatal("Run with an invalid time returned nil")
	}
	if len(api.notices) != 0 {
		t.Errorf("sent %d notices with an invalid time", len(api.notices))
	}
}

// A failing notification does not stop the to-dos, and the error is returned.
func TestRunNotifyFailureDoesNotBlockTodos(t *testing.T) {
	api := newFake(ScopeNotify, ScopeTodos)
	api.notifyErr = errors.New("hub 连不上")
	path := filepath.Join(t.TempDir(), "remind.json")
	r := &Reminder{Hub: api, Path: path, Config: Config{Mode: "both"}}

	if err := r.Run(context.Background(), busyHome(), at(9, 0), cst); err == nil {
		t.Fatal("Run returned nil, want the notify error")
	}
	if len(api.todos) != len(busyAlertKeys) {
		t.Fatalf("created %d to-dos, want %d although the notification failed", len(api.todos), len(busyAlertKeys))
	}
	if got := len(readStateFile(t, path)["todos"].(map[string]any)); got != len(busyAlertKeys) {
		t.Errorf("state has %d to-dos, want %d", got, len(busyAlertKeys))
	}
}

// An invalid time makes only the notification fail: to-dos are still created in both and todos modes. The todos
// mode has no notification, so it does not use the time and returns no error.
func TestRunInvalidTimeOnlyFailsNotify(t *testing.T) {
	for _, mode := range []string{"todos", "both"} {
		t.Run(mode, func(t *testing.T) {
			api := newFake(ScopeNotify, ScopeTodos)
			r := &Reminder{Hub: api, Path: filepath.Join(t.TempDir(), "remind.json"), Config: Config{Mode: mode, At: "8:30pm"}}
			err := r.Run(context.Background(), busyHome(), at(9, 0), cst)
			if (err != nil) != (mode == "both") {
				t.Fatalf("Run error = %v, want an error only in both mode", err)
			}
			if len(api.notices) != 0 {
				t.Errorf("sent %d notices with an invalid time", len(api.notices))
			}
			if len(api.todos) != len(busyAlertKeys) {
				t.Errorf("created %d to-dos, want %d", len(api.todos), len(busyAlertKeys))
			}
		})
	}
}

// Every create fails: at most 20 creations are attempted in one run, and the error is returned.
func TestRunTodoCreateAttemptsCapped(t *testing.T) {
	api := newFake(ScopeTodos)
	api.failCreates = true
	r := &Reminder{Hub: api, Path: filepath.Join(t.TempDir(), "remind.json"), Config: Config{Mode: "todos"}}
	if err := r.Run(context.Background(), manyOutHome(25), at(9, 0), cst); err == nil {
		t.Fatal("Run returned nil, want the create errors")
	}
	if api.creates != 20 {
		t.Errorf("attempted %d creations, want 20", api.creates)
	}
}

// An item keeps its one to-do while its finding changes: the to-do is neither closed nor repeated.
func TestRunItemKeepsTodoAcrossFindingChange(t *testing.T) {
	ctx := context.Background()
	api := newFake(ScopeTodos)
	r := &Reminder{Hub: api, Path: filepath.Join(t.TempDir(), "remind.json"), Config: Config{Mode: "todos"}}
	if err := r.Run(ctx, busyHome(), at(9, 0), cst); err != nil {
		t.Fatal(err)
	}

	// The diapers are now used up: the finding changes from 用量预警 to 已用完.
	home := busyHome()
	for i := range home.Items {
		if home.Items[i].ID == "i-diaper" {
			home.Items[i].Qty = 0
		}
	}
	if err := r.Run(ctx, home, at(9, 10), cst); err != nil {
		t.Fatal(err)
	}
	if len(api.todos) != len(busyAlertKeys) || len(api.done) != 0 {
		t.Fatalf("after the finding changed: %d to-dos created, %d done; want no change", len(api.todos), len(api.done))
	}
}

// Switching the mode off closes the to-dos already created, once the alerts are gone or not: they are not
// needed any more. Without the todos.write scope the state is left alone.
func TestRunTodosClosedWhenModeTurnsOff(t *testing.T) {
	ctx := context.Background()
	api := newFake(ScopeNotify, ScopeTodos)
	path := filepath.Join(t.TempDir(), "remind.json")
	r := &Reminder{Hub: api, Path: path, Config: Config{Mode: "both"}}
	if err := r.Run(ctx, busyHome(), at(9, 0), cst); err != nil {
		t.Fatal(err)
	}

	// Mode notify, the alerts still there: the to-dos are closed anyway.
	r.Config.Mode = "notify"
	if err := r.Run(ctx, busyHome(), at(9, 10), cst); err != nil {
		t.Fatal(err)
	}
	if len(api.done) != len(busyAlertKeys) {
		t.Fatalf("marked %d to-dos done, want %d", len(api.done), len(busyAlertKeys))
	}
	if got := readStateFile(t, path)["todos"]; got != nil {
		t.Errorf("state keeps to-dos %v, want none", got)
	}
	if len(api.todos) != len(busyAlertKeys) {
		t.Errorf("created %d to-dos in total, want no new ones", len(api.todos))
	}
}

func TestRunTodosNotClosedWithoutScope(t *testing.T) {
	ctx := context.Background()
	api := newFake(ScopeNotify, ScopeTodos)
	path := filepath.Join(t.TempDir(), "remind.json")
	r := &Reminder{Hub: api, Path: path, Config: Config{Mode: "both"}}
	if err := r.Run(ctx, busyHome(), at(9, 0), cst); err != nil {
		t.Fatal(err)
	}

	// The scope is withdrawn: nothing is sent and the state keeps the to-dos.
	api.scopes[ScopeTodos] = false
	r.Config.Mode = "notify"
	if err := r.Run(ctx, &model.Home{}, at(9, 10), cst); err != nil {
		t.Fatal(err)
	}
	if len(api.done) != 0 {
		t.Errorf("marked %d to-dos done without the scope", len(api.done))
	}
	if got := len(readStateFile(t, path)["todos"].(map[string]any)); got != len(busyAlertKeys) {
		t.Errorf("state has %d to-dos, want %d", got, len(busyAlertKeys))
	}
}
