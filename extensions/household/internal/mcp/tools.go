package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
	"github.com/nwverse/persona-extensions/extensions/household/internal/ops"
	"github.com/nwverse/persona-extensions/extensions/household/internal/rules"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
)

// Tool is one tool of the server. Read marks a tool that never changes the data (readOnlyHint). Input builds its JSON
// Schema. Run does the work and returns the answer, which is marshalled to JSON as both the text and the structured
// content of the result.
type Tool struct {
	Name, About string
	Read        bool
	Input       func() map[string]any
	Run         func(e env, raw json.RawMessage) (any, error)
}

// destructive names the tools that overwrite stored data or delete a record (destructiveHint). Restock only adds
// purchases, so it is not destructive.
var destructive = map[string]bool{
	"household_save": true, "household_use": true, "household_purchase": true,
	"household_chore_done": true, "household_remove": true,
}

// table is the one list of tools, in the order the server lists them. The manifest allows exactly the read-only ones.
var table = []Tool{
	{Name: "household_status", Read: true, Input: statusInput, Run: runStatus,
		About: "家庭管家总览：需要处理的物品（已过期、已用完、低库存、用量预警、即将过期）、待买的采购、到期的家务、今天和明天计划的餐食，以及汇总数字。"},
	{Name: "household_list", Read: true, Input: listInput, Run: runList,
		About: "列出一类记录：位置、物品、采购、家务、菜谱或餐食；可以只看标题里有某段文字的。物品带有估算数量、状态和所在位置。"},
	{Name: "household_meal_suggest", Read: true, Input: suggestInput, Run: runSuggest,
		About: "按手上有的食材推荐菜谱，列出缺的食材和应该先用掉的食材。"},
	{Name: "household_save", Input: saveInput, Run: runSave,
		About: "新建或修改一条记录（位置、物品、采购、家务、菜谱或餐食）。填编号则修改，属性按类型的结构检查。计划餐食用这个工具：类型 meal，带 date、slot 和 recipe。采购的状态改动不会入库，入库请用 household_purchase。"},
	{Name: "household_use", Input: useInput, Run: runUse,
		About: "用掉物品的一部分：估算数量减去这个数量，并把数量的记录日期设为今天。"},
	{Name: "household_restock", Input: restockInput, Run: runRestock,
		About: "一键补货：为每个需要买的物品（已过期、已用完、低库存或用量预警）建一条待买采购；已有待买采购的不重复建。"},
	{Name: "household_purchase", Input: purchaseInput, Run: runPurchase,
		About: "关闭采购：bought 为真（默认）时标为已买，并把物品补进库存（物品已过期的，旧数量作废，只剩这次买的数量，过期日清掉）；为假时标为不买，库存不变。"},
	{Name: "household_chore_done", Input: choreInput, Run: runChoreDone,
		About: "完成一次家务：记下今天完成，并按周期安排下一次；没有周期的家务完成后不再安排。"},
	{Name: "household_remove", Input: removeInput, Run: runRemove,
		About: "删除一条记录。指向它的关联会被去掉。不能撤销。"},
}

// Tools returns the tools in the order the server lists them.
func Tools() []Tool { return slices.Clone(table) }

// find returns the tool with the name.
func find(name string) (Tool, bool) {
	i := slices.IndexFunc(table, func(t Tool) bool { return t.Name == name })
	if i < 0 {
		return Tool{}, false
	}
	return table[i], true
}

// typeKeys are the types the tools accept, in the order the schema lists them; typeNames are their names.
var (
	typeKeys  = []string{model.TPlace, model.TItem, model.TBuy, model.TChore, model.TRecipe, model.TMeal}
	typeNames = map[string]string{
		model.TPlace: "位置", model.TItem: "物品", model.TBuy: "采购",
		model.TChore: "家务", model.TRecipe: "菜谱", model.TMeal: "餐食",
	}
)

// env is what a tool gets: the base, the time of the call and the hub's zone.
type env struct {
	b   *kernel.Base
	now time.Time
	loc *time.Location
}

func (e env) today() model.Day { return model.DayOf(e.now, e.loc) }

// board reads the household space as the tools show it. A base without the space reads as empty.
func (e env) board() board { return newBoard(model.Load(space(e.b), e.loc), e.today()) }

// one answers a write with the record it changed.
func (e env) one(typ, id string) (any, error) {
	ents, err := e.board().entries(typ)
	if err != nil {
		return nil, err
	}
	for _, en := range ents {
		if en.ID == id {
			return oneOut{Type: typ, Record: en.View}, nil
		}
	}
	return nil, fmt.Errorf("没有记录「%s」", id)
}

