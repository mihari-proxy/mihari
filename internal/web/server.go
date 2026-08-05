package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/coder/websocket"
)

// PanelSource provides the active panel static tree and setup deep-link.
type PanelSource interface {
	ActiveDir() (string, error)
	SetupPath(gatewayHost string) string
}

// Mutator handles allowlisted browser writes through the daemon coordinator.
// When nil, allowlisted mutations are rejected as unsupported.
type Mutator interface {
	SelectProxy(ctx context.Context, group, name string) error
	CloseConnection(ctx context.Context, id string) error
	CloseAllConnections(ctx context.Context) error
	// ApplyConfigPatch applies allowlisted config mutations (currently TUN only).
	ApplyConfigPatch(ctx context.Context, patch map[string]any) error
}

// Server is the loopback Web gateway: auth, static panel hosting, and API proxy.
type Server struct {
	Addr             string
	Auth             Authenticator
	Proxy            *httputil.ReverseProxy
	ControllerURL    string
	ControllerSecret string
	Panel            PanelSource
	Mutator          Mutator
	HTTPClient       *http.Client

	listener   net.Listener
	httpServer *http.Server
	sessions   atomic.Int64
	mu         sync.Mutex
	serving    bool
}

// Options configures a Web gateway server.
type Options struct {
	Addr             string
	Auth             Authenticator
	ControllerURL    string
	ControllerSecret string
	Panel            PanelSource
	Mutator          Mutator
	Transport        http.RoundTripper
}

// New builds a gateway server. Listen is deferred until Serve or ListenAndServe.
func New(options Options) (*Server, error) {
	if options.Addr == "" {
		return nil, protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "web gateway address is required"}
	}
	proxy, err := NewControllerProxy(ProxyOptions{
		ControllerURL:    options.ControllerURL,
		ControllerSecret: options.ControllerSecret,
		Transport:        options.Transport,
	})
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	if options.Transport != nil {
		client.Transport = options.Transport
	}
	server := &Server{
		Addr: options.Addr, Auth: options.Auth, Proxy: proxy,
		ControllerURL: options.ControllerURL, ControllerSecret: options.ControllerSecret,
		Panel: options.Panel, Mutator: options.Mutator, HTTPClient: client,
	}
	server.httpServer = &http.Server{
		Handler:           server.handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return server, nil
}

// SessionCount approximates concurrent authenticated request handlers.
func (s *Server) SessionCount() int {
	n := s.sessions.Load()
	if n < 0 {
		return 0
	}
	return int(n)
}

// ListenAddr returns the bound address after Serve starts, or the configured Addr.
func (s *Server) ListenAddr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.Addr
}

// Serve listens on Addr until ctx is cancelled, then shuts down gracefully.
func (s *Server) Serve(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return protocol.APIError{
			Code:    protocol.CodeInvalidState,
			Message: "web gateway address is unavailable",
			Details: map[string]any{"setting": "web-addr", "address": s.Addr},
		}
	}
	s.mu.Lock()
	s.listener = listener
	s.serving = true
	s.mu.Unlock()

	errCh := make(chan error, 1)
	go func() {
		err := s.httpServer.Serve(listener)
		if err == nil || err == http.ErrServerClosed {
			errCh <- nil
			return
		}
		errCh <- err
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpServer.Shutdown(shutdownCtx)
		return <-errCh
	case err := <-errCh:
		return err
	}
}

