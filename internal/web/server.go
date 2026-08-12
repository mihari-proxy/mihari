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

	"github.com/coder/websocket"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/panel"
)

// PanelSource provides panel static trees and setup deep-links.
// Multiple installed panels may be served concurrently under /__mihari/panels/{panelID}/;
// ActiveDir/SetupPath describe the default panel for root "/".
type PanelSource interface {
	ActiveDir() (string, error)
	SetupPath(gatewayHost string) string
	// PanelDir returns the static file root for an installed panel, or empty when missing.
	PanelDir(panelID string) (string, error)
	// SetupPathFor returns the same-origin setup deep-link for a specific panel.
	SetupPathFor(panelID, gatewayHost string) string
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

type webSocketRelayObserver interface {
	relayStarted()
	relayFinished()
	handlerFinished()
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
	wsObserver webSocketRelayObserver
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
		// Bare panel mount or root open lands on that panel's setup deep-link so
		// backends point at the gateway (same-origin), not the controller port.
		if token := r.URL.Query().Get("token"); token != "" && s.Auth.matchesWeb(token) {
			s.Auth.SetSessionCookie(w)
			q := r.URL.Query()
			q.Del("token")
			reqPath := r.URL.Path
			if reqPath == "" {
				reqPath = "/"
			}
			if q.Encode() == "" && isPanelEntryPath(reqPath) {
				http.Redirect(w, r, s.panelSetupPath(r), http.StatusFound)
				return
			}
			redirect := reqPath
			if encoded := q.Encode(); encoded != "" {
				redirect += "?" + encoded
			}
			http.Redirect(w, r, redirect, http.StatusFound)
			return
		}

		if r.URL.Path == "/__mihari/setup" {
			if !s.Auth.Authorized(r) {
				s.writeLogin(w, r)
				return
			}
			s.sessions.Add(1)
			defer s.sessions.Add(-1)
			s.handleSetup(w, r)
			return
		}

		if !s.Auth.Authorized(r) {
			// Panel static is not secret (loopback only; controller secret never embedded).
			// Vite module scripts use crossorigin → credentials=omit (no session cookie),
			// and Workbox precaches index.html the same way. Gate only API/WS/mutations;
			// any non-API GET/HEAD serves static without a session.
			if looksLikeAPIPath(normalizeAPIPath(r.URL.Path)) || isUpgradeRequest(r) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == "" {
				s.serveStatic(w, r)
				return
			}
			s.writeLogin(w, r)
			return
		}

		s.sessions.Add(1)
		defer s.sessions.Add(-1)

		action := ClassifyUpgrade(r.Method, r.URL.Path, isUpgradeRequest(r))
		switch action {
		case ActionProxyRead, ActionProxyStorage:
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
		if next == "" || next == "/" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
			next = s.panelSetupPath(r)
		}
		http.Redirect(w, r, next, http.StatusFound)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, s.panelSetupPath(r), http.StatusFound)
}

