package ui

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
)

// The entity page's limits and labels. A segmented control holds at most segmentMost choices; past that the stages are
// buttons. Links are listed at most linksMost to a section; facts at most factMost. A short value is a fact, a longer
// text is a text of its own, both at most longLead runes for a fact.
const (
	segmentMost = 5
	linksMost   = 100
	factMost    = 8
	badgeMost   = 4
	addLabel    = "添加…" // the first choice of a relation with many targets: adds the one chosen
	noLinks     = "没有关联"
	noBacklinks = "没有被关联"
)

// entityPage is the entity page of c.e, pushed over the page below: a header with the badges and facts of the entity,
// its progress and its texts, the lifecycle of a type with a flow, the links out and the links in (both always shown),
// the one open fold, and the buttons that edit, derive from or delete the entity.
func (c *ctx) entityPage() (*wire.View, error) {
	e, t, err := c.shown()
	if err != nil {
		return nil, err
	}
	v := &wire.View{Title: cut(e.Title, maxTitle), Sections: []wire.Section{{Items: c.entityHead(t, e)}}}
	if len(t.Flow) > 0 {
		v.Sections = append(v.Sections, c.lifecycle(t, e))
	}
	v.Sections = append(v.Sections, c.outLinks(t, e), c.inLinks(e))
	switch c.st.F {
	case "edit":
		v.Sections = append(v.Sections, c.editFold(t, e))
	case "derive":
		v.Sections = append(v.Sections, c.deriveFold(e))
	}
	v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{
		button("编辑", &wire.Action{ID: "fold:edit"}, wire.ButtonRoleNormal),
		button("衍生", &wire.Action{ID: "fold:derive"}, wire.ButtonRoleNormal),
		button("删除", &wire.Action{ID: "delete", Confirm: c.deleteConfirm(e)}, wire.ButtonRoleDestructive),
	}})
	return v, nil
}

// shown is the entity the page shows and its type. A stale entity refuses the change; a type missing from the space
// is a base that the kernel would not have stored.
func (c *ctx) shown() (*kernel.Entity, *kernel.Type, error) {
	if c.e == nil {
		return nil, nil, errors.New("没有这条记录")
	}
	t := typeIn(c.sp, c.e.Type)
	if t == nil {
		return nil, nil, fmt.Errorf("记录「%s」的类型「%s」不存在", c.e.Title, c.e.Type)
	}
	return c.e, t, nil
}

// entityHead is the first section: the header (title, subtitle, the status and up to three enum or boolean pills as
// badges, the scalar values as facts), then the progress, a link for each https address and a text for each long text.
func (c *ctx) entityHead(t *kernel.Type, e *kernel.Entity) []wire.Component {
	header := wire.Component{
		Type: wire.ComponentHeader, Title: cut(e.Title, maxTitle),
		Subtitle: cut(t.Name+" · "+c.ago(e.Updated), maxText),
	}
	if e.Status != "" {
		header.Badges = append(header.Badges, wire.Badge{Text: e.Status, Tone: toneOf(t, e.Status)})
	}
	badged := map[string]bool{}
	for _, key := range t.CardOrDefault().Pills {
		p, ok := c.sp.Field(t, key)
		k, known := kernel.KindOf(p.Kind)
		if !ok || !known || (k.Input != kernel.InChoice && k.Input != kernel.InToggle) {
			continue
		}
		text := c.w.Text(e, key)
		if text == "" || len(header.Badges) >= badgeMost {
			continue
		}
		header.Badges = append(header.Badges, wire.Badge{Text: text})
		badged[key] = true
	}
	// The lead reads as a paragraph under the header, and the progress bar already shows its own values.
	card := t.CardOrDefault()
	shown := map[string]bool{card.Lead: true}
	if card.Progress != nil {
		shown[card.Progress.Of], shown[card.Progress.Over] = true, true
	}
	var links, texts []wire.Component
	if lead := c.lead(t, e); lead != "" {
		texts = append(texts, wire.Component{Type: wire.ComponentText, Text: lead, Style: wire.TextStyleBody})
	}
	for i := range t.Props {
		p := t.Props[i]
		k, known := kernel.KindOf(p.Kind)
		raw := c.w.Value(e, p.Key)
		if !known || raw == nil || badged[p.Key] || p.Many || shown[p.Key] {
			continue
		}
		text := c.w.Text(e, p.Key)
		if text == "" {
			continue
		}
		if s, ok := raw.(string); ok && isURI(k, &p) && httpsURL(s) {
			links = append(links, wire.Component{Type: wire.ComponentLink, Title: p.Name, URL: s})
		}
		if utf8.RuneCountInString(text) > longLead {
			texts = append(texts, wire.Component{Type: wire.ComponentText, Text: cut(text, maxText), Style: wire.TextStyleBody})
			continue
		}
		if len(header.Facts) < factMost {
			header.Facts = append(header.Facts, wire.Fact{Label: p.Name, Value: text})
		}
	}
	out := []wire.Component{header}
	if prog, ok := c.progress(t, e); ok {
		out = append(out, prog)
	}
	out = append(out, links...)
	return append(out, texts...)
}