// many answers a write with the records it changed, in the order of ids.
func (e env) many(typ string, ids []string) (any, error) {
	ents, err := e.board().entries(typ)
	if err != nil {
		return nil, err
	}
	views := map[string]any{}
	for _, en := range ents {
		views[en.ID] = en.View
	}
	out := []any{}
	for _, id := range ids {
		if v, ok := views[id]; ok {
			out = append(out, v)
		}
	}
	return manyOut{Records: out}, nil
}

// space is the household space of the base, nil when it has none yet.
func space(b *kernel.Base) *kernel.Space {
	for _, sp := range b.Spaces {
		if sp.ID == model.SpaceID {
			return sp
		}
	}
	return nil
}

// ---- schemas ----

// object is a JSON Schema object that refuses every property it does not list.
func object(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "additionalProperties": false, "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

// str is a JSON Schema string with a description.
func str(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

// typeProp is a type key, one of the six.
func typeProp(desc string) map[string]any {
	return map[string]any{"type": "string", "enum": typeKeys, "description": desc}
}

func statusInput() map[string]any { return object(map[string]any{}) }

func listInput() map[string]any {
	return object(map[string]any{
		"type": typeProp("类型：place 位置、item 物品、buy 采购、chore 家务、recipe 菜谱、meal 餐食"),
		"text": str("只看标题里含有这段文字的记录"),
	}, "type")
}

func suggestInput() map[string]any {
	return object(map[string]any{
		"limit": map[string]any{"type": "integer", "minimum": 0, "description": "最多给几道菜；不填或为 0 时全部给出"},
	})
}

func saveInput() map[string]any {
	return object(map[string]any{
		"id":     str("记录编号：填写则修改这条记录，不填则新建"),
		"type":   typeProp("类型（只用于新建）"),
		"title":  map[string]any{"type": "string", "maxLength": kernel.MaxTitle, "description": "名称"},
		"status": str("状态：采购为 待买、已买、不买；餐食为 计划、已做、取消"),
		"values": map[string]any{"type": "object", "description": "属性值：键为属性名，合并写入，null 清除。例如物品的 qty、unit、expires、place"},
	})
}

func useInput() map[string]any {
	return object(map[string]any{
		"id":     str("物品编号"),
		"amount": map[string]any{"type": "number", "exclusiveMinimum": 0, "description": "用掉的数量，与物品的单位相同"},
	}, "id", "amount")
}

func restockInput() map[string]any { return object(map[string]any{}) }

func purchaseInput() map[string]any {
	return object(map[string]any{
		"ids": map[string]any{
			"type": "array", "minItems": 1, "items": map[string]any{"type": "string"}, "description": "采购记录的编号",
		},
		"bought": map[string]any{"type": "boolean", "description": "true 为已买（默认），false 为不买"},
	}, "ids")
}

func choreInput() map[string]any {
	return object(map[string]any{"id": str("家务编号")}, "id")
}

func removeInput() map[string]any {
	return object(map[string]any{"id": str("要删除的记录编号")}, "id")
}

// ---- answers ----

// oneOut is the answer of a write that changed one record.
type oneOut struct {
	Type   string `json:"type"`
	Record any    `json:"record"`
}

// manyOut is the answer of a write that changed several records.
type manyOut struct {
	Records []any `json:"records"`
}

// removeOut says what was removed.
type removeOut struct {
	Removed string `json:"removed"`
	Type    string `json:"type"`
	Title   string `json:"title"`
}

// refOut is a record named by id and title.
type refOut struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type placeOut struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Note  string `json:"note,omitempty"`
	Items int    `json:"items"`
}

// itemOut is an item with its estimate and finding. Qty, Est and Min are set only when the item has them.
type itemOut struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Category string    `json:"category,omitempty"`
	Place    string    `json:"place,omitempty"`
	PlaceID  string    `json:"place_id,omitempty"`
	Qty      *float64  `json:"qty,omitempty"`
	Est      *float64  `json:"est,omitempty"`
	Unit     string    `json:"unit,omitempty"`
	Min      *float64  `json:"min,omitempty"`
	Daily    float64   `json:"daily,omitempty"`
	Lead     int       `json:"lead"`
	Refill   float64   `json:"refill,omitempty"`
	Expires  model.Day `json:"expires,omitempty"`
	Counted  model.Day `json:"counted,omitempty"`
	Finding  string    `json:"finding,omitempty"`
	Label    string    `json:"label,omitempty"`
	Days     *int      `json:"days,omitempty"`
	Note     string    `json:"note,omitempty"`
}

