package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/pluginkit"
)

// The connector's catalog, for Persona's source subscriptions (connector.catalog.list / connector.catalog.call): the
// same three tools as tools.list, under the source the hub derived from them before the catalog was declared (id
// markets, the names without "markets_"), so subscriptions made then keep their recipes. search answers JSON there,
// an object with one list of instruments each with its ref as id, which a subscription follows one by one; quote and
// candles answer their text (values at a moment: snapshots). The assistant's tools.call answers text, as before.

const catalogSource = "markets"

func (p *plugin) cCatalogList(context.Context, *pluginkit.Hub, json.RawMessage) (any, error) {
	tools := p.tools()
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{"name": strings.TrimPrefix(t.Name, "markets_"), "description": t.Description,
			"input_schema": t.InputSchema, "read_only": true})
	}
	return map[string]any{"sources": []map[string]any{{"id": catalogSource, "name": "行情", "tools": out}}}, nil
}

func (p *plugin) cCatalogCall(ctx context.Context, _ *pluginkit.Hub, raw json.RawMessage) (any, error) {
	var in struct {
		Source string          `json:"source"`
		Tool   string          `json:"tool"`
		Args   json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	known := false
	for _, t := range p.tools() {
		known = known || t.Name == "markets_"+in.Tool
	}
	if in.Source != catalogSource || !known {
		return nil, pluginkit.Errorf("not_read_only", "the source and tool must be in the read-only catalog")
	}
	text, err := p.run(ctx, "markets_"+in.Tool, in.Args, true)
	if err != nil {
		return nil, err
	}
	return map[string]any{"text": text}, nil
}

type instrumentJSON struct {
	ID       string   `json:"id"`    // its ref, source:symbol (markets_quote and markets_candles take it)
	Title    string   `json:"title"` // its name, else its symbol
	Source   string   `json:"source"`
	Symbol   string   `json:"symbol"`
	Kind     string   `json:"kind,omitempty"`
	Currency string   `json:"currency,omitempty"`
	Last     float64  `json:"last"`
	Change   *float64 `json:"change,omitempty"` // over the period, a fraction (0.012 is +1.2 %)
	High     float64  `json:"high,omitempty"`
	Low      float64  `json:"low,omitempty"`
	Turnover float64  `json:"turnover,omitempty"`
	Period   string   `json:"period,omitempty"`
	Time     string   `json:"time,omitempty"` // of the quote, RFC 3339
	Idle     bool     `json:"idle,omitempty"` // the market is closed
	Body     string   `json:"body"`           // the quote as markets_search's text writes it
}

type searchJSON struct {
	Query       string           `json:"query"`
	Instruments []instrumentJSON `json:"instruments"`
}

// searchJSON is markets_search for subscriptions: the same search, as JSON.
func (p *plugin) searchJSON(ctx context.Context, query string, limit int) (string, error) {
	q := cleanQuery(query)
	if q == "" {
		return "", fmt.Errorf("query 要给名称或代码")
	}
	hits, err := p.search(ctx, q, limit, false)
	if err != nil {
		return "", err
	}
	out := searchJSON{Query: q, Instruments: make([]instrumentJSON, 0, len(hits))}
	for _, h := range hits {
		qt := h.Quote
		j := instrumentJSON{ID: h.Ref.String(), Title: qt.Name, Source: h.Ref.Source, Symbol: h.Ref.Symbol, Kind: qt.Kind,
			Currency: qt.Currency, Last: qt.Last, High: qt.High, Low: qt.Low, Turnover: qt.Turnover, Period: qt.Period, Idle: qt.Idle,
			Body: strings.TrimPrefix(quoteLine(h.Ref, qt), "- ")}
		if j.Title == "" {
			j.Title = h.Ref.Symbol
		}
		if c, ok := qt.Change(); ok {
			j.Change = &c
		}
		if !qt.At.IsZero() {
			j.Time = qt.At.UTC().Format(time.RFC3339)
		}
		out.Instruments = append(out.Instruments, j)
	}
	b, err := json.Marshal(out)
	return string(b), err
}