// isURI tells a property whose schema is a URI; the kind's own schema says so, as it does for a date.
func isURI(k *kernel.Kind, p *kernel.Prop) bool {
	f, _ := k.Schema(p)["format"].(string)
	return f == "uri"
}

// httpsURL tells a stored address the apps open: an https link with a host and no user part.
func httpsURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil
}

// lifecycle is the section of a type with a flow: a segmented control of the current stage and the stages the entity may
// move to, in flow order. Past segmentMost choices it is a row of buttons instead, the advance first.
func (c *ctx) lifecycle(t *kernel.Type, e *kernel.Entity) wire.Section {
	moves := t.Moves(e.Status)
	if 1+len(moves) > segmentMost {
		return wire.Section{Title: "生命周期", Items: moveButtons(t, e, moves)}
	}
	choices := []wire.Choice{}
	for _, st := range t.Flow {
		if st.Name == e.Status || slices.Contains(moves, st.Name) {
			choices = append(choices, wire.Choice{Value: st.Name, Title: st.Name})
		}
	}
	return wire.Section{Title: "生命周期", Items: []wire.Component{{
		Type: wire.ComponentSegmented, Name: "stage", Value: e.Status, Choices: choices,
		Action: &wire.Action{ID: "status"},
	}}}
}

// moveButtons is the lifecycle as buttons: the advance first when there is one, then the other moves in flow order.
func moveButtons(t *kernel.Type, e *kernel.Entity, moves []string) []wire.Component {
	items := []wire.Component{}
	next, hasNext := t.Next(e.Status)
	if hasNext {
		items = append(items, button("→ "+next, &wire.Action{ID: "status:" + next}, wire.ButtonRoleNormal))
	}
	for _, m := range moves {
		if hasNext && m == next {
			continue
		}
		items = append(items, button(m, &wire.Action{ID: "status:" + m}, wire.ButtonRoleNormal))
	}
	return items
}

// outLinks is the section of the links out of the entity: a row per target, subtitled with the relation's name (the
// origin reads 源自). While the edit fold is open, the targets of a relation with many are removed from here.
func (c *ctx) outLinks(t *kernel.Type, e *kernel.Entity) wire.Section {
	editing := c.st.F == "edit"
	var items []wire.Component
	for _, l := range c.w.Out(e) {
		target := c.w.Entity(l.To)
		if target == nil {
			continue
		}
		p, _ := c.sp.Field(t, l.Prop)
		row := c.linkRow(target, p.Name)
		if editing && p.Many {
			row.Value = "移除"
			row.Action = &wire.Action{ID: "unlink:" + l.Prop + ":" + l.To}
		}
		items = append(items, row)
	}
	return wire.Section{Title: "关联 →", Items: orEmpty(capped(items), noLinks)}
}

// inLinks is the section of the links into the entity: a row per source, subtitled with the relation's back label (the
// derived entities read 衍生).
func (c *ctx) inLinks(e *kernel.Entity) wire.Section {
	var items []wire.Component
	for _, l := range c.w.In(e) {
		src := c.w.Entity(l.From)
		if src == nil {
			continue
		}
		items = append(items, c.linkRow(src, c.backLabel(src, l.Prop)))
	}
	return wire.Section{Title: "← 被关联", Items: orEmpty(capped(items), noBacklinks)}
}

// linkRow is the row of a linked entity: its title, the label of the link, its toned status and its type's icon; it
// opens the entity.
func (c *ctx) linkRow(target *kernel.Entity, label string) wire.Component {
	row := wire.Component{
		Type: wire.ComponentRow, Title: cut(target.Title, maxTitle), Subtitle: label,
		Action: ext.Open("open:"+target.ID, PageUI, state{S: c.sp.ID, E: target.ID}, cut(target.Title, maxTitle)),
	}
	if t := typeIn(c.sp, target.Type); t != nil {
		row.Icon = iconName(t.Icon)
		if target.Status != "" {
			row.Badge = &wire.Badge{Text: target.Status, Tone: toneOf(t, target.Status)}
		}
	}
	return row
}

