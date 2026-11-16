package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
)

// The hub program's HTTP API, reached by the assistant's gated `sac` command (assistant/gate.json) through the
// hub at /api/v1/extensions/search-as-code. The gate decides call by call:
//
//	GET  /machines                          allowed: the computers, their keys and sync state
//	GET  /projects/{machine}                allowed: a computer's project names and keys
//	GET  /recipes                           allowed: the saved search recipes
//	GET  /search/{machine}/{project}/{scope} confirmed: reads session history; the route is what the user approves
//	POST /restore/{machine}/{entry}         confirmed: restores a cloud-moved session on that computer
//
// A search reads exactly what its route names: the computer (a key, hub or all), the project (a key or all) and
// the scope. The first read of a route in a conversation shows an approval card naming them, and "Always allow
// in this session" is remembered per route, so per computer, project and scope.

func (a *app) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /machines", a.apiMachines)
	mux.HandleFunc("GET /projects/{machine}", a.apiProjects)
	mux.HandleFunc("GET /recipes", a.apiRecipes)
	mux.HandleFunc("GET /search/{machine}/{project}/{scope}", a.apiSearch)
	mux.HandleFunc("POST /restore/{machine}/{entry}", a.apiRestore)
	return mux
}

type apiError struct {
	Code  string `json:"code"`
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func apiFail(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, apiError{Code: code, Error: msg})
}

// fleetOrLocal is the app's fleet, or one with the hub's computer only (no hub connection: tests, old hubs).
func (a *app) fleetOrLocal() *fleet {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fleet != nil {
		return a.fleet
	}
	return newFleet(nil, nil, "", nil)
}

func (a *app) apiMachines(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"machines": a.fleetOrLocal().statuses(r.Context()),
		"note":     "Use a machine's key (or hub, or all) and a project key from GET /projects/{machine} (or all) in GET /search/{machine}/{project}/{scope}.",
	})
}

func (a *app) apiProjects(w http.ResponseWriter, r *http.Request) {
	f := a.fleetOrLocal()
	targets, err := resolveTargets(f.targets(r.Context()), r.PathValue("machine"))
	if err != nil {
		apiFail(w, http.StatusNotFound, "unknown_machine", err.Error())
		return
	}
	out := []map[string]any{}
	for _, t := range targets {
		var list []projectInfo
		var perr error
		if t.Local {
			var st *State
			if err := a.db().read(func(s *State) { st = s }); err != nil {
				perr = err
			} else {
				_, stats := loadSessions(st)
				list = projectList(stats.ByProject)
			}
		} else {
			list, perr = f.projects(r.Context(), t)
		}
		row := map[string]any{"machine": t.Key, "name": t.Name, "projects": list}
		if perr != nil {
			row["error"] = perr.Error()
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"machines": out})
}

