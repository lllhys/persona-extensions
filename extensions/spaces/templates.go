package main

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Starter templates: a few common shapes a new space can start from instead of an empty schema. A template only
// fills in a space's types (and, when asked, a handful of example entities); afterwards the space is the user's like
// any other, and every type in it can be changed or removed.

type Template struct {
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Icon        string    `json:"icon"`
	Description string    `json:"description"`
	Types       []TypeDef `json:"types"`
	examples    []example
}

// example is an entity a template can bring along; links name the examples (by title) it points to.
type example struct {
	typ, title, status string
	values             map[string]any
	links              [][2]string // relation, title
}

func fText(key, label string) FieldDef   { return FieldDef{Key: key, Label: label, Kind: "text"} }
func fNumber(key, label string) FieldDef { return FieldDef{Key: key, Label: label, Kind: "number"} }
func fURL(key, label string) FieldDef    { return FieldDef{Key: key, Label: label, Kind: "url"} }
func fDate(key, label string) FieldDef   { return FieldDef{Key: key, Label: label, Kind: "date"} }
func fEnum(key, label string, o ...string) FieldDef {
	return FieldDef{Key: key, Label: label, Kind: "enum", Options: o}
}

var templates = []Template{
	{
		Key: "project", Name: "项目与目标", Icon: "star", Description: "点子慢慢孵化成目标，资料和动态都放在一起。",
		Types: []TypeDef{
			{Key: "ideas", Label: "点子", View: "board", Statuses: []string{"收集", "探索", "孵化", "归档"},
				Fields: []FieldDef{fText("summary", "简介"), fText("tags", "标签")}},
			{Key: "goals", Label: "目标", View: "progress", Statuses: []string{"规划中", "进行中", "已达成"},
				Fields: []FieldDef{fNumber("current", "当前值"), fNumber("target", "目标值"), fText("unit", "单位"), fDate("due", "截止日期")}},
			{Key: "resources", Label: "资料库", View: "list",
				Fields: []FieldDef{fEnum("category", "分类", "文章", "工具", "课程", "文档", "其他"), fURL("url", "链接"), fText("note", "备注")}},
			{Key: "log", Label: "动态", View: "feed",
				Fields: []FieldDef{fText("summary", "摘要"), fText("source", "来源")}},
		},
		examples: []example{
			{typ: "ideas", title: "每周写一篇复盘", status: "孵化", values: map[string]any{"summary": "把一周的进展和卡点写下来，方便回头看。"}},
			{typ: "goals", title: "完成 12 篇周复盘", status: "进行中", values: map[string]any{"current": 3.0, "target": 12.0, "unit": "篇"},
				links: [][2]string{{"源自", "每周写一篇复盘"}}},
			{typ: "resources", title: "复盘的写法", values: map[string]any{"category": "文章", "note": "先写事实，再写判断。"}},
			{typ: "log", title: "开了这个空间", values: map[string]any{"summary": "从「项目与目标」模板开始。"}},
		},
	},
	{
		Key: "reading", Name: "阅读与知识", Icon: "doc", Description: "书单、读书笔记和随手存下的摘录与链接。",
		Types: []TypeDef{
			{Key: "books", Label: "书单", View: "progress", Statuses: []string{"想读", "在读", "读完"},
				Fields: []FieldDef{fNumber("read", "已读页"), fNumber("pages", "总页数"), fText("author", "作者"), fEnum("rating", "评分", "★", "★★", "★★★", "★★★★", "★★★★★")}},
			{Key: "notes", Label: "笔记", View: "list",
				Fields: []FieldDef{fText("excerpt", "摘录"), fText("thought", "感悟"), fNumber("page", "页码")}},
			{Key: "clips", Label: "摘录与链接", View: "table",
				Fields: []FieldDef{fText("source", "来源"), fURL("url", "链接"), fText("tags", "标签")}},
		},
		examples: []example{
			{typ: "books", title: "一本在读的书", status: "在读", values: map[string]any{"read": 80.0, "pages": 320.0}},
			{typ: "notes", title: "第一条笔记", values: map[string]any{"thought": "读到有感触的地方，记一句自己的话。", "page": 42.0},
				links: [][2]string{{"出自", "一本在读的书"}}},
		},
	},
	{
		Key: "inventory", Name: "清单与物品", Icon: "list", Description: "家里有什么、放在哪、快用完的，还有想买的。",
		Types: []TypeDef{
			{Key: "items", Label: "物品", View: "table", Statuses: []string{"充足", "待补", "已空"},
				Fields: []FieldDef{fEnum("category", "分类", "食品", "日用", "药品", "数码", "其他"), fText("location", "位置"), fNumber("quantity", "数量"), fDate("expires", "保质期")}},
			{Key: "wishes", Label: "心愿单", View: "board", Statuses: []string{"想要", "观望", "已入"},
				Fields: []FieldDef{fNumber("price", "价格"), fURL("url", "链接"), fEnum("priority", "优先级", "高", "中", "低")}},
		},
		examples: []example{
			{typ: "items", title: "纸巾", status: "待补", values: map[string]any{"category": "日用", "location": "储物柜", "quantity": 1.0}},
			{typ: "wishes", title: "一盏台灯", status: "观望", values: map[string]any{"priority": "中"}},
		},
	},
	{Key: "blank", Name: "空白空间", Icon: "folder", Description: "", Types: []TypeDef{}},
}