// backLabel names a backlink: the relation's back label, else the source's type and the relation. A derivation reads
// 衍生.
func (c *ctx) backLabel(src *kernel.Entity, prop string) string {
	if prop == "origin" {
		return "衍生"
	}
	st := typeIn(c.sp, src.Type)
	if st == nil {
		return prop
	}
	p, _ := c.sp.Field(st, prop)
	if p.Back != "" {
		return p.Back
	}
	return st.Name + " · " + p.Name
}

// capped keeps the first linksMost rows of a list of links and adds a caption for the rest.
func capped(items []wire.Component) []wire.Component {
	if len(items) <= linksMost {
		return items
	}
	rest := wire.Component{
		Type: wire.ComponentText, Text: fmt.Sprintf("还有 %d 条关联没有显示", len(items)-linksMost), Style: wire.TextStyleCaption,
	}
	return append(items[:linksMost:linksMost], rest)
}

// orEmpty is the rows of a section, or one quiet line that says there are none.
func orEmpty(items []wire.Component, title string) []wire.Component {
	if len(items) == 0 {
		return []wire.Component{{Type: wire.ComponentText, Text: title, Style: wire.TextStyleSecondary}}
	}
	return items
}

// deleteConfirm asks before a delete, and says how many relations to other entities the delete removes.
func (c *ctx) deleteConfirm(e *kernel.Entity) string {
	n := 0
	for _, l := range c.w.In(e) {
		if l.Prop != "origin" {
			n++
		}
	}
	if n == 0 {
		return fmt.Sprintf("删除「%s」？", cut(e.Title, maxTitle))
	}
	return fmt.Sprintf("删除「%s」？有 %d 条关联会被解除。", cut(e.Title, maxTitle), n)
}

// editFold is the edit fold: the title, then one field for each stored property, built from the descriptor, then 保存
// and 取消. A relation with many targets offers 添加…, which adds the target chosen to the ones it has.
func (c *ctx) editFold(t *kernel.Type, e *kernel.Entity) wire.Section {
	items := []wire.Component{{Type: wire.ComponentField, Name: "title", Label: "标题", Kind: wire.FieldKindText, Value: e.Title}}
	for i := range t.Props {
		p := t.Props[i]
		name := "p_" + p.Key
		var f wire.Component
		var ok bool
		if p.Many {
			f, ok = c.addField(p, name, e)
		} else {
			f, ok = c.field(p, name, e.Values[p.Key])
		}
		if ok {
			items = append(items, f)
		}
	}
	items = append(items,
		button("保存", &wire.Action{ID: "update", Submit: true}, wire.ButtonRoleNormal),
		button("取消", &wire.Action{ID: "fold:edit"}, wire.ButtonRoleNormal),
	)
	return wire.Section{Items: items}
}

// addField is the field of a relation with many targets. It offers the newest targets the entity does not have yet,
// and its unset choice reads 添加…: submitting a target adds it to the list.
func (c *ctx) addField(p kernel.Prop, name string, e *kernel.Entity) (wire.Component, bool) {
	f, ok := c.field(p, name, nil)
	if !ok {
		return f, false
	}
	k, _ := kernel.KindOf(p.Kind)
	held := map[string]bool{}
	for _, id := range k.Refs(e.Values[p.Key]) {
		held[choicePrefix+id] = true
	}
	choices := []wire.Choice{{Value: choicePrefix, Title: addLabel}}
	for _, ch := range f.Choices[1:] {
		if !held[ch.Value] {
			choices = append(choices, ch)
		}
	}
	f.Value = choicePrefix
	f.Choices = choices
	return f, true
}

// deriveFold is the derive fold: the type the copy is made into (the first type that is not the entity's own, by
// default), its title, and 衍生.
func (c *ctx) deriveFold(e *kernel.Entity) wire.Section {
	choices := []wire.Choice{}
	for _, ty := range c.sp.Types {
		if ty != nil {
			choices = append(choices, wire.Choice{Value: choicePrefix + ty.Key, Title: ty.Name})
		}
	}
	def := choices[0].Value
	for _, ch := range choices {
		if ch.Value != choicePrefix+e.Type {
			def = ch.Value
			break
		}
	}
	return wire.Section{Items: []wire.Component{
		{Type: wire.ComponentField, Name: "type", Label: "类型", Kind: wire.FieldKindChoice, Value: def, Choices: choices},
		{Type: wire.ComponentField, Name: "title", Label: "标题", Kind: wire.FieldKindText, Placeholder: cut(e.Title, maxTitle)},
		button("衍生", &wire.Action{ID: "derive", Submit: true}, wire.ButtonRoleNormal),
	}}
}

