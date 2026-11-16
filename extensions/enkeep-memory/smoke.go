package main

// The smoke subcommand is the regression smoke of design 9.5: not an acceptance check, no pass line. After a change
// to prompts, recall or model settings it shows whether giving the curated results makes predicting the user's own
// choices clearly worse.
//
// The question bank is drawn by position from real records, never written or paraphrased: every card the user
// answered themself with a choice of options; the question is the conversation right before the card (the last
// texts of its session) plus the card's question and options, the answer is the user's own answer as given. Only
// work and everyday matters go in: a model sorts every question, and the four sensitive categories (emotion,
// finance, health, family) never enter the bank. Prediction and scoring use the configured model, never an
// Anthropic one. The bank and the results stay in -out.
//
//	enkeep-memory smoke -agents-dir DIR [-assistant-dirs A,B] -from YYYY-MM-DD -to YYYY-MM-DD -state DIR -out DIR

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	exits "github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/export"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/hubapi"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

type smokeQuestion struct {
	ID       string   `json:"id"`  // the card id
	Ref      string   `json:"ref"` // agents/<machine>/<session> or asks/<day>, and the time: where it was drawn from
	At       string   `json:"at"`
	Context  []string `json:"context"` // the session's last texts before the card, as said
	Question string   `json:"question"`
	Options  []string `json:"options"`
	Answer   []string `json:"answer"` // the user's answer, as given
	Category string   `json:"category,omitempty"`
}

type smokeResult struct {
	ID        string `json:"id"`
	Condition string `json:"condition"` // none | curated
	Predicted string `json:"predicted"`
	Reason    string `json:"reason,omitempty"`
	Score     int    `json:"score"` // 2 same choice, 1 partly, 0 different
	Why       string `json:"why,omitempty"`
}

func smokeCmd(args []string) error {
	fs := flag.NewFlagSet("smoke", flag.ContinueOnError)
	agentsDir := fs.String("agents-dir", "", "agents' transcript directories (read only)")
	assistantDirs := fs.String("assistant-dirs", "", "comma-separated names under -agents-dir holding the assistant's sessions")
	from := fs.String("from", "", "first day")
	to := fs.String("to", "", "last day")
	tz := fs.String("tz", "UTC", "time zone")
	stateDir := fs.String("state", "", "the state whose curated results the second condition gets (exit files beside it)")
	out := fs.String("out", "", "where the bank and the results go")
	mdl := fs.String("model", "", "model for sorting, predicting and scoring (required)")
	provider := fs.String("provider", "", "provider pi asks (empty: pi's own default)")
	limit := fs.Int("limit", 40, "questions at most")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *agentsDir == "" || *from == "" || *to == "" || *stateDir == "" || *out == "" {
		return fmt.Errorf("-agents-dir, -from, -to, -state and -out are required")
	}
	if *mdl == "" {
		return fmt.Errorf("-model is required")
	}
	if err := refuseAnthropic(*mdl); err != nil {
		return err
	}
	loc, err := time.LoadLocation(*tz)
	if err != nil {
		return err
	}
	start, err := time.ParseInLocation("2006-01-02", *from, loc)
	if err != nil {
		return err
	}
	end, err := time.ParseInLocation("2006-01-02", *to, loc)
	if err != nil {
		return err
	}
	off := &hubapi.Offline{AgentsDir: *agentsDir, Since: start}
	if *assistantDirs != "" {
		off.AssistantDirs = strings.Split(*assistantDirs, ",")
	}
	ctx := context.Background()
	bank, err := drawBank(ctx, off, start, end.AddDate(0, 0, 1), loc)
	if err != nil {
		return err
	}
	m := model.Pi{Provider: *provider, Models: map[model.Tier]string{model.Small: *mdl}, CacheDir: filepath.Join(*out, "cache")}
	j := &judger{m: m}
	bank = sortBank(ctx, j, bank)
	if len(bank) > *limit {
		bank = bank[:*limit]
	}
	if err := writeJSON(filepath.Join(*out, "bank.json"), bank); err != nil {
		return err
	}
	curated, err := curatedBrief(*stateDir)
	if err != nil {
		return err
	}
	var res []smokeResult
	for _, cond := range []string{"none", "curated"} {
		brief := ""
		if cond == "curated" {
			brief = curated
		}
		res = append(res, predict(ctx, j, bank, cond, brief)...)
	}
	score(ctx, j, bank, res)
	if err := writeJSON(filepath.Join(*out, "results.json"), res); err != nil {
		return err
	}
	sum := smokeSummary(bank, res, j.errs)
	if err := os.WriteFile(filepath.Join(*out, "smoke.md"), []byte(sum), 0o600); err != nil {
		return err
	}
	fmt.Print(sum)
	return nil
}

