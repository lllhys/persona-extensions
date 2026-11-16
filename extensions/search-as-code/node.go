package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
)

// The program on a computer (the manifest's node section, `search-as-code node`). It reads only this computer's
// transcripts and answers the hub program's calls over nodes.call:
//
//	sac.search    run a compiled program here and return the ranked evidence (never whole transcripts)
//	sac.index     the sessions changed since a cursor, as a compact user / assistant plain-text index
//	sac.projects  the projects this computer's sessions belong to
//	sac.restore   restore a session disklessly moved to the cloud, on this computer
//
// It has no hub token and calls nothing of the hub's.

type nodeWorker struct {
	mu   sync.Mutex
	st   State
	data string
	now  func() time.Time
}

func newNodeWorker(data string, cfg configSpec) *nodeWorker {
	w := &nodeWorker{data: data, now: time.Now}
	w.configure(cfg)
	return w
}

// configure takes the extension's settings: the extra session and text-index directories apply on every
// computer (where they do not exist, nothing is read); the repository directories are the hub's only.
func (w *nodeWorker) configure(cfg configSpec) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.st = State{SessionDirs: splitDirs(cfg.SessionDirs), ColdTextDirs: splitDirs(cfg.ColdTextDirs)}
}

func (w *nodeWorker) state() *State {
	w.mu.Lock()
	defer w.mu.Unlock()
	st := w.st
	st.SessionDirs, st.ColdTextDirs = slices.Clone(w.st.SessionDirs), slices.Clone(w.st.ColdTextDirs)
	return &st
}

func nodeProgram() ext.Program {
	var w *nodeWorker
	return ext.Program{
		ID: about.ID, Version: about.Version, Manifest: manifest,
		Init: func(_ context.Context, in *ext.Init, _ *ext.Hub) (map[string]any, error) {
			var cfg configSpec
			_ = in.Config(&cfg)
			w = newNodeWorker(in.Info().DataDir, cfg)
			return nil, nil
		},
		Namespaces: map[string]ext.Handler{
			"sac": func(ctx context.Context, method string, params json.RawMessage) (any, error) {
				if w == nil {
					return nil, errors.New("not initialized")
				}
				return w.rpc(ctx, method, params)
			},
		},
	}
}

func (w *nodeWorker) rpc(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "search":
		var p nodeSearchParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return w.search(p)
	case "index":
		var p indexParams
		if len(params) > 0 {
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, err
			}
		}
		return w.index(p), nil
	case "projects":
		_, stats := loadSessions(w.state())
		return map[string]any{"projects": projectList(stats.ByProject)}, nil
	case "restore":
		var p struct {
			EntryID string `json:"entry_id"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return restoreArchived(ctx, w.state(), p.EntryID, w.data)
	default:
		return nil, ext.UnknownMethod("sac." + method)
	}
}

// search runs the hub's program over this computer's sessions, named with the computer the hub knows it as.
func (w *nodeWorker) search(p nodeSearchParams) (*ExecutionResult, error) {
	if len(p.Program.Steps) > maxSteps {
		return nil, errors.New("too many steps")
	}
	if err := validateSteps(p.Program.Steps); err != nil {
		return nil, err
	}
	st := w.state()
	sessions, stats := loadSessions(st)
	for _, s := range sessions {
		s.Machine, s.MachineName = p.Machine, p.MachineName
	}
	// Repository files are the hub's: scan_files finds nothing here.
	st.RepoDirs = nil
	prog := withLimit(p.Program, programLimit(p.Program))
	return executeSessions(st, sessions, stats, prog, w.now()), nil
}

// indexCursor orders sessions by update time, then id, so a page boundary between sessions updated at the same
// moment loses none of them.
type indexCursor struct {
	At time.Time `json:"at,omitzero"`
	ID string    `json:"id,omitempty"`
}

func (c indexCursor) after(o indexCursor) bool {
	if c.At.Equal(o.At) {
		return c.ID > o.ID
	}
	return c.At.After(o.At)
}

type indexParams struct {
	After indexCursor `json:"after"`
	Limit int         `json:"limit,omitempty"`
}

type indexPage struct {
	Sessions []*SessionRecord `json:"sessions"`
	Cursor   indexCursor      `json:"cursor"`
	More     bool             `json:"more,omitempty"`
}

// index is a page of the sessions updated after the cursor, oldest first, each compacted.
func (w *nodeWorker) index(p indexParams) indexPage {
	limit := p.Limit
	if limit <= 0 || limit > indexPageSessions {
		limit = indexPageSessions
	}
	sessions, _ := loadSessions(w.state())
	var newer []*SessionRecord
	for _, s := range sessions {
		if (indexCursor{At: s.UpdatedAt, ID: s.SessionID}).after(p.After) {
			newer = append(newer, s)
		}
	}
	slices.SortFunc(newer, func(a, b *SessionRecord) int {
		if c := a.UpdatedAt.Compare(b.UpdatedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.SessionID, b.SessionID)
	})
	out := indexPage{Cursor: p.After, More: len(newer) > limit}
	for _, s := range newer[:min(len(newer), limit)] {
		out.Sessions = append(out.Sessions, compactSession(s))
		out.Cursor = indexCursor{At: s.UpdatedAt, ID: s.SessionID}
	}
	return out
}

// compactSession is a session as the synced index keeps it: its metadata and the user and assistant turns, each
// clipped, the first and the latest ones when there are many; no tool calls or outputs, no paths of this
// computer's index files.
func compactSession(s *SessionRecord) *SessionRecord {
	c := *s
	c.TextPath, c.StateDir, c.Machine, c.MachineName = "", "", "", ""
	var turns []SessionTurn
	fromDropped := false
	for _, tr := range s.Turns {
		if tr.IsTool || (tr.Role != "user" && tr.Role != "assistant") {
			fromDropped = fromDropped || firstLineTitle(tr.Text) == s.Title
			continue
		}
		tr.Text = clipRunes(strings.TrimSpace(tr.Text), maxCachedTurnRunes)
		tr.ToolName = ""
		turns = append(turns, tr)
	}
	if len(turns) > maxCachedTurns {
		head := maxCachedTurns / 4
		turns = append(turns[:head:head], turns[len(turns)-(maxCachedTurns-head):]...)
	}
	c.Turns = turns
	// A title taken from a dropped turn (a tool's output) would carry it into the index: take it from the kept
	// turns instead.
	if fromDropped && !slices.ContainsFunc(turns, func(tr SessionTurn) bool { return firstLineTitle(tr.Text) == s.Title }) {
		c.Title = conversationTitle(turns, s.SessionID)
	}
	return &c
}
