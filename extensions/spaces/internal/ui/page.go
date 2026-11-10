// Package ui draws the spaces extension: the nav page, the overview card and the actions behind them. It reads and
// changes the base through kernel.Storage only. Every screen is a function of the page state and the stored base, so
// nothing is kept between requests.
package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/spaces/internal/kernel"
	"github.com/nwverse/persona-extensions/extensions/spaces/internal/templates"
)

// The contributions of the extension: the nav page and the overview card that opens it.
const (
	PageUI = "spaces"
	CardUI = "spaces-card"
)

// Notifier redraws a contribution on the hub. *ext.Hub satisfies it.
type Notifier interface {
	Invalidate(contribution string) error
}

// App draws the spaces contributions over one storage.
type App struct {
	st  kernel.Storage
	Now func() time.Time // the clock; replaced in tests
	hub Notifier
}

// New returns the app over st, with the real clock and no hub.
func New(st kernel.Storage) *App {
	return &App{st: st, Now: time.Now}
}

// SetHub sets the hub that is told when the overview card has to be drawn again.
func (a *App) SetHub(n Notifier) { a.hub = n }

// state is the page's own state, sent back with every request. The fields name what is shown: the space, the type
// tab, the view, the search text, the extra pages loaded, the entity page, the schema editor for a type and the one
// open fold.
type state struct {
	S string `json:"s,omitempty"` // space
	T string `json:"t,omitempty"` // type; "" = the space overview
	V int    `json:"v,omitempty"` // view index
	Q string `json:"q,omitempty"` // search text
	N int    `json:"n,omitempty"` // extra pages loaded
	E string `json:"e,omitempty"` // entity page
	D string `json:"d,omitempty"` // schema editor for this type ("+" = a new type)
	F string `json:"f,omitempty"` // the one open fold
}

// ctx is one request: its state, the time, the time zone and, once a base is read, the space and entity shown.
type ctx struct {
	app *App
	r   *ext.UIRequest
	st  state
	now time.Time
	loc *time.Location

	b  *kernel.Base   // the base being read or changed; valid only inside the storage call
	sp *kernel.Space  // the space shown; nil when there is none
	w  *kernel.World  // the index of sp
	e  *kernel.Entity // the entity shown on the entity page
}

// errKeep ends a storage update without saving: nothing changed, or the change was refused.
var errKeep = errors.New("keep")

// actions are the handlers of the actions, keyed by the part of the action id before ':'. The part after it is the
// argument. A handler returns nil to draw the page again from the state it changed, or a result; an error is refused
// and shown as a toast, and nothing is saved.
var actions = map[string]func(c *ctx, arg string) (*wire.ActionResult, error){
	"tab":    actTab,
	"view":   actView,
	"search": actSearch,
	"more":   actMore,
	"fold":   actFold,
	"space":  actSpace,
	"create": actCreate,
	"add":    actAdd,
	"save":   actSave,
	"drop":   actDrop,
	"back":   actBack,
	// The entity page.
	"status": actStatus,
	"update": actUpdate,
	"unlink": actUnlink,
	"derive": actDerive,
	"delete": actDelete,
	// The schema editor.
	"typenew":   actTypeNew,
	"typeset":   actTypeSet,
	"typedrop":  actTypeDrop,
	"stagesave": actStageSave,
	"stageup":   actStageUp,
	"stagedrop": actStageDrop,
	"propnew":   actPropNew,
	"propsave":  actPropSave,
	"propup":    actPropUp,
	"propdrop":  actPropDrop,
	"cardsave":  actCardSave,
	"viewsave":  actViewSave,
	"viewdrop":  actViewDrop,
}

// Page draws the nav page for the request.
func (a *App) Page(_ context.Context, r *ext.UIRequest) (*wire.View, error) {
	return a.render(newCtx(a, r))
}