// actStatus moves the entity to the stage named by the argument, or by the stage control's value. A move the flow does
// not allow is refused with the flow's sentence, and nothing changes.
func actStatus(c *ctx, arg string) (*wire.ActionResult, error) {
	e, _, err := c.shown()
	if err != nil {
		return nil, err
	}
	stage := c.pick(arg, "stage")
	if stage == "" {
		return nil, errors.New("没有选择阶段")
	}
	if _, err := c.sp.PutEntity(c.b, kernel.EntityInput{ID: e.ID, Status: &stage}, c.now); err != nil {
		return nil, err
	}
	return nil, nil
}

// actUpdate saves the edit fold: the title and every stored property. Relations with many targets add the target chosen;
// an unset toggle that was never set stays unset.
func actUpdate(c *ctx, _ string) (*wire.ActionResult, error) {
	e, t, err := c.shown()
	if err != nil {
		return nil, err
	}
	in := kernel.EntityInput{ID: e.ID}
	if sent, ok := c.r.Values["title"]; ok {
		title := strings.TrimSpace(sent)
		if title == "" {
			return nil, errors.New("记录需要一个标题")
		}
		in.Title = &title
	}
	vals := map[string]any{}
	for i := range t.Props {
		p := t.Props[i]
		k, ok := kernel.KindOf(p.Kind)
		raw, sent := c.r.Values["p_"+p.Key]
		if !ok || !sent || k.Input == kernel.InNone {
			continue
		}
		v, err := submitted(p, raw)
		if err != nil {
			return nil, err
		}
		cur := e.Values[p.Key]
		switch {
		case p.Many:
			if v != nil {
				vals[p.Key] = addIDs(k.Refs(cur), k.Refs(v))
			}
		case k.Input == kernel.InToggle && cur == nil && v == false:
			continue
		default:
			vals[p.Key] = v
		}
	}
	in.Values = vals
	if _, err := c.sp.PutEntity(c.b, in, c.now); err != nil {
		return nil, err
	}
	c.st.F = ""
	return &wire.ActionResult{Toast: "已保存"}, nil
}

// addIDs is cur with the ids of add that it does not hold yet, appended in order.
func addIDs(cur, add []string) []string {
	out := slices.Clone(cur)
	for _, id := range add {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// actDerive makes an entity of the chosen type from the one shown, and opens it. The source does not change.
func actDerive(c *ctx, _ string) (*wire.ActionResult, error) {
	e, _, err := c.shown()
	if err != nil {
		return nil, err
	}
	typ := strings.TrimPrefix(c.r.Values["type"], choicePrefix)
	if typ == "" {
		return nil, errors.New("先选择一个类型")
	}
	title := strings.TrimSpace(c.r.Values["title"])
	ne, err := c.sp.Derive(c.b, e.ID, typ, title, c.now)
	if err != nil {
		return nil, err
	}
	c.st.F = ""
	return &wire.ActionResult{
		Toast: "已衍生「" + cut(ne.Title, maxTitle) + "」",
		Push:  ext.Open("open:"+ne.ID, PageUI, state{S: c.sp.ID, E: ne.ID}, cut(ne.Title, maxTitle)).Push,
	}, nil
}

// actUnlink removes one target from a relation with many targets. The argument is the property key and the target id.
func actUnlink(c *ctx, arg string) (*wire.ActionResult, error) {
	e, t, err := c.shown()
	if err != nil {
		return nil, err
	}
	key, id, _ := strings.Cut(arg, ":")
	p, ok := propOf(t, key)
	if !ok {
		return nil, errors.New("没有这个关联")
	}
	k, known := kernel.KindOf(p.Kind)
	if !known || k.Refs == nil || !slices.Contains(k.Refs(e.Values[key]), id) {
		return nil, errors.New("没有这个关联")
	}
	rest := slices.DeleteFunc(slices.Clone(k.Refs(e.Values[key])), func(x string) bool { return x == id })
	vals := map[string]any{key: nil}
	if len(rest) > 0 {
		vals[key] = rest
	}
	if _, err := c.sp.PutEntity(c.b, kernel.EntityInput{ID: e.ID, Values: vals}, c.now); err != nil {
		return nil, err
	}
	return nil, nil
}

// propOf is the declared property of a type with the key; false for a built-in field or an unknown key.
func propOf(t *kernel.Type, key string) (kernel.Prop, bool) {
	for _, p := range t.Props {
		if p.Key == key {
			return p, true
		}
	}
	return kernel.Prop{}, false
}

// actDelete deletes the entity shown and leaves its page: the page below is drawn again.
func actDelete(c *ctx, _ string) (*wire.ActionResult, error) {
	e, _, err := c.shown()
	if err != nil {
		return nil, err
	}
	title := cut(e.Title, maxTitle)
	if _, err := c.sp.Drop(e.ID); err != nil {
		return nil, err
	}
	c.st = state{S: c.sp.ID}
	return &wire.ActionResult{Toast: "已删除「" + title + "」", Pop: true}, nil
}
