package mcp

import (
	"encoding/json"
	"fmt"

	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/budget"
	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/store"
	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/usage"
)

// daysOf maps a range name to its number of days.
func daysOf(name string) (int, error) {
	switch name {
	case "today":
		return 1, nil
	case "7d":
		return 7, nil
	case "30d":
		return 30, nil
	default:
		return 0, fmt.Errorf("range 只能是 today、7d 或 30d")
	}
}

// summary answers usage_summary.
func (s *server) summary(args json.RawMessage) (any, error) {
	var p struct {
		Range   string `json:"range"`
		GroupBy string `json:"group_by"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("参数不是合法的 JSON")
	}
	days, err := daysOf(p.Range)
	if err != nil {
		return nil, err
	}
	var r usage.Range
	if err := s.st.View(func(st *store.State) error {
		r = usage.Summarize(st, s.now(), days, s.loc, usage.Filter{})
		return nil
	}); err != nil {
		return nil, err
	}
	out := map[string]any{"range": p.Range, "total": r.Total, "before": r.Before}
	want := func(dim string) bool { return p.GroupBy == "" || p.GroupBy == dim }
	switch p.GroupBy {
	case "", "model", "machine", "launcher", "day":
	default:
		return nil, fmt.Errorf("group_by 只能是 model、machine、launcher 或 day")
	}
	if want("model") {
		out["by_model"] = r.Models
	}
	if want("machine") {
		out["by_machine"] = r.Machines
	}
	if want("launcher") {
		out["by_launcher"] = r.Launchers
	}
	if want("day") {
		out["by_day"] = r.Trend
	}
	return out, nil
}

// top answers usage_top.
func (s *server) top(args json.RawMessage) (any, error) {
	var p struct {
		Range string `json:"range"`
		Dim   string `json:"dim"`
		N     int    `json:"n"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("参数不是合法的 JSON")
	}
	days, err := daysOf(p.Range)
	if err != nil {
		return nil, err
	}
	if p.N <= 0 {
		p.N = 8
	}
	if p.N > 50 {
		p.N = 50
	}
	var shares []usage.Share
	if err := s.st.View(func(st *store.State) error {
		r := usage.Summarize(st, s.now(), days, s.loc, usage.Filter{})
		switch p.Dim {
		case "model":
			shares = r.Models
		case "machine":
			shares = r.Machines
		case "launcher":
			shares = r.Launchers
		default:
			return fmt.Errorf("dim 只能是 model、machine 或 launcher")
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if len(shares) > p.N {
		shares = shares[:p.N]
	}
	return map[string]any{"range": p.Range, "dim": p.Dim, "top": shares}, nil
}

// status answers budget_status.
func (s *server) status() (any, error) {
	var out []budget.Status
	err := s.st.View(func(st *store.State) error {
		var err error
		out, err = s.bf.Statuses(st, s.now(), s.loc)
		return err
	})
	return map[string]any{"budgets": out}, err
}

// set answers budget_set.
func (s *server) set(args json.RawMessage) (any, error) {
	var p struct {
		Model string `json:"model"`
		Span  string `json:"span"`
		Limit int64  `json:"limit"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("参数不是合法的 JSON")
	}
	b := budget.Budget{Model: p.Model, Span: p.Span, Limit: p.Limit}
	if err := s.bf.Set(b); err != nil {
		return nil, err
	}
	return map[string]any{"saved": b}, nil
}

// del answers budget_delete.
func (s *server) del(args json.RawMessage) (any, error) {
	var p struct {
		Model string `json:"model"`
		Span  string `json:"span"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("参数不是合法的 JSON")
	}
	if err := s.bf.Delete(p.Span, p.Model); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}