// Card draws the overview card: the spaces in one glance, and the entities in progress.
func (a *App) Card(_ context.Context, r *ext.UIRequest) (*wire.View, error) {
	c := newCtx(a, r)
	var v *wire.View
	err := a.st.View(func(b *kernel.Base) error {
		c.b = b
		v = c.card()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return lower(v, r.UIVersion), nil
}

// Act answers an action on the nav page. A change is saved as one update; a refused change saves nothing and shows
// its message as a toast. The answer is the page drawn from the resulting state unless the handler gave a push or a
// pop.
func (a *App) Act(_ context.Context, r *ext.UIRequest) (*wire.ActionResult, error) {
	c := newCtx(a, r)
	key, arg, _ := strings.Cut(r.Action, ":")
	fn, ok := actions[key]
	if !ok {
		v, err := a.render(c)
		if err != nil {
			return nil, err
		}
		return &wire.ActionResult{View: v}, nil
	}
	var (
		res     *wire.ActionResult
		refused error
		changed bool
	)
	st0 := c.st
	err := a.st.Update(func(b *kernel.Base, _ time.Time) error {
		c.b = b
		c.resolve()
		before, err := json.Marshal(b)
		if err != nil {
			return err
		}
		res, refused = fn(c, arg)
		if refused != nil {
			return errKeep
		}
		after, err := json.Marshal(b)
		if err != nil {
			return err
		}
		if bytes.Equal(before, after) {
			return errKeep
		}
		changed = true
		return nil
	})
	switch {
	case refused != nil:
		c.st = st0
		res = &wire.ActionResult{Toast: refused.Error()}
	case err != nil && !errors.Is(err, errKeep):
		return nil, err
	}
	if changed && a.hub != nil {
		_ = a.hub.Invalidate(CardUI)
	}
	if res == nil {
		res = &wire.ActionResult{}
	}
	if res.View == nil && res.Push == nil && !res.Pop {
		v, err := a.render(c)
		if err != nil {
			return nil, err
		}
		res.View = v
	}
	return res, nil
}

// render draws the nav page for the state of c and returns it with that state.
func (a *App) render(c *ctx) (*wire.View, error) {
	var v *wire.View
	err := a.st.View(func(b *kernel.Base) error {
		c.b = b
		var err error
		v, err = c.route()
		return err
	})
	if err != nil {
		return nil, err
	}
	v.State = ext.EncodeState(c.st)
	return lower(v, c.r.UIVersion), nil
}

func newCtx(a *App, r *ext.UIRequest) *ctx {
	c := &ctx{app: a, r: r, now: a.clock(), loc: location(r.TZ)}
	var st state
	if r.DecodeState(&st) == nil {
		c.st = st
	}
	return c
}

// clock is the app's time.
func (a *App) clock() time.Time {
	if a.Now == nil {
		return time.Now()
	}
	return a.Now()
}

// location is the request's time zone; UTC when it is empty or unknown.
func location(name string) *time.Location {
	if name != "" {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
	}
	return time.UTC
}

// route chooses the screen from the state and the data: no space is the welcome; an entity is its page; a schema
// editor is shown for a type that exists or for a new one; else the space's overview or the type's tab. A stale id
// falls through to the next screen.
func (c *ctx) route() (*wire.View, error) {
	c.resolve()
	if c.e != nil {
		return c.entityPage()
	}
	if c.sp == nil {
		return c.welcome(), nil
	}
	if c.st.D != "" {
		if c.st.D == "+" || c.typeOf(c.st.D) != nil {
			return c.editorPage()
		}
		c.st.D = ""
	}
	if t := c.typeOf(c.st.T); t != nil {
		return c.typePage(t)
	}
	c.st.T, c.st.V, c.st.N = "", 0, 0
	return c.overview()
}

// resolve finds the entity and the space the state names. An entity id that is not in the base is dropped; the space
// is the one the state names, else the first one. The state is left naming the space that is shown.
func (c *ctx) resolve() {
	c.e = nil
	if c.st.E != "" {
		if sp, e, err := c.b.Find(c.st.E); err == nil {
			c.open(sp)
			c.e = e
			return
		}
		c.st.E = ""
	}
	c.open(c.pickSpace())
}

// open makes sp the space shown, with its index. A nil space shows none.
func (c *ctx) open(sp *kernel.Space) {
	c.sp, c.w = sp, nil
	if sp == nil {
		c.st.S = ""
		return
	}
	c.st.S = sp.ID
	c.w = kernel.Open(sp)
}

// pickSpace is the space the state names, else the first space; nil when there is none.
func (c *ctx) pickSpace() *kernel.Space {
	for _, sp := range c.b.Spaces {
		if sp != nil && sp.ID == c.st.S {
			return sp
		}
	}
	for _, sp := range c.b.Spaces {
		if sp != nil {
			return sp
		}
	}
	return nil
}

// typeOf is the type of the space shown with the key; nil when there is none.
func (c *ctx) typeOf(key string) *kernel.Type {
	if c.sp == nil || key == "" {
		return nil
	}
	return typeIn(c.sp, key)
}

// pick is the argument of an action when it has one, else the value the page sent under name.
func (c *ctx) pick(arg, name string) string {
	if arg != "" {
		return arg
	}
	return c.r.Values[name]
}

// ago is how long ago a moment was, in words, or its day for more than a day.
func (c *ctx) ago(t time.Time) string {
	d := c.now.Sub(t)
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d/time.Hour))
	}
	return t.In(c.loc).Format(time.DateOnly)
}