// panelSetupPath returns the same-origin setup deep-link for the panel addressed by r.
func (s *Server) panelSetupPath(r *http.Request) string {
	host := r.Host
	if host == "" {
		host = s.ListenAddr()
	}
	if s.Panel == nil {
		return "/"
	}
	if panelID := panelIDFromPath(r.URL.Path); panelID != "" {
		if setup := s.Panel.SetupPathFor(panelID, host); setup != "" {
			return setup
		}
	}
	if setup := s.Panel.SetupPath(host); setup != "" {
		return setup
	}
	return "/"
}

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	if s.Panel == nil {
		http.Error(w, "no active panel", http.StatusServiceUnavailable)
		return
	}
	reqPath := path.Clean("/" + r.URL.Path)
	fileRoot := ""
	servePath := reqPath
	mountPrefix := ""
	if panelID := panelIDFromPath(reqPath); panelID != "" {
		root, err := s.Panel.PanelDir(panelID)
		if err != nil || root == "" {
			http.Error(w, "panel is not installed", http.StatusNotFound)
			return
		}
		fileRoot = root
		mountPrefix = panel.UIMount(panelID)
		servePath = strings.TrimPrefix(reqPath, mountPrefix)
		if servePath == "" {
			servePath = "/"
		}
	} else {
		// Root-absolute Vite assets (/assets/…, /registerSW.js) often load while the
		// document lives under /__mihari/panels/{id}/. Prefer the referer panel so the
		// correct tree is used when the default active panel differs.
		if panelID := panelIDFromReferer(r); panelID != "" {
			if root, err := s.Panel.PanelDir(panelID); err == nil && root != "" {
				fileRoot = root
				mountPrefix = panel.UIMount(panelID)
			}
		}
		if fileRoot == "" {
			root, err := s.Panel.ActiveDir()
			if err != nil || root == "" {
				http.Error(w, "no active panel", http.StatusServiceUnavailable)
				return
			}
			fileRoot = root
		}
	}
	// ResolveFileRoot already prefers dist/ and nested index; keep a defensive dist check
	// for PanelSource implementations that return the raw build directory.
	if info, err := os.Stat(filepath.Join(fileRoot, "dist")); err == nil && info.IsDir() {
		if _, err := os.Stat(filepath.Join(fileRoot, "dist", "index.html")); err == nil {
			fileRoot = filepath.Join(fileRoot, "dist")
		}
	}
	// Serve under the stripped panel path (or original path for the default root panel).
	// Do not rewrite "/" → "/index.html": FileServer redirects */index.html to "./" and that loops.
	upath := path.Clean("/" + servePath)
	if upath != r.URL.Path {
		r = r.Clone(r.Context())
		r.URL.Path = upath
	}
	rel := strings.TrimPrefix(upath, "/")
	if rel == "" {
		rel = "."
	}
	dirFS := os.DirFS(fileRoot)
	indexPath := filepath.Join(fileRoot, "index.html")
	// Directory entry (/ or /__mihari/panels/{id}/) always serves rewritten index when present.
	if upath == "/" || upath == "." {
		if _, err := os.Stat(indexPath); err == nil {
			s.servePanelIndex(w, r, indexPath, mountPrefix)
			return
		}
		http.NotFound(w, r)
		return
	}
	f, err := fs.Stat(dirFS, rel)
	if err != nil || (f != nil && f.IsDir()) {
		// Never SPA-fallback for real static extensions — that yields text/html for
		// module scripts (strict MIME check) and breaks Vite/Workbox loads.
		if looksLikeStaticAssetPath(upath) {
			http.NotFound(w, r)
			return
		}
		if _, err := os.Stat(indexPath); err == nil {
			s.servePanelIndex(w, r, indexPath, mountPrefix)
			return
		}
		http.NotFound(w, r)
		return
	}
	// Explicit index.html also needs mount rewriting when under a panel.
	if strings.EqualFold(path.Base(upath), "index.html") {
		s.servePanelIndex(w, r, filepath.Join(fileRoot, filepath.FromSlash(rel)), mountPrefix)
		return
	}
	http.FileServer(http.FS(dirFS)).ServeHTTP(w, r)
}