func (a *app) apiRecipes(w http.ResponseWriter, r *http.Request) {
	var recipes []*Recipe
	if err := a.db().read(func(st *State) { recipes = st.allRecipes() }); err != nil {
		apiFail(w, http.StatusInternalServerError, "state", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recipes": recipes, "operators": knownOps, "scopes": scopes})
}

// projectSegment is a project route segment: a key (routeKey), or all.
var projectSegment = regexp.MustCompile(`^[a-z][a-z-]{0,23}$`)

func (a *app) apiSearch(w http.ResponseWriter, r *http.Request) {
	scope := r.PathValue("scope")
	if !isValidScope(scope) {
		apiFail(w, http.StatusBadRequest, "invalid_scope", "scope must be one of "+strings.Join(scopes, ", "))
		return
	}
	projectKey := strings.ToLower(r.PathValue("project"))
	if !projectSegment.MatchString(projectKey) {
		apiFail(w, http.StatusBadRequest, "invalid_project", "project must be all or a project key from GET /projects/{machine}")
		return
	}
	if projectKey == allKey {
		projectKey = ""
	}
	f := a.fleetOrLocal()
	targets, err := resolveTargets(f.targets(r.Context()), r.PathValue("machine"))
	if err != nil {
		apiFail(w, http.StatusNotFound, "unknown_machine", err.Error())
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	var prog SearchProgram
	var scan *State
	rerr := a.db().read(func(st *State) {
		var rec *Recipe
		if id := strings.TrimSpace(q.Get("recipe")); id != "" {
			if rec = st.recipe(id); rec == nil {
				err = fmt.Errorf("no recipe %q (GET /recipes lists them)", id)
				return
			}
		}
		prog, err = CompileProgram(CompileInput{
			Query: strings.TrimSpace(q.Get("q")), Code: strings.TrimSpace(q.Get("code")), Recipe: rec,
			Scope: scope, Harness: q.Get("harness"), Model: q.Get("model"), Since: q.Get("since"),
			Limit: cmp.Or(limit, defaultLim), ProjectKey: projectKey,
		})
		if err != nil {
			return
		}
		prog, scan = pinProgram(prog, scope, projectKey), st.scanState()
	})
	if err = cmp.Or(rerr, err); err != nil {
		apiFail(w, http.StatusBadRequest, "invalid_program", err.Error())
		return
	}
	// The route's computers, as approved: the program's own machine filters were dropped by pinProgram.
	res := f.search(r.Context(), scan, prog, a.now(), targets)
	_ = a.db().update(func(st *State, now time.Time) error {
		st.recordRun(res, now)
		return nil
	})
	writeJSON(w, http.StatusOK, res)
}

func (a *app) apiRestore(w http.ResponseWriter, r *http.Request) {
	f := a.fleetOrLocal()
	targets, err := resolveTargets(f.targets(r.Context()), r.PathValue("machine"))
	if err != nil || len(targets) != 1 {
		apiFail(w, http.StatusNotFound, "unknown_machine", "name one computer: a key from GET /machines, or hub")
		return
	}
	t, entry := targets[0], r.PathValue("entry")
	var out *RestoreResult
	if t.Local {
		var st *State
		if err = a.db().read(func(s *State) { st = s }); err == nil {
			out, err = restoreArchived(r.Context(), st, entry, a.db().dir)
		}
	} else {
		out, err = f.restore(r.Context(), t.ID, entry)
	}
	if err != nil {
		apiFail(w, http.StatusConflict, "restore_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// assistant answers the assistant namespace: credentials for the gated command (this program's address at the
// hub and the extension's token, whose scopes reach this extension and the machine list only; the gate keeps the
// token out of the assistant's sight), and summary, which spells a card's routes out for the user.
func (a *app) assistant(_ context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "credentials":
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.hubURL == "" || a.token == "" {
			return nil, errors.New("the hub gave no API address or token (permissions.hub not granted)")
		}
		return map[string]any{"env": map[string]string{
			"SAC_BASE":  a.hubURL + "/api/v1/extensions/" + about.ID,
			"SAC_TOKEN": a.token,
		}}, nil
	case "summary":
		var p struct {
			Summary string `json:"summary"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return map[string]string{"summary": a.cardSummary(context.Background(), p.Summary)}, nil
	default:
		return nil, ext.UnknownMethod("assistant." + method)
	}
}

var cardRoute = regexp.MustCompile(`^(GET|POST) /(search|restore)/([^/?\s]+)/([^/?\s]+)(?:/([^/?\s]+))?(?:\?(\S*))?$`)

// cardSummary adds under each route of an approval card what it reads: which computers (and which of them are
// offline, searched in their synced index), which project, which scope and the query.
func (a *app) cardSummary(ctx context.Context, summary string) string {
	all := a.fleetOrLocal().targets(ctx)
	var b strings.Builder
	for i, line := range strings.Split(summary, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
		m := cardRoute.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		targets, err := resolveTargets(all, m[3])
		var names []string
		for _, t := range targets {
			n := t.Name
			if !t.Local && !t.Online {
				n += "（离线，查已同步的索引）"
			}
			names = append(names, n)
		}
		machines := strings.Join(names, "、")
		if err != nil {
			machines = m[3] + "（未知电脑）"
		}
		if m[2] == "restore" {
			b.WriteString("\n电脑：" + machines + "\n恢复已上云会话：" + m[4])
			continue
		}
		project := "全部项目"
		if m[4] != allKey {
			project = "项目 " + a.projectName(ctx, targets, m[4])
		}
		b.WriteString("\n电脑：" + machines + "\n项目：" + project + "\n范围：" + cmp.Or(scopeLabels[m[5]], m[5]))
		if vals, err := parseQuery(m[6]); err == nil {
			for _, k := range []string{"q", "recipe", "code", "since"} {
				if v := strings.TrimSpace(vals[k]); v != "" {
					b.WriteString("\n" + map[string]string{"q": "查询", "recipe": "配方", "code": "流水线", "since": "时间"}[k] + "：" + clipRunes(v, 200))
				}
			}
		}
	}
	return b.String()
}

// projectName is the name of the project with the key on the hub's computer or in a synced index ("<key>" when
// neither has it).
func (a *app) projectName(_ context.Context, targets []target, key string) string {
	f := a.fleetOrLocal()
	for _, t := range targets {
		var byProject map[string]int
		if t.Local {
			var st *State
			if a.db().read(func(s *State) { st = s }) == nil {
				_, stats := loadSessions(st)
				byProject = stats.ByProject
			}
		} else if c := f.loadCache(t.ID); c != nil {
			byProject = statsOf(c.Sessions).ByProject
		}
		for name := range byProject {
			if projectKey(name) == key {
				return name
			}
		}
	}
	return key
}

func parseQuery(raw string) (map[string]string, error) {
	out := map[string]string{}
	if raw == "" {
		return out, nil
	}
	req, err := http.NewRequest(http.MethodGet, "http://x/?"+raw, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range req.URL.Query() {
		out[k] = strings.Join(v, " ")
	}
	return out, nil
}

// rpcNodes calls the program on a computer through the hub's nodes.call.
type rpcNodes struct{ hub *ext.Hub }

func (n rpcNodes) Call(ctx context.Context, machine, method string, params, out any) error {
	return n.hub.Call(ctx, "nodes.call", map[string]any{"machine": machine, "method": method, "params": params}, out)
}

// hubClient reads the hub's machine list with the extension's token (scope machines.read).
type hubClient struct {
	base, token string
	client      *http.Client
}

func (c *hubClient) Machines(ctx context.Context) ([]hubMachine, error) {
	if c.base == "" || c.token == "" {
		return nil, errors.New("no hub API")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/v1/machines", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("GET /api/v1/machines: http %d", resp.StatusCode)
	}
	var out struct {
		Machines []hubMachine `json:"machines"`
	}
	return out.Machines, json.Unmarshal(raw, &out)
}