// drawBank takes, by position, every card in [since, until) the user answered themself and that offered options.
func drawBank(ctx context.Context, off *hubapi.Offline, since, until time.Time, loc *time.Location) ([]smokeQuestion, error) {
	cards, err := off.Interactions(ctx, hubapi.InteractionsQuery{Since: since, Before: until})
	if err != nil && len(cards) == 0 {
		return nil, err
	}
	var bank []smokeQuestion
	for _, c := range cards {
		if c.Private || !acceptAnsweredByUser(c) || len(c.Options) < 2 || strings.TrimSpace(c.Question) == "" {
			continue
		}
		q := smokeQuestion{ID: c.ID, At: c.At.In(loc).Format(time.RFC3339), Question: c.Question, Options: c.Options, Answer: c.Answers,
			Ref: "asks/" + c.At.In(loc).Format("2006-01-02")}
		if c.Session != "" {
			q.Ref = "agents/" + c.Machine + "/" + c.Session
			items, err := off.Items(ctx, c.Machine, c.Session, hubapi.ItemsQuery{})
			if err == nil {
				var texts []string
				for _, it := range items {
					if !it.At.Before(c.At) {
						break
					}
					if (it.Kind == hubapi.KindUser || it.Kind == hubapi.KindAgent) && strings.TrimSpace(it.Text) != "" {
						who := "user"
						if it.Kind == hubapi.KindAgent {
							who = "assistant"
						}
						texts = append(texts, who+": "+clipRunes(it.Text, 600))
					}
				}
				if len(texts) > 6 {
					texts = texts[len(texts)-6:]
				}
				q.Context = texts
			}
		}
		bank = append(bank, q)
	}
	sort.Slice(bank, func(i, j int) bool { return bank[i].At < bank[j].At })
	return bank, nil
}

func acceptAnsweredByUser(c hubapi.Interaction) bool {
	return len(c.Answers) > 0 && (c.AnsweredBy == "" || c.AnsweredBy == "user")
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

const schemaSort = `{"type":"object","properties":{"items":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},"category":{"type":"string","enum":["work","everyday","emotion","finance","health","family"]}},"required":["id","category"]}}},"required":["items"]}`

const promptSort = `Sort each question a user was asked into one category by its topic: work, everyday (errands, tools, scheduling and other ordinary matters), emotion, finance, health, family.
Choose emotion, finance, health or family whenever the question touches that area at all.
Answer with JSON {"items":[{"id","category"}]}, one per item, ids as given.`

// sortBank keeps the work and everyday questions; the sensitive categories and anything unsorted are dropped.
func sortBank(ctx context.Context, j *judger, bank []smokeQuestion) []smokeQuestion {
	var items []map[string]any
	idx := map[string]*smokeQuestion{}
	for i := range bank {
		idx[bank[i].ID] = &bank[i]
		items = append(items, map[string]any{"id": bank[i].ID, "question": clipRunes(bank[i].Question, 600), "options": bank[i].Options})
	}
	j.ask(ctx, "smoke_sort", promptSort, schemaSort, items, func(id string, raw json.RawMessage) {
		var a struct {
			Category string `json:"category"`
		}
		if q := idx[id]; q != nil && json.Unmarshal(raw, &a) == nil {
			q.Category = a.Category
		}
	})
	var out []smokeQuestion
	for _, q := range bank {
		if q.Category == "work" || q.Category == "everyday" {
			out = append(out, q)
		}
	}
	return out
}

// curatedBrief is what the curator gives the assistant: exit A and the skills of exit B, as written.
func curatedBrief(dir string) (string, error) {
	var b strings.Builder
	a, err := os.ReadFile(filepath.Join(dir, exits.InstructionsPath))
	if err == nil {
		b.Write(a)
	}
	_ = filepath.WalkDir(filepath.Join(dir, "assistant", "skills"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") && b.Len() < 24000 {
			if s, err := os.ReadFile(p); err == nil {
				b.WriteString("\n\n")
				b.Write(s)
			}
		}
		return nil
	})
	if b.Len() == 0 {
		// No exits written yet: the stable judgement and workflow entries, as the exits would carry them.
		st, err := store.Open(dir)
		if err != nil {
			return "", err
		}
		for _, e := range st.S.Entries {
			if e.Live() && (e.Facet == entry.Judgement || e.Facet == entry.Workflow) && e.Grain != entry.Action && e.Sensitive == "" {
				b.WriteString("- " + exitText(e) + "\n")
			}
		}
	}
	s := b.String()
	return clipRunes(s, 24000), nil
}