// servePanelIndex serves index.html, rewriting root-absolute asset URLs so a panel
// built with Vite base "/" works under /__mihari/panels/{id}/.
func (s *Server) servePanelIndex(w http.ResponseWriter, r *http.Request, indexPath, mountPrefix string) {
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	body := raw
	if mountPrefix != "" {
		body = rewriteRootAbsoluteURLs(raw, mountPrefix)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Avoid long-lived wrong-base caches while panels move between mounts.
	w.Header().Set("Cache-Control", "no-cache")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// rewriteRootAbsoluteURLs prefixes same-origin absolute paths (src="/…", href="/…")
// with the panel mount. Scheme-relative // and already-mounted paths are left alone.
func rewriteRootAbsoluteURLs(html []byte, mount string) []byte {
	mount = strings.TrimSuffix(strings.TrimSpace(mount), "/")
	if mount == "" || mount == "/" {
		return html
	}
	s := string(html)
	for _, attr := range []string{`src="`, `href="`, `src='`, `href='`} {
		s = rewriteRootAbsoluteAttr(s, attr, mount)
	}
	// import("/…") and similar in inline modulepreload hints.
	s = strings.ReplaceAll(s, `"/assets/`, `"`+mount+`/assets/`)
	s = strings.ReplaceAll(s, `'/assets/`, `'`+mount+`/assets/`)
	return []byte(s)
}

func rewriteRootAbsoluteAttr(s, attr, mount string) string {
	// attr includes the quote, e.g. src="
	quote := attr[len(attr)-1]
	old := attr + `/`
	var b strings.Builder
	b.Grow(len(s) + len(mount))
	for {
		i := strings.Index(s, old)
		if i < 0 {
			b.WriteString(s)
			break
		}
		rest := s[i+len(attr):]
		// Protocol-relative // or already rewritten mount.
		if strings.HasPrefix(rest, "//") || strings.HasPrefix(rest, mount+"/") || strings.HasPrefix(rest, mount+string(quote)) {
			b.WriteString(s[:i+len(old)])
			s = s[i+len(old):]
			continue
		}
		b.WriteString(s[:i])
		b.WriteString(attr)
		b.WriteString(mount)
		b.WriteString("/")
		s = s[i+len(old):]
	}
	return b.String()
}

// panelIDFromReferer extracts a panel id from the Referer path when present.
func panelIDFromReferer(r *http.Request) string {
	if r == nil {
		return ""
	}
	ref := strings.TrimSpace(r.Header.Get("Referer"))
	if ref == "" {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	return panelIDFromPath(u.Path)
}

// panelIDFromPath extracts {id} from /__mihari/panels/{id} or /__mihari/panels/{id}/....
func panelIDFromPath(reqPath string) string {
	cleaned := path.Clean("/" + reqPath)
	prefix := panel.UIPathPrefix + "/"
	if cleaned == panel.UIPathPrefix || cleaned == panel.UIPathPrefix+"/" {
		return ""
	}
	if !strings.HasPrefix(cleaned, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(cleaned, prefix)
	id, _, _ := strings.Cut(rest, "/")
	id = strings.TrimSpace(id)
	if id == "" || strings.Contains(id, "..") {
		return ""
	}
	return id
}

// isPanelEntryPath reports paths that should land on setup after one-shot open.
func isPanelEntryPath(reqPath string) bool {
	cleaned := path.Clean("/" + reqPath)
	if cleaned == "/" {
		return true
	}
	panelID := panelIDFromPath(cleaned)
	if panelID == "" {
		return false
	}
	prefix := panel.UIMount(panelID)
	return cleaned == prefix || cleaned == prefix+"/"
}

func looksLikeStaticAssetPath(reqPath string) bool {
	base := strings.ToLower(path.Base(reqPath))
	if base == "" || base == "." || base == "/" {
		return false
	}
	// Common Vite/PWA static names without a conventional extension.
	switch base {
	case "registersw.js", "sw.js", "service-worker.js", "manifest.webmanifest", "manifest.json", "favicon.ico", "index.html":
		return true
	}
	for _, ext := range []string{
		".js", ".mjs", ".css", ".map", ".svg", ".png", ".jpg", ".jpeg", ".gif", ".webp",
		".ico", ".woff", ".woff2", ".ttf", ".otf", ".eot", ".webmanifest", ".wasm", ".html",
	} {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	return false
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
	return strings.HasPrefix(key, "external-ui")
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

	observer := s.wsObserver
	if observer != nil {
		observer.relayStarted()
		observer.relayStarted()
		defer observer.handlerFinished()
	}
	relayCtx, cancelRelay := context.WithCancel(r.Context())
	defer cancelRelay()
	type relayResult struct {
		peer *websocket.Conn
		err  error
	}
	results := make(chan relayResult, 2)
	copyWS := func(dst, src *websocket.Conn) error {
		for {
			msgType, data, err := src.Read(relayCtx)
			if err != nil {
				return err
			}
			if err := dst.Write(relayCtx, msgType, data); err != nil {
				return err
			}
		}
	}
	runCopy := func(dst, src *websocket.Conn) {
		err := copyWS(dst, src)
		if observer != nil {
			observer.relayFinished()
		}
		results <- relayResult{peer: dst, err: err}
	}
	go runCopy(upstream, client)
	go runCopy(client, upstream)
	first := <-results
	cancelRelay()
	closeWebSocketRelayPeer(first.peer, first.err)
	upstream.CloseNow()
	client.CloseNow()
	<-results
}

func closeWebSocketRelayPeer(peer *websocket.Conn, relayErr error) {
	status := websocket.CloseStatus(relayErr)
	if status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway {
		_ = peer.Close(status, "")
		return
	}
	peer.CloseNow()
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