func templateKeys() []string {
	keys := make([]string, len(templates))
	for i, t := range templates {
		keys[i] = t.Key
	}
	return keys
}

func findTemplate(key string) (*Template, error) {
	for i := range templates {
		if templates[i].Key == key {
			return &templates[i], nil
		}
	}
	return nil, fmt.Errorf("没有模板 %q（可选：%s）", key, strings.Join(templateKeys(), "、"))
}

// types are a fresh copy of the template's types, held to the rules every type is.
func (t *Template) types() ([]TypeDef, error) {
	out := make([]TypeDef, len(t.Types))
	for i, d := range t.Types {
		d.Fields = slices.Clone(d.Fields)
		for j := range d.Fields {
			d.Fields[j].Options = slices.Clone(d.Fields[j].Options)
		}
		d.Statuses = slices.Clone(d.Statuses)
		if err := d.check(); err != nil {
			return nil, fmt.Errorf("模板 %s 的类型 %s：%v", t.Key, d.Key, err)
		}
		out[i] = d
	}
	return out, nil
}

// seed writes the template's examples into a space made from it.
func (t *Template) seed(st *State, space string, now time.Time) error {
	ids := map[string]string{}
	for i, x := range t.examples {
		in := EntityInput{Space: space, Type: x.typ, Title: &x.title, Status: x.status, Values: x.values}
		for _, l := range x.links {
			in.Links = append(in.Links, Link{Type: l[0], To: ids[l[1]]})
		}
		// one second apart, the first oldest, so a feed and "recently changed" read in the order they are written
		e, err := st.upsertEntity(in, now.Add(time.Duration(i-len(t.examples))*time.Second))
		if err != nil {
			return fmt.Errorf("模板 %s 的示例「%s」：%v", t.Key, x.title, err)
		}
		ids[x.title] = e.ID
	}
	return nil
}

// ---- Derive: a new entity of another type from one there is ----

// deriveEntity makes an entity of another type of the same space from src: the title (or the one given), the
// values whose field the new type has too and which fit it, and a relation from the new entity back to src.
func (st *State) deriveEntity(srcID, typ, title, rel string, now time.Time) (*Entity, error) {
	src := st.entity(srcID)
	if src == nil {
		return nil, fmt.Errorf("没有记录 %q", srcID)
	}
	sp, _ := st.space(src.Space)
	t := sp.typeDef(typ)
	if t == nil {
		return nil, fmt.Errorf("空间「%s」没有类型 %q", sp.Name, typ)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = src.Title
	}
	rel = strings.TrimSpace(rel)
	if rel == "" {
		rel = "源自"
	}
	vals := map[string]any{}
	for k, v := range src.Values {
		if f := t.field(k); f != nil {
			if n, err := f.normalize(plain(v)); err == nil {
				vals[k] = n
			}
		}
	}
	return st.upsertEntity(EntityInput{Space: sp.ID, Type: t.Key, Title: &title, Values: vals,
		Links: []Link{{Type: rel, To: src.ID}}}, now)
}