const schemaPredict = `{"type":"object","properties":{"items":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},"predicted":{"type":"string"},"reason":{"type":"string"}},"required":["id","predicted","reason"]}}},"required":["items"]}`

const promptPredict = `Each item is a question an assistant put to one particular user, with the conversation just before it and the options offered.
Predict which option this user chose (the option's text; or a short answer in the user's words when none fits) and give a one-sentence reason.
Answer with JSON {"items":[{"id","predicted","reason"}]}, one per item, ids as given.`

func predict(ctx context.Context, j *judger, bank []smokeQuestion, cond, brief string) []smokeResult {
	system := promptPredict
	if brief != "" {
		system += "\n\nWhat is known about this user (their assistant's standing notes):\n" + brief
	}
	var items []map[string]any
	for _, q := range bank {
		items = append(items, map[string]any{"id": q.ID, "conversation": q.Context, "question": q.Question, "options": q.Options})
	}
	got := map[string]smokeResult{}
	j.ask(ctx, "smoke_predict_"+cond, system, schemaPredict, items, func(id string, raw json.RawMessage) {
		var a struct {
			Predicted string `json:"predicted"`
			Reason    string `json:"reason"`
		}
		if json.Unmarshal(raw, &a) == nil {
			got[id] = smokeResult{ID: id, Condition: cond, Predicted: a.Predicted, Reason: a.Reason}
		}
	})
	var out []smokeResult
	for _, q := range bank {
		r, ok := got[q.ID]
		if !ok {
			r = smokeResult{ID: q.ID, Condition: cond}
		}
		out = append(out, r)
	}
	return out
}

const schemaScore = `{"type":"object","properties":{"items":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},"score":{"type":"integer","enum":[0,1,2]},"why":{"type":"string"}},"required":["id","score","why"]}}},"required":["items"]}`

const promptScore = `Each item has the options a user was offered, the answer the user actually gave, and a predicted answer.
Score the prediction: 2 = the same choice, 1 = partly the same (overlapping choice, or the same direction with a different detail), 0 = a different choice or no prediction.
Answer with JSON {"items":[{"id","score","why"}]}, one per item, ids as given; why is one short sentence.`

func score(ctx context.Context, j *judger, bank []smokeQuestion, res []smokeResult) {
	byID := map[string]smokeQuestion{}
	for _, q := range bank {
		byID[q.ID] = q
	}
	var items []map[string]any
	idx := map[string]*smokeResult{}
	for i := range res {
		r := &res[i]
		key := r.Condition + ":" + r.ID
		idx[key] = r
		q := byID[r.ID]
		items = append(items, map[string]any{"id": key, "options": q.Options, "answer": q.Answer, "predicted": r.Predicted})
	}
	j.ask(ctx, "smoke_score", promptScore, schemaScore, items, func(id string, raw json.RawMessage) {
		var a struct {
			Score int    `json:"score"`
			Why   string `json:"why"`
		}
		if r := idx[id]; r != nil && json.Unmarshal(raw, &a) == nil {
			r.Score, r.Why = a.Score, a.Why
		}
	})
}

func smokeSummary(bank []smokeQuestion, res []smokeResult, errs []string) string {
	var b strings.Builder
	sum := map[string][4]int{} // total score, same, partly, different
	per := map[string]map[string]int{}
	for _, r := range res {
		x := sum[r.Condition]
		x[0] += r.Score
		x[3-r.Score]++
		sum[r.Condition] = x
		if per[r.ID] == nil {
			per[r.ID] = map[string]int{}
		}
		per[r.ID][r.Condition] = r.Score
	}
	fmt.Fprintf(&b, "regression smoke (no pass line): %d questions drawn from cards the user answered, work and everyday only\n", len(bank))
	for _, c := range []string{"none", "curated"} {
		x := sum[c]
		avg := 0.0
		if len(bank) > 0 {
			avg = float64(x[0]) / float64(len(bank))
		}
		fmt.Fprintf(&b, "%-8s mean %.2f  same %d, partly %d, different %d\n", c, avg, x[1], x[2], x[3])
	}
	better, worse := 0, 0
	for _, s := range per {
		switch {
		case s["curated"] > s["none"]:
			better++
		case s["curated"] < s["none"]:
			worse++
		}
	}
	fmt.Fprintf(&b, "with the curated results: better on %d, worse on %d, same on %d\n", better, worse, len(bank)-better-worse)
	for _, e := range errs {
		fmt.Fprintf(&b, "error: %s\n", e)
	}
	return b.String()
}
