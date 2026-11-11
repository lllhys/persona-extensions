// Package ui draws the household extension: the 家庭管家 nav page with its tabs, the overview card, and the forms and
// actions behind them. It reads and changes the records through kernel.Storage only, and every write goes through the
// ops package inside one Storage.Update. Every screen is a function of the page state and the stored records.
package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
)

// The contributions of the extension: the nav page and the overview card that opens it.
const (
	PageUI = "household"
	CardUI = "household-card"
)

// App draws the household contributions over one storage.
type App struct {
	st      kernel.Storage
	loc     func() *time.Location
	changed func()
	now     func() time.Time // the clock; replaced in tests
}

// New returns the app over st. loc gives the time zone the days are counted in (time.Local when nil). changed is
// called after every action that wrote; it may be nil.
func New(st kernel.Storage, loc func() *time.Location, changed func()) *App {
	return &App{st: st, loc: loc, changed: changed, now: time.Now}
}

// Page is the nav page 家庭管家.
func (a *App) Page() ext.Page {
	return ext.Page{Render: a.renderPage, Action: a.act}
}

// Card is the overview card: the counts and the most urgent records, each opening its form.
func (a *App) Card() ext.Page {
	return ext.Page{Render: a.renderCard}
}

// state is the page's own state, sent back with every request. T is the tab ("" = the overview), Q the search text,
// G the category the stock tab is filtered by, and F the form page: "new:<type>" or "edit:<id>".
type state struct {
	T string `json:"t,omitempty"`
	Q string `json:"q,omitempty"`
	G string `json:"g,omitempty"`
	F string `json:"f,omitempty"`
}

// ctx is one request: its state, the time and time zone, and the base read or changed, with the household space and
// its records as they are in that base.
type ctx struct {
	app   *App
	r     *ext.UIRequest
	st    state
	now   time.Time
	loc   *time.Location
	b     *kernel.Base
	sp    *kernel.Space // the household space; nil before its first write
	home  *model.Home   // its records, typed
	today model.Day
}

// errKeep ends a storage update without saving: nothing changed.
var errKeep = errors.New("keep")

// actions are the handlers of the actions, keyed by the part of the action id before ':'; the part after it is the
// argument. A handler returns nil to draw the page again, or a result. An error is refused: it becomes a toast, and
// nothing is saved.
var actions = map[string]func(c *ctx, arg string) (*wire.ActionResult, error){
	"tab":        actTab,
	"search":     actSearch,
	"filter":     actFilter,
	"back":       actBack,
	"add":        actAdd,
	"edit":       actEdit,
	"save":       actSave,
	"delete":     actDelete,
	"load":       actLoad,
	"restock":    actRestock,
	"buy":        actBuy,
	"skip":       actSkip,
	"chore":      actChore,
	"use":        actUse,
	"plan":       actPlan,
	"mealdone":   actMealDone,
	"mealcancel": actMealCancel,
}

// renderPage draws the nav page for the request.
func (a *App) renderPage(_ context.Context, r *ext.UIRequest) (*wire.View, error) {
	return a.draw(a.newCtx(r))
}

// renderCard draws the overview card.
func (a *App) renderCard(_ context.Context, r *ext.UIRequest) (*wire.View, error) {
	c := a.newCtx(r)
	var v *wire.View
	err := a.st.View(func(b *kernel.Base) error {
		c.bind(b)
		v = c.card()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return v, nil
}

// act answers an action on the nav page. A successful write is saved as one update and the changed hook is called. A
// refused one saves nothing: its error is the toast, and the page is drawn again in the state it had.
func (a *App) act(_ context.Context, r *ext.UIRequest) (*wire.ActionResult, error) {
	c := a.newCtx(r)
	key, arg, _ := strings.Cut(r.Action, ":")
	fn, ok := actions[key]
	if !ok {
		v, err := a.draw(c)
		if err != nil {
			return nil, err
		}
		return &wire.ActionResult{View: v}, nil
	}
	st0 := c.st
	var res *wire.ActionResult
	wrote := false
	err := a.st.Update(func(b *kernel.Base, _ time.Time) error {
		c.bind(b)
		before, err := json.Marshal(b)
		if err != nil {
			return err
		}
		res, err = fn(c, arg)
		if err != nil {
			return err
		}
		after, err := json.Marshal(b)
		if err != nil {
			return err
		}
		if bytes.Equal(before, after) {
			return errKeep
		}
		wrote = true
		return nil
	})
	if err != nil && !errors.Is(err, errKeep) {
		c.st = st0
		res = &wire.ActionResult{Toast: err.Error()}
	}
	if wrote && a.changed != nil {
		a.changed()
	}
	if res == nil {
		res = &wire.ActionResult{}
	}
	if res.View == nil && res.Push == nil && !res.Pop {
		v, err := a.draw(c)
		if err != nil {
			return nil, err
		}
		res.View = v
	}
	return res, nil
}

// draw reads the base and draws the screen the state names, with the state it ends in.
func (a *App) draw(c *ctx) (*wire.View, error) {
	var v *wire.View
	err := a.st.View(func(b *kernel.Base) error {
		c.bind(b)
		v = c.route()
		return nil
	})
	if err != nil {
		return nil, err
	}
	v.State = ext.EncodeState(c.st)
	return v, nil
}

// route is the form page when the state names one that exists, else the nav page.
func (c *ctx) route() *wire.View {
	if c.st.F != "" {
		if v := c.formPage(); v != nil {
			return v
		}
		c.st.F = ""
	}
	return c.page()
}

// newCtx is the request's context: its state, the clock and the time zone.
func (a *App) newCtx(r *ext.UIRequest) *ctx {
	c := &ctx{app: a, r: r, now: a.clock(), loc: a.location()}
	var st state
	if r.DecodeState(&st) == nil {
		c.st = st
	}
	return c
}

// bind makes b the base of the request and reads its household records.
func (c *ctx) bind(b *kernel.Base) {
	c.b = b
	c.sp = householdSpace(b)
	c.home = model.Load(c.sp, c.loc)
	c.today = model.DayOf(c.now, c.loc)
}

// clock is the app's time.
func (a *App) clock() time.Time {
	if a.now == nil {
		return time.Now()
	}
	return a.now()
}

// location is the time zone of the records' days: time.Local when none is given.
func (a *App) location() *time.Location {
	if a.loc != nil {
		if loc := a.loc(); loc != nil {
			return loc
		}
	}
	return time.Local
}

// pick is the argument of an action when it has one, else the value the page sent under name.
func (c *ctx) pick(arg, name string) string {
	if arg != "" {
		return arg
	}
	return c.r.Values[name]
}

// record is the record of the household space with the id; false when there is none, or it is another space's.
func (c *ctx) record(id string) (*kernel.Entity, bool) {
	sp, e, err := c.b.Find(id)
	if err != nil || sp.ID != model.SpaceID {
		return nil, false
	}
	return e, true
}

// Action handlers that only change the page state.

func actSearch(c *ctx, _ string) (*wire.ActionResult, error) {
	c.st.Q = cut(strings.TrimSpace(c.r.Values["q"]), maxSearch)
	return nil, nil
}

func actFilter(c *ctx, arg string) (*wire.ActionResult, error) {
	c.st.G = strings.TrimPrefix(c.pick(arg, "category"), choicePrefix)
	return nil, nil
}

// actBack goes back a page: the page below is drawn again.
func actBack(_ *ctx, _ string) (*wire.ActionResult, error) {
	return &wire.ActionResult{Pop: true}, nil
}