// Actions that navigate, and the handlers.

func actTab(c *ctx, arg string) (*wire.ActionResult, error) {
	key := c.pick(arg, "tab")
	if key == overTab {
		key = ""
	}
	if key != "" && c.typeOf(key) == nil {
		return nil, fmt.Errorf("没有类型「%s」", key)
	}
	c.st = state{S: c.st.S, T: key}
	return nil, nil
}

func actView(c *ctx, arg string) (*wire.ActionResult, error) {
	t := c.typeOf(c.st.T)
	i, err := strconv.Atoi(c.pick(arg, "view"))
	if t == nil || err != nil || i < 0 || i >= len(t.ViewsOrDefault()) {
		return nil, errors.New("没有这个视图")
	}
	c.st.V, c.st.N, c.st.F = i, 0, ""
	return nil, nil
}

func actSearch(c *ctx, _ string) (*wire.ActionResult, error) {
	c.st.Q = cut(strings.TrimSpace(c.r.Values["q"]), maxSearch)
	c.st.N = 0
	return nil, nil
}

func actMore(c *ctx, _ string) (*wire.ActionResult, error) {
	if c.st.N < maxMore {
		c.st.N++
	}
	return nil, nil
}

// actFold opens the fold named by the argument, or closes it when that fold is already open.
func actFold(c *ctx, arg string) (*wire.ActionResult, error) {
	if arg == c.st.F {
		arg = ""
	}
	c.st.F = arg
	return nil, nil
}

func actSpace(c *ctx, arg string) (*wire.ActionResult, error) {
	sp, err := c.b.Space(c.pick(arg, "space"))
	if err != nil {
		return nil, err
	}
	c.st = state{S: sp.ID}
	return nil, nil
}

// actCreate creates a space from a template, with its samples.
func actCreate(c *ctx, arg string) (*wire.ActionResult, error) {
	sp, err := templates.Apply(c.b, arg, "", true, c.now)
	if err != nil {
		return nil, err
	}
	c.st = state{S: sp.ID}
	return &wire.ActionResult{Toast: "已创建「" + sp.Name + "」"}, nil
}

// actAdd creates an entity of the type tab's type with the title of the quick-add fold. It starts at the first stage.
func actAdd(c *ctx, _ string) (*wire.ActionResult, error) {
	t := c.typeOf(c.st.T)
	if t == nil {
		return nil, errors.New("先选择一个类型")
	}
	title := strings.TrimSpace(c.r.Values["title"])
	if _, err := c.sp.PutEntity(c.b, kernel.EntityInput{Type: t.Key, Title: &title}, c.now); err != nil {
		return nil, err
	}
	c.st.F = ""
	return &wire.ActionResult{Toast: "已创建「" + title + "」"}, nil
}

// actSave changes the name and the about of the space shown, from its settings fold.
func actSave(c *ctx, _ string) (*wire.ActionResult, error) {
	if c.sp == nil {
		return nil, errors.New("还没有空间")
	}
	name := strings.TrimSpace(c.r.Values["name"])
	about := strings.TrimSpace(c.r.Values["about"])
	if _, err := c.b.PutSpace(kernel.SpaceInput{ID: c.sp.ID, Name: &name, About: &about}); err != nil {
		return nil, err
	}
	c.st.F = ""
	return &wire.ActionResult{Toast: "已保存"}, nil
}

// actDrop deletes the space shown, with everything in it. The state then shows the first space left, or the welcome.
func actDrop(c *ctx, _ string) (*wire.ActionResult, error) {
	if c.sp == nil {
		return nil, errors.New("还没有空间")
	}
	name := c.sp.Name
	if err := c.b.DropSpace(c.sp.ID); err != nil {
		return nil, err
	}
	c.st = state{}
	return &wire.ActionResult{Toast: "已删除「" + name + "」"}, nil
}

// actBack goes back a page: the page below is drawn again.
func actBack(_ *ctx, _ string) (*wire.ActionResult, error) {
	return &wire.ActionResult{Pop: true}, nil
}