// Shutdown stops the HTTP server if running.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Auth endpoints are reachable without a session (except logout).
		if r.URL.Path == AuthPath {
			s.handleAuth(w, r)
			return
		}

		// One-shot open token: set cookie and redirect stripping token from URL.
		if token := r.URL.Query().Get("token"); token != "" && s.Auth.matchesWeb(token) {
			s.Auth.SetSessionCookie(w)
			q := r.URL.Query()
			q.Del("token")
			redirect := r.URL.Path
			if encoded := q.Encode(); encoded != "" {
				redirect += "?" + encoded
			}
			http.Redirect(w, r, redirect, http.StatusFound)
			return
		}

		if !s.Auth.Authorized(r) {
			if looksLikeAPIPath(normalizeAPIPath(r.URL.Path)) || isUpgradeRequest(r) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			s.writeLogin(w, r)
			return
		}

		s.sessions.Add(1)
		defer s.sessions.Add(-1)

		if r.URL.Path == "/__mihari/setup" {
			s.handleSetup(w, r)
			return
		}

		action := ClassifyUpgrade(r.Method, r.URL.Path, isUpgradeRequest(r))
		switch action {
		case ActionProxyRead:
			s.Proxy.ServeHTTP(w, r)
			return
		case ActionProxyWS:
			s.proxyWebSocket(w, r)
			return
		case ActionRejectUpgrade, ActionRejectManaged, ActionRejectUnknown:
			WriteReject(w, action)
			return
		case ActionMutateSelectProxy, ActionMutateClose, ActionMutateDelayTest, ActionMutateRestart, ActionMutateConfigs:
			s.handleMutation(w, r, action)
			return
		case ActionNotAPI:
			s.serveStatic(w, r)
			return
		default:
			s.serveStatic(w, r)
		}
	})
}

func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.writeLogin(w, r)
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		password := r.FormValue("password")
		if !s.Auth.AuthenticateForm(password) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		s.Auth.SetSessionCookie(w)
		next := r.FormValue("next")
		if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
			next = "/"
		}
		http.Redirect(w, r, next, http.StatusFound)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if host == "" {
		host = s.ListenAddr()
	}
	setup := "/"
	if s.Panel != nil {
		setup = s.Panel.SetupPath(host)
	}
	http.Redirect(w, r, setup, http.StatusFound)
}

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	if s.Panel == nil {
		http.Error(w, "no active panel", http.StatusServiceUnavailable)
		return
	}
	root, err := s.Panel.ActiveDir()
	if err != nil || root == "" {
		http.Error(w, "no active panel", http.StatusServiceUnavailable)
		return
	}
	// Prefer nested dist/ when present (common panel layout).
	fileRoot := root
	if info, err := os.Stat(filepath.Join(root, "dist")); err == nil && info.IsDir() {
		if _, err := os.Stat(filepath.Join(root, "dist", "index.html")); err == nil {
			fileRoot = filepath.Join(root, "dist")
		}
	}
	fsys := http.FS(os.DirFS(fileRoot))
	// SPA fallback: missing paths serve index.html when present.
	upath := path.Clean("/" + r.URL.Path)
	if upath == "/" {
		upath = "/index.html"
	}
	if f, err := fs.Stat(os.DirFS(fileRoot), strings.TrimPrefix(upath, "/")); err != nil || f.IsDir() {
		if _, err := fs.Stat(os.DirFS(fileRoot), "index.html"); err == nil {
			r = r.Clone(r.Context())
			r.URL.Path = "/index.html"
		}
	}
	http.FileServer(fsys).ServeHTTP(w, r)
}

