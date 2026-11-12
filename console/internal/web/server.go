// Package web serves console's UI and API.
//
// Three secrets, three doors, never shared code:
//   - UI session (CONSOLE_UI_TOKEN): every UI route and every node action.
//   - Node token (CONSOLE_NODE_TOKEN): only the heartbeat a node pushes.
//   - Automation token (CONSOLE_AUTOMATION_TOKEN): the workflow routes only,
//     the node actions and the cluster shutdown, for a system such as Home
//     Assistant. It reads nothing and cannot log in.
package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kostiantyn-matsebora/barekube/console/internal/app"
	"github.com/kostiantyn-matsebora/barekube/console/internal/events"
	"github.com/kostiantyn-matsebora/barekube/console/internal/flows"
	"github.com/kostiantyn-matsebora/barekube/console/internal/nodeagent"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

const sessionCookie = "console-session"
const sessionTTL = 24 * time.Hour

// Backend is what the web layer needs. app.Service implements it.
type Backend interface {
	Nodes() []app.NodeView
	Runs() []flows.RunInfo
	Workflows() []flows.WorkflowInfo
	Events(events.Filter) []events.Event
	StartNode(ctx context.Context, wf, node string, mode flows.Mode, by string) (flows.RunInfo, error)
	StartCluster(ctx context.Context, by string) (flows.RunInfo, error)
	StartClusterStart(ctx context.Context, by string) (flows.RunInfo, error)
	Platform(ctx context.Context) app.PlatformView
	StartVipRestore(ctx context.Context, by string) (flows.RunInfo, error)
	Heartbeat(node string, k3sActive bool) error
}

// Tokens are the three secrets. Empty means that door is closed.
type Tokens struct{ UI, Node, Automation string }

type Server struct {
	be     Backend
	tokens Tokens

	mu       sync.Mutex
	sessions map[string]time.Time
	tmpl     *template.Template
}

func New(be Backend, tokens Tokens) (*Server, error) {
	tmpl, err := template.ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{be: be, tokens: tokens, sessions: map[string]time.Time{}, tmpl: tmpl}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	ui := func(h http.HandlerFunc) http.Handler { return s.requireSession(h) }
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.Handle("GET /static/", http.FileServerFS(staticFS))
	mux.Handle("GET /{$}", ui(s.handleIndex))
	mux.Handle("GET /api/nodes", ui(s.handleNodes))
	mux.Handle("GET /api/runs", ui(s.handleRuns))
	mux.Handle("GET /api/workflows", ui(s.handleWorkflows))
	mux.Handle("GET /api/events", ui(s.handleEvents))
	for _, wf := range []string{flows.WfReboot, flows.WfStandBy, flows.WfPowerOff, flows.WfRejoin} {
		mux.Handle("POST /api/nodes/{name}/"+wf, s.requireAny(s.handleNodeAction(wf)))
	}
	mux.Handle("POST /api/cluster/shutdown", s.requireAny(http.HandlerFunc(s.handleClusterShutdown)))
	// A cold start is for a cluster that is off, when the automation is not up
	// to ask for it: a person at the UI starts it, the automation token never does.
	mux.Handle("POST /api/cluster/start", ui(s.handleClusterStart))
	// The platform layer (Cilium, kube-vip) is read and repaired apart from the
	// nodes. Same rule: a person at the UI, never a token.
	mux.Handle("GET /api/platform", ui(s.handlePlatform))
	mux.Handle("POST /api/platform/vip-restore", ui(s.handleVipRestore))
	mux.Handle("POST /api/internal/heartbeat", s.requireNodeToken(http.HandlerFunc(s.handleHeartbeat)))
	return mux
}

// --- auth ---------------------------------------------------------------

func (s *Server) newSession() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	s.mu.Lock()
	s.sessions[token] = time.Now().Add(sessionTTL)
	s.mu.Unlock()
	return token
}

func (s *Server) validSession(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.sessions[c.Value]
	return ok && !time.Now().After(exp)
}

// bearerMatches never matches an empty configured token: comparing two empty
// slices would otherwise authenticate any request without a header.
func bearerMatches(r *http.Request, want string) bool {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.validSession(r) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// caller names who asked, for the event log.
func (s *Server) caller(r *http.Request) string {
	if s.validSession(r) {
		return "ui"
	}
	return "automation"
}

// requireAny admits a UI session or the automation token.
func (s *Server) requireAny(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.validSession(r) && !bearerMatches(r, s.tokens.Automation) {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireNodeToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !bearerMatches(r, s.tokens.Node) {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	_ = s.tmpl.ExecuteTemplate(w, "login.html", nil)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.tokens.UI == "" || subtle.ConstantTimeCompare([]byte(r.FormValue("token")), []byte(s.tokens.UI)) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		_ = s.tmpl.ExecuteTemplate(w, "login.html", map[string]string{"Error": "invalid token"})
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: s.newSession(), Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Expires: time.Now().Add(sessionTTL)})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// --- pages / API ----------------------------------------------------------

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	_ = s.tmpl.ExecuteTemplate(w, "index.html", nil)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.be.Nodes()) }
func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request)  { writeJSON(w, s.be.Runs()) }
func (s *Server) handleWorkflows(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.be.Workflows())
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
	writeJSON(w, s.be.Events(events.Filter{Node: q.Get("node"), RunID: q.Get("run"), Workflow: q.Get("workflow"), After: after}))
}

// startError maps a refusal to start a run onto an HTTP status.
func startError(w http.ResponseWriter, err error) {
	status := http.StatusConflict
	switch {
	case errors.Is(err, flows.ErrUnknownNode):
		status = http.StatusNotFound
	case errors.Is(err, flows.ErrUnsupported):
		status = http.StatusBadRequest
	}
	http.Error(w, err.Error(), status)
}

func (s *Server) handleNodeAction(wf string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mode := flows.Mode(strings.ToLower(r.URL.Query().Get("mode")))
		if wf == flows.WfRejoin && mode == "" {
			mode = flows.ModeGraceful
		}
		if mode != flows.ModeGraceful && mode != flows.ModeForce {
			http.Error(w, "mode must be graceful or force", http.StatusBadRequest)
			return
		}
		info, err := s.be.StartNode(r.Context(), wf, r.PathValue("name"), mode, s.caller(r))
		if err != nil {
			startError(w, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		writeJSON(w, info)
	}
}

func (s *Server) handleClusterShutdown(w http.ResponseWriter, r *http.Request) {
	info, err := s.be.StartCluster(r.Context(), s.caller(r))
	if err != nil {
		startError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, info)
}

func (s *Server) handleClusterStart(w http.ResponseWriter, r *http.Request) {
	info, err := s.be.StartClusterStart(r.Context(), s.caller(r))
	if err != nil {
		startError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, info)
}

func (s *Server) handlePlatform(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.be.Platform(r.Context()))
}

func (s *Server) handleVipRestore(w http.ResponseWriter, r *http.Request) {
	info, err := s.be.StartVipRestore(r.Context(), s.caller(r))
	if err != nil {
		startError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, info)
}

// handleHeartbeat is the one route a node calls, not a browser.
func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req nodeagent.HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := s.be.Heartbeat(req.Node, req.K3sActive); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, nodeagent.HeartbeatResponse{State: "ok"})
}
