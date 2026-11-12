package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Everything here is synthetic: temporary git repositories, a fake hub and the node worker called in process.

var testStart = time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)

const testMachine = "machine-1"

func newTestQueue(t *testing.T, cfg QueueConfig) *Queue {
	t.Helper()
	q, err := NewQueue(t.TempDir(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.SetMachines([]MachineSlot{{Name: testMachine, Title: "Studio", Online: true, MaxSlots: 1}}); err != nil {
		t.Fatal(err)
	}
	return q
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newTestRepo makes <root>/app, a git repository with one commit, and returns root and the repository.
func newTestRepo(t *testing.T) (root, repo string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo = filepath.Join(root, "app")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-q", "-m", "first")
	return root, repo
}

// fakeHub is the hub the dispatcher sees: sessions the test moves by hand.
type fakeHub struct {
	mu         sync.Mutex
	machines   []hubMachine
	sessions   map[string]*hubSession
	replies    map[string]string
	created    []sessionRequest
	interrupts []string
	attached   []string
	createErr  error
}

func newFakeHub() *fakeHub {
	return &fakeHub{machines: []hubMachine{{ID: testMachine, Name: "Studio", Online: true}},
		sessions: map[string]*hubSession{}, replies: map[string]string{}}
}

func (f *fakeHub) Machines(context.Context) ([]hubMachine, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]hubMachine(nil), f.machines...), nil
}

func (f *fakeHub) CreateSession(_ context.Context, req sessionRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return "", f.createErr
	}
	f.created = append(f.created, req)
	id := fmt.Sprintf("session-%d", len(f.created))
	f.sessions[id] = &hubSession{SessionID: id, State: "working", UpdatedAt: "u0"}
	return id, nil
}

func (f *fakeHub) Session(_ context.Context, _ string, sessionID string) (*hubSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[sessionID]
	if !ok {
		return nil, nil
	}
	c := *s
	return &c, nil
}

func (f *fakeHub) LastReply(_ context.Context, _ string, sessionID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.replies[sessionID], nil
}

func (f *fakeHub) Interrupt(_ context.Context, _ string, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.interrupts = append(f.interrupts, sessionID)
	return nil
}

func (f *fakeHub) AttachTodo(_ context.Context, todoID, _ string, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attached = append(f.attached, todoID+"/"+sessionID)
	return nil
}

// set changes a session the way the hub would report it.
func (f *fakeHub) set(id string, change func(s *hubSession)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f.sessions[id])
}

// turnEnds makes the session's turn end with reply.
func (f *fakeHub) turnEnds(id, updatedAt, reply string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.sessions[id]
	s.State, s.UpdatedAt, s.PendingInteractions = "unread", updatedAt, 0
	f.replies[id] = reply
}

// localNodes calls the node worker in process, through the same JSON the hub would carry.
type localNodes struct {
	worker *nodeWorker
	mu     sync.Mutex
	calls  []string
	fail   map[string]error
}

func (l *localNodes) Call(ctx context.Context, machine, method string, params, out any) error {
	l.mu.Lock()
	l.calls = append(l.calls, method)
	err := l.fail[method]
	l.mu.Unlock()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	res, err := l.worker.rpc(ctx, strings.TrimPrefix(method, "ats."), raw)
	if err != nil || out == nil {
		return err
	}
	raw, err = json.Marshal(res)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// todoServer is the hub's to-do API as far as the outcome write-back uses it.
type todoServer struct {
	mu        sync.Mutex
	decisions []OutcomeDecisionPayload
	paths     []string
	srv       *httptest.Server
}

func newTodoServer(t *testing.T) *todoServer {
	ts := &todoServer{}
	seen := map[string]bool{}
	ts.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		if strings.Contains(r.URL.Path, "/todo_missing/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body OutcomeDecisionPayload
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		ts.decisions = append(ts.decisions, body)
		ts.paths = append(ts.paths, r.URL.Path)
		inserted := !seen[body.Ref]
		seen[body.Ref] = true
		_ = json.NewEncoder(w).Encode(OutcomeDecisionResponse{Inserted: inserted, Decision: json.RawMessage(`{"id":"dec_1"}`)})
	}))
	t.Cleanup(ts.srv.Close)
	return ts
}

func (ts *todoServer) written() []OutcomeDecisionPayload {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return append([]OutcomeDecisionPayload(nil), ts.decisions...)
}

// rig is a dispatcher over a real queue and a real node worker, with a fake hub and a clock the test moves.
type rig struct {
	t      *testing.T
	q      *Queue
	hub    *fakeHub
	nodes  *localNodes
	worker *nodeWorker
	todos  *todoServer
	d      *dispatcher
	cfg    ExtConfig
	now    time.Time
	root   string
	repo   string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, hub: newFakeHub(), now: testStart, cfg: defaultExtConfig()}
	r.root, r.repo = newTestRepo(t)
	r.cfg.Machines, r.cfg.Repos = []string{testMachine}, []string{r.root}
	r.q = newTestQueue(t, r.cfg.queueConfig())
	r.todos = newTodoServer(t)
	r.start(t.TempDir())
	return r
}

// start makes the dispatcher and the node worker (again: a restart of both programs keeps the queue and the
// worker's data directory).
func (r *rig) start(nodeData string) {
	writer := NewDecisionWriter(r.todos.srv.URL, "test-token", []string{"todos.write"}, r.todos.srv.Client())
	r.nodes = &localNodes{fail: map[string]error{}}
	r.d = newDispatcher(r.q, r.hub, r.nodes, writer, func() ExtConfig { return r.cfg }, func() time.Time { return r.now })
	r.worker = newNodeWorker(nodeData, r.cfg.Repos, func(event string, params any) {
		if res, ok := params.(acceptResult); ok && event == "ats.accepted" {
			r.d.onAccepted(res)
		}
	})
	r.nodes.worker = r.worker
}

func (r *rig) enqueue(in EnqueueInput) *Task {
	r.t.Helper()
	if in.Repo == "" {
		in.Repo = r.repo
	}
	if in.Prompt == "" {
		in.Prompt = "Do the thing"
	}
	if in.Labels == nil {
		in.Labels = map[string]string{PersonaTodoLabel: "todo_1"}
	}
	in.Now = r.now
	res, err := r.q.Enqueue(in)
	if err != nil {
		r.t.Fatal(err)
	}
	return res.Task
}

// tick runs one dispatcher round, a little later.
func (r *rig) tick() {
	r.now = r.now.Add(dispatchInterval)
	r.d.tick(context.Background())
}

func (r *rig) task(id string) *Task {
	r.t.Helper()
	t, err := r.q.GetTask(id)
	if err != nil {
		r.t.Fatal(err)
	}
	return t
}

// waitAccepted waits until the acceptance command of the attempt has finished on the node worker.
func (r *rig) waitAccepted(taskID string, attempt int) {
	r.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if res, err := r.worker.acceptStatus(taskID, attempt); err == nil && res.Done {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.t.Fatal("the acceptance command did not finish")
}

func TestPackagedManifestStripsSyntheticIcon(t *testing.T) {
	raw := []byte(`{"id":"synthetic","version":"0.1.0","icon":"assets/icon.svg","tags":["demo"],"replaces":"predecessor","unknown_field":true}`)
	var runtime map[string]json.RawMessage
	if err := json.Unmarshal(packagedManifest(raw), &runtime); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"icon", "tags", "replaces"} {
		if _, ok := runtime[key]; ok {
			t.Errorf("source metadata %s leaked", key)
		}
	}
	if string(runtime["unknown_field"]) != "true" {
		t.Fatal("projection silently removed an unknown runtime field")
	}
}