func (s *Server) handleMutation(w http.ResponseWriter, r *http.Request, action Action) {
	if s.Mutator == nil {
		WriteReject(w, ActionRejectUnknown)
		return
	}
	ctx := r.Context()
	switch action {
	case ActionMutateSelectProxy:
		group := strings.TrimPrefix(normalizeAPIPath(r.URL.Path), "/proxies/")
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil || body.Name == "" || group == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if err := s.Mutator.SelectProxy(ctx, group, body.Name); err != nil {
			writeMutationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case ActionMutateClose:
		p := normalizeAPIPath(r.URL.Path)
		if p == "/connections" {
			if err := s.Mutator.CloseAllConnections(ctx); err != nil {
				writeMutationError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		id := strings.TrimPrefix(p, "/connections/")
		if id == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if err := s.Mutator.CloseConnection(ctx, id); err != nil {
			writeMutationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case ActionMutateConfigs:
		s.handleConfigMutation(w, r)
	default:
		// Delay/restart are classified but not fully implemented until control wiring matures.
		WriteReject(w, ActionRejectUnknown)
	}
}

// handleConfigMutation validates the browser PATCH/PUT /configs body against the
// allowlist (TUN only in this phase) and routes through the coordinator mutator.
func (s *Server) handleConfigMutation(w http.ResponseWriter, r *http.Request) {
	var patch map[string]any
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&patch); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if len(patch) == 0 {
		WriteReject(w, ActionRejectUnknown)
		return
	}

	// Managed fields reject even when mixed with allowlisted keys.
	for key := range patch {
		if isManagedConfigField(key) {
			WriteReject(w, ActionRejectManaged)
			return
		}
	}
	// Only "tun" is allowlisted for this phase.
	for key := range patch {
		if key != "tun" {
			WriteReject(w, ActionRejectUnknown)
			return
		}
	}

	tunRaw, ok := patch["tun"]
	if !ok {
		WriteReject(w, ActionRejectUnknown)
		return
	}
	tun, ok := tunRaw.(map[string]any)
	if !ok {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if _, ok := tun["enable"].(bool); !ok {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if stack, exists := tun["stack"]; exists {
		if _, ok := stack.(string); !ok {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
	}

	if err := s.Mutator.ApplyConfigPatch(r.Context(), patch); err != nil {
		writeMutationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// isManagedConfigField reports mihomo config keys that Mihari owns and never
// accepts from the browser (controller, secret, ports, external UI).
func isManagedConfigField(key string) bool {
	switch key {
	case "external-controller", "secret", "mixed-port", "bind-address",
		"external-ui", "external-ui-name", "external-ui-url":
		return true
	}
	if strings.HasPrefix(key, "external-ui") {
		return true
	}
	return false
}

func (s *Server) proxyWebSocket(w http.ResponseWriter, r *http.Request) {
	controller, err := url.Parse(s.ControllerURL)
	if err != nil {
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	scheme := "ws"
	if controller.Scheme == "https" {
		scheme = "wss"
	}
	target := scheme + "://" + controller.Host + r.URL.RequestURI()
	header := http.Header{}
	if s.ControllerSecret != "" {
		header.Set("Authorization", "Bearer "+s.ControllerSecret)
	}
	upstream, resp, err := websocket.Dial(r.Context(), target, &websocket.DialOptions{
		HTTPClient: s.HTTPClient,
		HTTPHeader: header,
	})
	if err != nil {
		if resp != nil {
			http.Error(w, "upstream stream unavailable", resp.StatusCode)
			return
		}
		http.Error(w, "upstream stream unavailable", http.StatusBadGateway)
		return
	}
	defer upstream.CloseNow()

	client, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		upstream.CloseNow()
		return
	}
	defer client.CloseNow()

	errc := make(chan error, 2)
	copyWS := func(dst, src *websocket.Conn) {
		for {
			msgType, data, err := src.Read(r.Context())
			if err != nil {
				errc <- err
				return
			}
			if err := dst.Write(r.Context(), msgType, data); err != nil {
				errc <- err
				return
			}
		}
	}
	go copyWS(upstream, client)
	go copyWS(client, upstream)
	<-errc
}

func (s *Server) writeLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	next := r.URL.RequestURI()
	if next == AuthPath {
		next = "/"
	}
	_, _ = fmt.Fprintf(w, `<!doctype html><html><head><title>Mihari Web Login</title></head><body>
<h1>Mihari Web GUI</h1>
<form method="post" action="%s">
<input type="hidden" name="next" value="%s"/>
<label>Access token <input type="password" name="password" autofocus/></label>
<button type="submit">Sign in</button>
</form>
</body></html>`, AuthPath, htmlEscape(next))
}

func writeMutationError(w http.ResponseWriter, err error) {
	var api protocol.APIError
	if errors.As(err, &api) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(protocol.NewError(api.Code, api.Message, api.Details))
		return
	}
	http.Error(w, "mutation failed", http.StatusBadGateway)
}

func isUpgradeRequest(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

func htmlEscape(s string) string {
	replacer := strings.NewReplacer(`&`, "&amp;", `"`, "&quot;", `<`, "&lt;", `>`, "&gt;")
	return replacer.Replace(s)
}