type buyOut struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Status   string  `json:"status"`
	Item     string  `json:"item,omitempty"`
	ItemID   string  `json:"item_id,omitempty"`
	Amount   float64 `json:"amount"`
	Unit     string  `json:"unit,omitempty"`
	Category string  `json:"category,omitempty"`
	Reason   string  `json:"reason,omitempty"`
	Note     string  `json:"note,omitempty"`
}

type choreOut struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Who     string    `json:"who,omitempty"`
	Place   string    `json:"place,omitempty"`
	PlaceID string    `json:"place_id,omitempty"`
	Every   float64   `json:"every,omitempty"`
	Minutes float64   `json:"minutes,omitempty"`
	Last    model.Day `json:"last,omitempty"`
	Next    model.Day `json:"next,omitempty"`
	Due     bool      `json:"due"`
	Overdue int       `json:"overdue,omitempty"`
	Note    string    `json:"note,omitempty"`
}

type recipeOut struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Needs   []refOut `json:"needs"`
	Minutes float64  `json:"minutes,omitempty"`
	URL     string   `json:"url,omitempty"`
	Note    string   `json:"note,omitempty"`
}

type mealOut struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Status   string    `json:"status"`
	Date     model.Day `json:"date,omitempty"`
	Slot     string    `json:"slot,omitempty"`
	Recipe   string    `json:"recipe,omitempty"`
	RecipeID string    `json:"recipe_id,omitempty"`
	Note     string    `json:"note,omitempty"`
}

// summaryOut counts what needs attention, as rules.Summarize does.
type summaryOut struct {
	Expired   int    `json:"expired"`
	Out       int    `json:"out"`
	Low       int    `json:"low"`
	Runout    int    `json:"runout"`
	Expiring  int    `json:"expiring"`
	OpenBuys  int    `json:"open_buys"`
	ChoresDue int    `json:"chores_due"`
	Alerts    int    `json:"alerts"`
	Tone      string `json:"tone"`
}

type statusOut struct {
	Today     model.Day  `json:"today"`
	Summary   summaryOut `json:"summary"`
	Findings  []itemOut  `json:"findings"`
	Purchases []buyOut   `json:"purchases"`
	ChoresDue []choreOut `json:"chores_due"`
	Meals     []mealOut  `json:"meals"`
}

type ideaOut struct {
	Recipe   refOut   `json:"recipe"`
	Have     int      `json:"have"`
	Total    int      `json:"total"`
	Missing  []string `json:"missing"`
	UseFirst []string `json:"use_first"`
}

type suggestOut struct {
	Ideas []ideaOut `json:"ideas"`
}

// entry is one record of a type: its id, its title (what a text filter matches) and its answer.
type entry struct {
	ID, Title string
	View      any
}

// board is the household as the tools read it: the typed records, what each item and chore needs, and today.
type board struct {
	h       *model.Home
	today   model.Day
	finds   map[string]rules.Finding
	dues    map[string]rules.Due
	places  map[string]string
	items   map[string]string
	recipes map[string]string
}

func newBoard(h *model.Home, today model.Day) board {
	b := board{
		h: h, today: today,
		finds: map[string]rules.Finding{}, dues: map[string]rules.Due{},
		places: map[string]string{}, items: map[string]string{}, recipes: map[string]string{},
	}
	for _, f := range rules.Findings(h, today) {
		b.finds[f.Item.ID] = f
	}
	for _, d := range rules.ChoresDue(h, today) {
		b.dues[d.Chore.ID] = d
	}
	for _, p := range h.Places {
		b.places[p.ID] = p.Title
	}
	for _, it := range h.Items {
		b.items[it.ID] = it.Title
	}
	for _, r := range h.Recipes {
		b.recipes[r.ID] = r.Title
	}
	return b
}

// entries returns the records of the type in the space's order.
func (b board) entries(typ string) ([]entry, error) {
	var out []entry
	switch typ {
	case model.TPlace:
		for _, p := range b.h.Places {
			out = append(out, entry{p.ID, p.Title, b.place(p)})
		}
	case model.TItem:
		for _, it := range b.h.Items {
			out = append(out, entry{it.ID, it.Title, b.item(it)})
		}
	case model.TBuy:
		for _, bu := range b.h.Buys {
			out = append(out, entry{bu.ID, bu.Title, b.buy(bu)})
		}
	case model.TChore:
		for _, c := range b.h.Chores {
			out = append(out, entry{c.ID, c.Title, b.chore(c)})
		}
	case model.TRecipe:
		for _, r := range b.h.Recipes {
			out = append(out, entry{r.ID, r.Title, b.recipe(r)})
		}
	case model.TMeal:
		for _, m := range b.h.Meals {
			out = append(out, entry{m.ID, m.Title, b.meal(m)})
		}
	default:
		return nil, fmt.Errorf("没有类型「%s」，可用的有 %s", typ, strings.Join(typeKeys, "、"))
	}
	return out, nil
}

func (b board) place(p model.Place) placeOut {
	return placeOut{ID: p.ID, Title: p.Title, Note: p.Note, Items: p.Items}
}

func (b board) item(it model.Item) itemOut {
	out := itemOut{
		ID: it.ID, Title: it.Title, Category: it.Category,
		Place: b.places[it.PlaceID], PlaceID: it.PlaceID, Unit: it.Unit,
		Daily: it.Daily, Lead: rules.Lead(it), Refill: it.Refill,
		Expires: it.Expires, Counted: it.Counted, Note: it.Note,
	}
	if it.HasQty {
		qty, est := it.Qty, rules.Estimate(it, b.today)
		out.Qty, out.Est = &qty, &est
	}
	if it.HasMin {
		minimum := it.Min
		out.Min = &minimum
	}
	if f, ok := b.finds[it.ID]; ok {
		out.Finding, out.Label = string(f.Kind), f.Kind.Label()
		if f.Kind == rules.Expired || f.Kind == rules.Expiring || f.Kind == rules.Runout {
			days := f.Days
			out.Days = &days
		}
	}
	return out
}

func (b board) buy(bu model.Buy) buyOut {
	return buyOut{
		ID: bu.ID, Title: bu.Title, Status: bu.Status,
		Item: b.items[bu.ItemID], ItemID: bu.ItemID, Amount: bu.Amount, Unit: bu.Unit,
		Category: bu.Category, Reason: bu.Reason, Note: bu.Note,
	}
}

func (b board) chore(c model.Chore) choreOut {
	d, due := b.dues[c.ID]
	return choreOut{
		ID: c.ID, Title: c.Title, Who: c.Who,
		Place: b.places[c.PlaceID], PlaceID: c.PlaceID,
		Every: c.Every, Minutes: c.Minutes, Last: c.Last, Next: c.Next,
		Due: due, Overdue: d.Overdue, Note: c.Note,
	}
}

func (b board) recipe(r model.Recipe) recipeOut {
	needs := make([]refOut, 0, len(r.Needs))
	for _, id := range r.Needs {
		title := b.items[id]
		if title == "" {
			title = "?"
		}
		needs = append(needs, refOut{ID: id, Title: title})
	}
	return recipeOut{ID: r.ID, Title: r.Title, Needs: needs, Minutes: r.Minutes, URL: r.URL, Note: r.Note}
}

func (b board) meal(m model.Meal) mealOut {
	return mealOut{
		ID: m.ID, Title: m.Title, Status: m.Status, Date: m.Date, Slot: m.Slot,
		Recipe: b.recipes[m.RecipeID], RecipeID: m.RecipeID, Note: m.Note,
	}
}

// nonNil turns a nil list into an empty one, so that it is written as [].
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// noArgs refuses any argument: the tool takes none.
func noArgs(raw json.RawMessage) error {
	return decode(raw, &struct{}{})
}

// entityIn finds a record of the space by id, nil when there is none.
func entityIn(sp *kernel.Space, id string) *kernel.Entity {
	if sp == nil {
		return nil
	}
	for _, e := range sp.Entities {
		if e.ID == id {
			return e
		}
	}
	return nil
}

// idsOf is the ids of the records.
func idsOf(es []*kernel.Entity) []string {
	ids := make([]string, 0, len(es))
	for _, e := range es {
		ids = append(ids, e.ID)
	}
	return ids
}

// ---- status ----

func runStatus(e env, raw json.RawMessage) (any, error) {
	if err := noArgs(raw); err != nil {
		return nil, err
	}
	b := e.board()
	sum := rules.Summarize(b.h, b.today)
	out := statusOut{
		Today: b.today,
		Summary: summaryOut{
			Expired: sum.Expired, Out: sum.Out, Low: sum.Low, Runout: sum.Runout, Expiring: sum.Expiring,
			OpenBuys: sum.OpenBuys, ChoresDue: sum.ChoresDue, Alerts: sum.Alerts, Tone: sum.Tone,
		},
		Findings: []itemOut{}, Purchases: []buyOut{}, ChoresDue: []choreOut{}, Meals: []mealOut{},
	}
	for _, f := range rules.Findings(b.h, b.today) {
		out.Findings = append(out.Findings, b.item(f.Item))
	}
	for _, bu := range b.h.Buys {
		if bu.Status == model.BuyTodo {
			out.Purchases = append(out.Purchases, b.buy(bu))
		}
	}
	for _, d := range rules.ChoresDue(b.h, b.today) {
		out.ChoresDue = append(out.ChoresDue, b.chore(d.Chore))
	}
	tomorrow := b.today.Add(1)
	for _, m := range b.h.Meals {
		if m.Status == model.MealPlan && (m.Date == b.today || m.Date == tomorrow) {
			out.Meals = append(out.Meals, b.meal(m))
		}
	}
	return out, nil
}

// ---- list ----

func runList(e env, raw json.RawMessage) (any, error) {
	var a struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	if a.Type == "" {
		return nil, fmt.Errorf("需要指定类型，可用的有 %s", strings.Join(typeKeys, "、"))
	}
	ents, err := e.board().entries(a.Type)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(a.Text)
	out := []any{}
	for _, en := range ents {
		if needle != "" && !strings.Contains(strings.ToLower(en.Title), needle) {
			continue
		}
		out = append(out, en.View)
	}
	return manyOut{Records: out}, nil
}

// ---- meal suggestions ----

func runSuggest(e env, raw json.RawMessage) (any, error) {
	var a struct {
		Limit int `json:"limit"`
	}
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	b := e.board()
	out := suggestOut{Ideas: []ideaOut{}}
	for _, idea := range rules.MealIdeas(b.h, b.today, a.Limit) {
		out.Ideas = append(out.Ideas, ideaOut{
			Recipe:   refOut{ID: idea.Recipe.ID, Title: idea.Recipe.Title},
			Have:     idea.Have,
			Total:    idea.Total,
			Missing:  nonNil(idea.Missing),
			UseFirst: nonNil(idea.UseFirst),
		})
	}
	return out, nil
}

// ---- writes ----

func runSave(e env, raw json.RawMessage) (any, error) {
	var in kernel.EntityInput
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	ent, err := ops.Save(e.b, in, e.now, e.loc)
	if err != nil {
		return nil, err
	}
	return e.one(ent.Type, ent.ID)
}

func runUse(e env, raw json.RawMessage) (any, error) {
	var a struct {
		ID     string  `json:"id"`
		Amount float64 `json:"amount"`
	}
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	if a.ID == "" {
		return nil, errors.New("需要物品的编号")
	}
	ent, err := ops.Use(e.b, a.ID, a.Amount, e.now, e.loc)
	if err != nil {
		return nil, err
	}
	return e.one(ent.Type, ent.ID)
}

func runRestock(e env, raw json.RawMessage) (any, error) {
	if err := noArgs(raw); err != nil {
		return nil, err
	}
	ents, err := ops.Restock(e.b, e.now, e.loc)
	if err != nil {
		return nil, err
	}
	return e.many(model.TBuy, idsOf(ents))
}

func runPurchase(e env, raw json.RawMessage) (any, error) {
	var a struct {
		IDs    []string `json:"ids"`
		Bought *bool    `json:"bought"`
	}
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	if len(a.IDs) == 0 {
		return nil, errors.New("需要至少一条采购的编号")
	}
	bought := a.Bought == nil || *a.Bought
	// Each id is processed once, in first-seen order; a repeated id would otherwise close its purchase twice.
	ids := []string{}
	for _, id := range a.IDs {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		if _, err := ops.Purchase(e.b, id, bought, e.now, e.loc); err != nil {
			return nil, err
		}
	}
	return e.many(model.TBuy, ids)
}

func runChoreDone(e env, raw json.RawMessage) (any, error) {
	var a struct {
		ID string `json:"id"`
	}
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	if a.ID == "" {
		return nil, errors.New("需要家务的编号")
	}
	ent, err := ops.ChoreDone(e.b, a.ID, e.now, e.loc)
	if err != nil {
		return nil, err
	}
	return e.one(ent.Type, ent.ID)
}

func runRemove(e env, raw json.RawMessage) (any, error) {
	var a struct {
		ID string `json:"id"`
	}
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	if a.ID == "" {
		return nil, errors.New("需要记录的编号")
	}
	ent := entityIn(space(e.b), a.ID)
	if ent == nil {
		return nil, fmt.Errorf("没有记录「%s」", a.ID)
	}
	if err := ops.Remove(e.b, a.ID, e.now); err != nil {
		return nil, err
	}
	return removeOut{Removed: a.ID, Type: ent.Type, Title: ent.Title}, nil
}
