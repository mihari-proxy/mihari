package web

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// recordingMutator captures ApplyConfigPatch calls for allowlist tests.
type recordingMutator struct {
	mu      sync.Mutex
	patches []map[string]any
	err     error
}

func (m *recordingMutator) SelectProxy(context.Context, string, string) error { return nil }
func (m *recordingMutator) CloseConnection(context.Context, string) error     { return nil }
func (m *recordingMutator) CloseAllConnections(context.Context) error         { return nil }

func (m *recordingMutator) ApplyConfigPatch(_ context.Context, patch map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := make(map[string]any, len(patch))
	for k, v := range patch {
		copied[k] = v
	}
	m.patches = append(m.patches, copied)
	return m.err
}

func (m *recordingMutator) lastPatch() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.patches) == 0 {
		return nil
	}
	return m.patches[len(m.patches)-1]
}

type memoryPanel struct {
	dir    string
	path   string
	panels map[string]string // panelID → static root
	setups map[string]string // panelID → setup path
}

func (m memoryPanel) ActiveDir() (string, error) { return m.dir, nil }
func (m memoryPanel) SetupPath(host string) string {
	if m.path != "" {
		return m.path
	}
	// Mirror Zashboard-style same-origin setup deep-link (host may be host:port).
	hostname, port := host, ""
	if i := strings.LastIndex(host, ":"); i >= 0 && !strings.Contains(host[:i], ":") {
		hostname, port = host[:i], host[i+1:]
	}
	path := "/__mihari/panels/zashboard/#/setup?hostname=" + hostname + "&disableUpgrade=true"
	if port != "" {
		path = "/__mihari/panels/zashboard/#/setup?hostname=" + hostname + "&port=" + port + "&disableUpgrade=true"
	}
	return path
}
func (m memoryPanel) PanelDir(panelID string) (string, error) {
	if m.panels != nil {
		return m.panels[panelID], nil
	}
	return "", nil
}
func (m memoryPanel) SetupPathFor(panelID, host string) string {
	if m.setups != nil {
		if setup := m.setups[panelID]; setup != "" {
			return setup
		}
	}
	return "/__mihari/panels/" + panelID + "/#/setup?hostname=" + host
}

func newWebSocketController(t *testing.T, wantSecret, forbiddenWebCredential, responseHeaderSentinel string) *httptest.Server {
	t.Helper()

	type observedFrame struct {
		messageType websocket.MessageType
		data        []byte
	}
	var observed struct {
		sync.Mutex
		requests int
		path     string
		rawQuery string
		headers  http.Header
		frames   []observedFrame
		errors   []string
	}
	handlerDone := make(chan struct{})
	var handlerDoneOnce sync.Once

	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer handlerDoneOnce.Do(func() { close(handlerDone) })

		observed.Lock()
		observed.requests++
		observed.path = r.URL.Path
		observed.rawQuery = r.URL.RawQuery
		observed.headers = r.Header.Clone()
		observed.Unlock()

		w.Header().Set("X-Upstream-Response-Sentinel", responseHeaderSentinel)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			observed.Lock()
			observed.errors = append(observed.errors, "accept: "+err.Error())
			observed.Unlock()
			return
		}
		defer conn.CloseNow()

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		for range 2 {
			messageType, data, err := conn.Read(ctx)
			if err != nil {
				observed.Lock()
				observed.errors = append(observed.errors, "read: "+err.Error())
				observed.Unlock()
				return
			}
			observed.Lock()
			observed.frames = append(observed.frames, observedFrame{
				messageType: messageType,
				data:        append([]byte(nil), data...),
			})
			observed.Unlock()
			if err := conn.Write(ctx, messageType, data); err != nil {
				observed.Lock()
				observed.errors = append(observed.errors, "write: "+err.Error())
				observed.Unlock()
				return
			}
		}
	}))

	t.Cleanup(func() {
		controller.CloseClientConnections()
		controller.Close()

		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		select {
		case <-handlerDone:
		case <-timer.C:
			t.Error("WebSocket controller handler did not stop")
		}

		observed.Lock()
		defer observed.Unlock()
		if observed.requests != 1 {
			t.Errorf("controller requests = %d, want 1", observed.requests)
		}
		if observed.path != "/connections" {
			t.Errorf("controller path = %q, want /connections", observed.path)
		}
		if observed.rawQuery != "scope=active&format=full" {
			t.Errorf("controller query = %q, want scope=active&format=full", observed.rawQuery)
		}
		if observed.headers.Get("Authorization") != "Bearer "+wantSecret {
			t.Errorf("controller authorization was not the injected controller secret")
		}
		if cookie := observed.headers.Get("Cookie"); cookie != "" {
			t.Errorf("controller received browser cookie %q", cookie)
		}
		for name, values := range observed.headers {
			joined := strings.Join(values, "\n")
			if strings.Contains(joined, forbiddenWebCredential) {
				t.Errorf("controller header %q leaked browser web credential", name)
			}
			if !strings.EqualFold(name, "Authorization") && strings.Contains(joined, wantSecret) {
				t.Errorf("controller header %q leaked controller secret outside Authorization", name)
			}
		}
		if len(observed.errors) != 0 {
			t.Errorf("controller relay errors: %v", observed.errors)
		}

		wantFrames := []observedFrame{
			{messageType: websocket.MessageText, data: []byte(`{"command":"ping"}`)},
			{messageType: websocket.MessageBinary, data: []byte{0x00, 0x7f, 0x80, 0xff}},
		}
		if len(observed.frames) != len(wantFrames) {
			t.Errorf("controller frames = %d, want %d", len(observed.frames), len(wantFrames))
			return
		}
		for i, want := range wantFrames {
			got := observed.frames[i]
			if got.messageType != want.messageType {
				t.Errorf("controller frame %d type = %v, want %v", i, got.messageType, want.messageType)
			}
			if !bytes.Equal(got.data, want.data) {
				t.Errorf("controller frame %d data = %v, want %v", i, got.data, want.data)
			}
		}
	})

	return controller
}

type gatewayServeState struct {
	done chan struct{}
	err  error
}

func waitGatewayServeReady(t *testing.T, gateway *Server, serveState *gatewayServeState) string {
	t.Helper()

	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	retry := time.NewTicker(10 * time.Millisecond)
	defer retry.Stop()

	probe := func() string {
		addr := gateway.ListenAddr()
		if addr == "" || addr == "127.0.0.1:0" {
			return ""
		}
		probeCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		conn, err := (&net.Dialer{}).DialContext(probeCtx, "tcp", addr)
		if err != nil {
			return ""
		}
		if err := conn.Close(); err != nil {
			t.Errorf("close gateway readiness probe: %v", err)
		}
		return "http://" + addr
	}

	for {
		if base := probe(); base != "" {
			return base
		}
		select {
		case <-serveState.done:
			t.Fatalf("gateway stopped before becoming ready: %v", serveState.err)
		case <-retry.C:
		case <-deadline.C:
			t.Fatal("gateway did not become network-ready")
		}
	}
}

const (
	task5WebCredential    = "web-token-task5-aaaaaaaaaaaaaaaaaaaaaaaaaa"
	task5ControllerSecret = "controller-secret-task5-bbbbbbbbbbbbbbbbb"
)

type task5RoundTripFunc func(*http.Request) (*http.Response, error)

func (f task5RoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type task5ControllerState struct {
	started         chan struct{}
	accepted        chan struct{}
	done            chan struct{}
	relayErr        error
	relayContextErr error
	cancel          context.CancelFunc
}

type webSocketRelayJoinObserver struct {
	active        atomic.Int32
	handlerResult chan int32
}

func newWebSocketRelayJoinObserver() *webSocketRelayJoinObserver {
	return &webSocketRelayJoinObserver{handlerResult: make(chan int32, 1)}
}

func (o *webSocketRelayJoinObserver) relayStarted() {
	o.active.Add(1)
}

func (o *webSocketRelayJoinObserver) relayFinished() {
	o.active.Add(-1)
}

func (o *webSocketRelayJoinObserver) handlerFinished() {
	o.handlerResult <- o.active.Load()
}

func newTask5WebSocketController(
	t *testing.T,
	relay func(context.Context, *websocket.Conn) error,
) (*httptest.Server, *task5ControllerState) {
	t.Helper()

	controllerCtx, cancelController := context.WithCancel(context.Background())
	state := &task5ControllerState{
		started:  make(chan struct{}),
		accepted: make(chan struct{}),
		done:     make(chan struct{}),
		cancel:   cancelController,
	}
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(state.started)
		defer close(state.done)

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			state.relayErr = err
			return
		}
		defer conn.CloseNow()
		close(state.accepted)

		relayCtx, cancelRelay := context.WithTimeout(controllerCtx, 3*time.Second)
		defer cancelRelay()
		state.relayErr = relay(relayCtx, conn)
		state.relayContextErr = relayCtx.Err()
	}))
	t.Cleanup(controller.Close)
	t.Cleanup(func() {
		state.cancel()
		controller.CloseClientConnections()
		select {
		case <-state.started:
			waitDone(t, state.done, "WebSocket controller handler")
		default:
		}
	})
	return controller, state
}

func newTask5Gateway(t *testing.T, controllerURL string, transport http.RoundTripper) *Server {
	t.Helper()

	gateway, err := New(Options{
		Addr:             "127.0.0.1:0",
		Auth:             Authenticator{WebCredential: task5WebCredential, ControllerSecret: task5ControllerSecret},
		ControllerURL:    controllerURL,
		ControllerSecret: task5ControllerSecret,
		Transport:        transport,
	})
	if err != nil {
		t.Fatal(err)
	}
	return gateway
}

func serveWebSocketGateway(t *testing.T, gateway *Server) string {
	t.Helper()

	gatewayCtx, cancelGateway := context.WithCancel(context.Background())
	serveState := &gatewayServeState{done: make(chan struct{})}
	go func() {
		serveState.err = gateway.Serve(gatewayCtx)
		close(serveState.done)
	}()
	t.Cleanup(func() {
		cancelGateway()
		waitDone(t, serveState.done, "gateway")
		if serveState.err != nil {
			t.Errorf("serve gateway: %v", serveState.err)
		}
	})
	return waitGatewayServeReady(t, gateway, serveState)
}

func dialTask5GatewayStream(t *testing.T, base string) *websocket.Conn {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	header := http.Header{}
	header.Set("Authorization", "Bearer "+task5WebCredential)
	stream, response, err := websocket.Dial(
		ctx,
		"ws"+strings.TrimPrefix(base, "http")+"/connections?scope=active",
		&websocket.DialOptions{HTTPHeader: header},
	)
	if err != nil {
		t.Fatalf("dial gateway WebSocket: %v", err)
	}
	if response == nil || response.StatusCode != http.StatusSwitchingProtocols {
		stream.CloseNow()
		t.Fatalf("gateway handshake response = %v", response)
	}
	t.Cleanup(func() { stream.CloseNow() })
	return stream
}

func waitDone(t *testing.T, done <-chan struct{}, label string) {
	t.Helper()

	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
		return
	case <-timer.C:
		select {
		case <-done:
			return
		default:
			t.Fatalf("%s did not stop before its deadline", label)
		}
	}
}

func requireTask5NotDone(t *testing.T, done <-chan struct{}, label string) {
	t.Helper()

	select {
	case <-done:
		t.Fatalf("%s stopped before the shutdown trigger", label)
	default:
	}
}

func requireTask5PeerClose(t *testing.T, state *task5ControllerState) {
	t.Helper()

	if state.relayContextErr != nil {
		t.Fatalf("controller relay ended from its context instead of its peer: %v", state.relayContextErr)
	}
	if state.relayErr == nil {
		t.Fatal("controller relay returned no peer-close error")
	}
}

func waitTask5SessionCount(t *testing.T, gateway *Server, want int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if got := gateway.SessionCount(); got == want {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("gateway session count = %d, want %d: %v", gateway.SessionCount(), want, ctx.Err())
		}
	}
}

func TestGatewayWebSocketHandlerWaitsForBothRelaysAfterNormalClose(t *testing.T) {
	closeUpstream := make(chan struct{})
	controller, controllerState := newTask5WebSocketController(t, func(ctx context.Context, conn *websocket.Conn) error {
		select {
		case <-closeUpstream:
			return conn.Close(websocket.StatusNormalClosure, "")
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	observer := newWebSocketRelayJoinObserver()
	gateway := newTask5Gateway(t, controller.URL, nil)
	gateway.wsObserver = observer
	base := serveWebSocketGateway(t, gateway)
	stream := dialTask5GatewayStream(t, base)
	waitDone(t, controllerState.accepted, "upstream WebSocket acceptance")

	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelRead()
	readErr := make(chan error, 1)
	go func() {
		_, _, err := stream.Read(readCtx)
		readErr <- err
	}()
	close(closeUpstream)

	var activeAtHandlerFinish int32
	select {
	case activeAtHandlerFinish = <-observer.handlerResult:
	case <-readCtx.Done():
		t.Fatalf("gateway WebSocket handler did not finish: %v", readCtx.Err())
	}
	if activeAtHandlerFinish != 0 {
		t.Fatalf("gateway WebSocket handler finished with %d relay copier still running; want 0", activeAtHandlerFinish)
	}

	select {
	case err := <-readErr:
		if err == nil {
			t.Fatal("browser WebSocket remained open after normal upstream close")
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			t.Fatalf("browser read ended from its context instead of upstream close: %v", err)
		}
	case <-readCtx.Done():
		t.Fatalf("browser did not receive graceful close: %v", readCtx.Err())
	}
	waitDone(t, controllerState.done, "normal upstream close")
	if controllerState.relayErr != nil || controllerState.relayContextErr != nil {
		t.Fatalf("normal upstream close failed: relay=%v context=%v", controllerState.relayErr, controllerState.relayContextErr)
	}
}

func TestGatewayWebSocketUpstreamCloseStopsRelay(t *testing.T) {
	closeUpstream := make(chan struct{})
	controller, controllerState := newTask5WebSocketController(t, func(ctx context.Context, conn *websocket.Conn) error {
		select {
		case <-closeUpstream:
			conn.CloseNow()
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	gateway := newTask5Gateway(t, controller.URL, nil)
	base := serveWebSocketGateway(t, gateway)
	stream := dialTask5GatewayStream(t, base)
	waitDone(t, controllerState.accepted, "upstream WebSocket acceptance")
	waitTask5SessionCount(t, gateway, 1)
	requireTask5NotDone(t, controllerState.done, "upstream WebSocket")

	close(closeUpstream)
	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelRead()
	if _, _, err := stream.Read(readCtx); err == nil {
		t.Fatal("browser WebSocket remained readable after upstream close")
	}
	if err := readCtx.Err(); err != nil {
		t.Fatalf("browser WebSocket read ended from its deadline instead of upstream close: %v", err)
	}
	waitDone(t, controllerState.done, "upstream WebSocket")
	if controllerState.relayContextErr != nil {
		t.Fatalf("upstream WebSocket close ended from its relay context: %v", controllerState.relayContextErr)
	}
	if controllerState.relayErr != nil {
		t.Fatalf("upstream WebSocket shutdown: %v", controllerState.relayErr)
	}
	waitTask5SessionCount(t, gateway, 0)
}

func TestGatewayWebSocketClientCloseStopsUpstream(t *testing.T) {
	controller, controllerState := newTask5WebSocketController(t, func(ctx context.Context, conn *websocket.Conn) error {
		_, _, err := conn.Read(ctx)
		return err
	})
	gateway := newTask5Gateway(t, controller.URL, nil)
	base := serveWebSocketGateway(t, gateway)
	stream := dialTask5GatewayStream(t, base)
	waitDone(t, controllerState.accepted, "upstream WebSocket acceptance")
	waitTask5SessionCount(t, gateway, 1)
	requireTask5NotDone(t, controllerState.done, "upstream WebSocket")

	stream.CloseNow()
	waitDone(t, controllerState.done, "upstream WebSocket")
	requireTask5PeerClose(t, controllerState)
	waitTask5SessionCount(t, gateway, 0)
}

func TestGatewayWebSocketContextCancelReleasesBothSides(t *testing.T) {
	controller, controllerState := newTask5WebSocketController(t, func(ctx context.Context, conn *websocket.Conn) error {
		_, _, err := conn.Read(ctx)
		return err
	})
	gateway := newTask5Gateway(t, controller.URL, nil)

	requestCtx, cancelRequest := context.WithCancel(context.Background())
	t.Cleanup(cancelRequest)
	handlerDone := make(chan struct{})
	originalHandler := gateway.httpServer.Handler
	gateway.httpServer.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originalHandler.ServeHTTP(w, r.WithContext(requestCtx))
		close(handlerDone)
	})
	base := serveWebSocketGateway(t, gateway)
	stream := dialTask5GatewayStream(t, base)
	waitDone(t, controllerState.accepted, "upstream WebSocket acceptance")
	waitTask5SessionCount(t, gateway, 1)
	requireTask5NotDone(t, controllerState.done, "upstream WebSocket")

	cancelRequest()
	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelRead()
	if _, _, err := stream.Read(readCtx); err == nil {
		t.Fatal("browser WebSocket remained readable after relay context cancellation")
	}
	if err := readCtx.Err(); err != nil {
		t.Fatalf("browser WebSocket read ended from its deadline instead of relay cancellation: %v", err)
	}
	waitDone(t, controllerState.done, "upstream WebSocket")
	requireTask5PeerClose(t, controllerState)
	waitDone(t, handlerDone, "gateway WebSocket handler")
	waitTask5SessionCount(t, gateway, 0)
}

func TestGatewayWebSocketHandshakeFailuresAreSanitized(t *testing.T) {
	const upstreamBody = "upstream-private-diagnostic-task5"
	tests := []struct {
		name             string
		upstreamStatus   int
		transportFailure bool
		invalidURL       bool
		wantStatus       int
		wantBody         string
	}{
		{name: "upstream 401", upstreamStatus: http.StatusUnauthorized, wantStatus: http.StatusUnauthorized, wantBody: "upstream stream unavailable\n"},
		{name: "upstream 500", upstreamStatus: http.StatusInternalServerError, wantStatus: http.StatusInternalServerError, wantBody: "upstream stream unavailable\n"},
		{name: "controller unavailable", transportFailure: true, wantStatus: http.StatusBadGateway, wantBody: "upstream stream unavailable\n"},
		{name: "invalid controller URL", invalidURL: true, wantStatus: http.StatusBadGateway, wantBody: "bad gateway\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controllerURL := "http://controller.invalid"
			var transport http.RoundTripper
			if tt.upstreamStatus != 0 {
				controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("X-Upstream-Diagnostic", task5ControllerSecret+" "+upstreamBody)
					w.WriteHeader(tt.upstreamStatus)
					_, _ = io.WriteString(w, task5ControllerSecret+" "+upstreamBody)
				}))
				t.Cleanup(func() {
					controller.CloseClientConnections()
					controller.Close()
				})
				controllerURL = controller.URL
			}
			if tt.transportFailure {
				transport = task5RoundTripFunc(func(*http.Request) (*http.Response, error) {
					return nil, errors.New(task5ControllerSecret + " " + upstreamBody)
				})
			}

			gateway := newTask5Gateway(t, controllerURL, transport)
			if tt.invalidURL {
				gateway.ControllerURL = "%"
			}
			base := serveWebSocketGateway(t, gateway)
			dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancelDial()
			header := http.Header{}
			header.Set("Authorization", "Bearer "+task5WebCredential)
			stream, response, err := websocket.Dial(
				dialCtx,
				"ws"+strings.TrimPrefix(base, "http")+"/connections",
				&websocket.DialOptions{HTTPHeader: header},
			)
			if stream != nil {
				stream.CloseNow()
				t.Fatal("gateway WebSocket handshake unexpectedly succeeded")
			}
			if err == nil {
				t.Fatal("gateway WebSocket handshake returned no error")
			}
			if response == nil {
				t.Fatalf("gateway WebSocket handshake returned no HTTP response: %v", err)
			}
			body, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if readErr != nil {
				t.Fatal(readErr)
			}
			if response.StatusCode != tt.wantStatus {
				t.Fatalf("gateway handshake status = %d, want %d", response.StatusCode, tt.wantStatus)
			}
			if got := string(body); got != tt.wantBody {
				t.Fatalf("gateway handshake body = %q, want %q", got, tt.wantBody)
			}
			for _, sensitive := range []string{task5ControllerSecret, upstreamBody} {
				if bytes.Contains(body, []byte(sensitive)) {
					t.Fatalf("gateway handshake body leaked %q", sensitive)
				}
				if strings.Contains(err.Error(), sensitive) {
					t.Fatalf("gateway handshake error leaked %q", sensitive)
				}
				for name, values := range response.Header {
					if strings.Contains(strings.Join(values, "\n"), sensitive) {
						t.Fatalf("gateway handshake header %q leaked %q", name, sensitive)
					}
				}
			}
			waitTask5SessionCount(t, gateway, 0)
		})
	}
}

func TestGatewayWebSocketRelaysBidirectionallyAndInjectsOnlyControllerSecret(t *testing.T) {
	const webCredential = "web-token-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const controllerSecret = "controller-secret-bbbbbbbbbbbbbbbbbbbbbbbb"
	const upstreamResponseHeaderSentinel = "upstream-response-header-cccccccccccccccc"

	controller := newWebSocketController(t, controllerSecret, webCredential, upstreamResponseHeaderSentinel)
	gateway, err := New(Options{
		Addr:             "127.0.0.1:0",
		Auth:             Authenticator{WebCredential: webCredential, ControllerSecret: controllerSecret},
		ControllerURL:    controller.URL,
		ControllerSecret: controllerSecret,
	})
	if err != nil {
		t.Fatal(err)
	}

	base := serveWebSocketGateway(t, gateway)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	browser := &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	t.Cleanup(browser.CloseIdleConnections)
	streamURL := "ws" + strings.TrimPrefix(base, "http") + "/connections?scope=active&format=full"
	unauthenticatedCtx, cancelUnauthenticated := context.WithTimeout(context.Background(), 5*time.Second)
	unauthenticatedStream, unauthenticatedResponse, err := websocket.Dial(
		unauthenticatedCtx,
		streamURL,
		&websocket.DialOptions{HTTPClient: browser},
	)
	cancelUnauthenticated()
	if err == nil {
		unauthenticatedStream.CloseNow()
		t.Fatal("unauthenticated browser stream succeeded")
	}
	if unauthenticatedResponse == nil {
		t.Fatalf("unauthenticated browser stream returned no HTTP response: %v", err)
	}
	unauthenticatedBody, readErr := io.ReadAll(unauthenticatedResponse.Body)
	unauthenticatedResponse.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if unauthenticatedResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated stream status = %d, want %d", unauthenticatedResponse.StatusCode, http.StatusUnauthorized)
	}
	if bytes.Contains(unauthenticatedBody, []byte(controllerSecret)) {
		t.Fatal("controller secret leaked in unauthenticated response body")
	}
	for name, values := range unauthenticatedResponse.Header {
		if strings.Contains(strings.Join(values, "\n"), controllerSecret) {
			t.Fatalf("controller secret leaked in unauthenticated response header %q", name)
		}
	}

	loginValues := url.Values{
		"password": {webCredential},
		"next":     {"/connections?scope=active&format=full"},
	}
	loginCtx, cancelLogin := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancelLogin)
	loginRequest, err := http.NewRequestWithContext(
		loginCtx,
		http.MethodPost,
		base+AuthPath,
		strings.NewReader(loginValues.Encode()),
	)
	if err != nil {
		t.Fatal(err)
	}
	loginRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginResponse, err := browser.Do(loginRequest)
	if err != nil {
		t.Fatal(err)
	}
	loginBody, err := io.ReadAll(loginResponse.Body)
	loginResponse.Body.Close()
	cancelLogin()
	if err != nil {
		t.Fatal(err)
	}
	if loginResponse.StatusCode != http.StatusFound {
		t.Fatalf("login status = %d, want %d", loginResponse.StatusCode, http.StatusFound)
	}
	if bytes.Contains(loginBody, []byte(controllerSecret)) {
		t.Fatal("controller secret leaked in login response body")
	}
	for name, values := range loginResponse.Header {
		if strings.Contains(strings.Join(values, "\n"), controllerSecret) {
			t.Fatalf("controller secret leaked in login response header %q", name)
		}
	}

	baseURL, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	var sessionCookie *http.Cookie
	for _, cookie := range browser.Jar.Cookies(baseURL) {
		if cookie.Name == CookieName {
			sessionCookie = cookie
			break
		}
	}
	if sessionCookie == nil || sessionCookie.Value != webCredential {
		t.Fatalf("browser session cookie = %v", sessionCookie)
	}

	streamCtx, cancelStream := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancelStream)
	stream, handshakeResponse, err := websocket.Dial(streamCtx, streamURL, &websocket.DialOptions{
		HTTPClient: browser,
	})
	if err != nil {
		t.Fatalf("dial gateway stream: %v", err)
	}
	t.Cleanup(func() { stream.CloseNow() })
	if handshakeResponse == nil || handshakeResponse.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("gateway handshake response = %v", handshakeResponse)
	}
	if got := handshakeResponse.Header.Get("X-Upstream-Response-Sentinel"); got != "" {
		t.Fatalf("gateway handshake exposed upstream response header sentinel %q", got)
	}
	for name, values := range handshakeResponse.Header {
		if strings.Contains(strings.Join(values, "\n"), controllerSecret) {
			t.Fatalf("controller secret leaked in gateway handshake header %q", name)
		}
		if strings.Contains(strings.Join(values, "\n"), upstreamResponseHeaderSentinel) {
			t.Fatalf("upstream response header leaked through gateway handshake header %q", name)
		}
	}

	frames := []struct {
		messageType websocket.MessageType
		data        []byte
	}{
		{messageType: websocket.MessageText, data: []byte(`{"command":"ping"}`)},
		{messageType: websocket.MessageBinary, data: []byte{0x00, 0x7f, 0x80, 0xff}},
	}
	for i, frame := range frames {
		if err := stream.Write(streamCtx, frame.messageType, frame.data); err != nil {
			t.Fatalf("write browser frame %d: %v", i, err)
		}
		messageType, data, err := stream.Read(streamCtx)
		if err != nil {
			t.Fatalf("read browser frame %d: %v", i, err)
		}
		if messageType != frame.messageType {
			t.Errorf("browser frame %d type = %v, want %v", i, messageType, frame.messageType)
		}
		if !bytes.Equal(data, frame.data) {
			t.Errorf("browser frame %d data = %v, want %v", i, data, frame.data)
		}
		if bytes.Contains(data, []byte(controllerSecret)) {
			t.Errorf("controller secret leaked in browser frame %d", i)
		}
	}
}

func TestGatewayProxiesVersionWithAuthAndRejectsUpgrade(t *testing.T) {
	const webToken = "web-token-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const controllerSecret = "controller-secret-bbbbbbbbbbbbbbbbbbbbbbbb"
	var sawUpgrade bool
	var sawAuth string
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/upgrade" {
			sawUpgrade = true
			w.WriteHeader(http.StatusOK)
			return
		}
		sawAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"version":"v1"}`))
	}))
	defer controller.Close()

	uiDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(uiDir, "index.html"), []byte("<html>panel</html>"), 0o600); err != nil {
		t.Fatal(err)
	}

	gateway, err := New(Options{
		Addr:          "127.0.0.1:0",
		Auth:          Authenticator{WebCredential: webToken, ControllerSecret: controllerSecret},
		ControllerURL: controller.URL, ControllerSecret: controllerSecret,
		Panel: memoryPanel{dir: uiDir},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- gateway.Serve(ctx) }()
	// Wait until bound.
	deadline := time.Now().Add(2 * time.Second)
	var base string
	for time.Now().Before(deadline) {
		if addr := gateway.ListenAddr(); addr != "" && addr != "127.0.0.1:0" {
			base = "http://" + addr
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if base == "" {
		t.Fatal("gateway did not bind")
	}

	// Unauthenticated API → 401.
	resp, err := http.Get(base + "/version")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth status=%d", resp.StatusCode)
	}

	// Authenticated GET proxies with injected controller secret.
	req, _ := http.NewRequest(http.MethodGet, base+"/version", nil)
	req.Header.Set("Authorization", "Bearer "+webToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "v1") {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	if sawAuth != "Bearer "+controllerSecret {
		t.Fatalf("controller auth=%q", sawAuth)
	}
	if strings.Contains(string(body), controllerSecret) {
		t.Fatal("controller secret leaked to browser")
	}

	// POST /upgrade rejected without hitting controller.
	req, _ = http.NewRequest(http.MethodPost, base+"/upgrade", nil)
	req.Header.Set("Authorization", "Bearer "+webToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "managed_operation") {
		t.Fatalf("upgrade status=%d body=%s", resp.StatusCode, body)
	}
	if sawUpgrade {
		t.Fatal("upgrade reached controller")
	}

	// Static panel with cookie session.
	req, _ = http.NewRequest(http.MethodGet, base+"/", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: webToken})
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "panel") {
		t.Fatalf("static status=%d body=%s", resp.StatusCode, body)
	}

	// Credential-less module/script fetch (Vite crossorigin) must still get static bytes.
	if err := os.WriteFile(filepath.Join(uiDir, "index-BcsehOF5.js"), []byte("export default 1"), 0o600); err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequest(http.MethodGet, base+"/index-BcsehOF5.js", nil)
	req.Header.Set("Sec-Fetch-Dest", "script")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "export default") {
		t.Fatalf("credential-less script status=%d body=%s", resp.StatusCode, body)
	}
	// Unauthenticated document GET serves panel HTML (API remains gated); no login wall for SPA shells.
	req, _ = http.NewRequest(http.MethodGet, base+"/", nil)
	req.Header.Set("Sec-Fetch-Dest", "document")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "panel") {
		t.Fatalf("unauth document status=%d body=%s", resp.StatusCode, body)
	}
	// Missing .js must 404 as application error, not SPA index.html (wrong MIME for modules).
	resp, err = http.Get(base + "/missing-module.js")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing module status=%d", resp.StatusCode)
	}

	// One-shot token sets cookie and redirects to panel setup (no token, no :9090).
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err = client.Get(base + "/?token=" + webToken)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("token redirect status=%d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if strings.Contains(loc, "token=") {
		t.Fatalf("redirect kept token: %s", loc)
	}
	if !strings.Contains(loc, "/#/setup?") {
		t.Fatalf("token open should land on setup deep-link, got %q", loc)
	}
	if strings.Contains(loc, "9090") {
		t.Fatalf("setup must not target controller port: %s", loc)
	}
	if len(resp.Cookies()) == 0 || resp.Cookies()[0].Name != CookieName {
		t.Fatalf("cookies=%v", resp.Cookies())
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("gateway did not shut down")
	}
}

func TestGatewayServesInstalledPanelsConcurrentlyAtUIPaths(t *testing.T) {
	const webToken = "web-token-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const controllerSecret = "controller-secret-bbbbbbbbbbbbbbbbbbbbbbbb"
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version":"v1"}`))
	}))
	defer controller.Close()

	root := t.TempDir()
	zashDir := filepath.Join(root, "zashboard")
	metaDir := filepath.Join(root, "metacubexd")
	if err := os.MkdirAll(zashDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(metaDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zashDir, "index.html"), []byte("<html>zashboard</html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metaDir, "index.html"), []byte("<html>metacubexd</html>"), 0o600); err != nil {
		t.Fatal(err)
	}

	gateway, err := New(Options{
		Addr:          "127.0.0.1:0",
		Auth:          Authenticator{WebCredential: webToken, ControllerSecret: controllerSecret},
		ControllerURL: controller.URL, ControllerSecret: controllerSecret,
		Panel: memoryPanel{
			dir: zashDir,
			panels: map[string]string{
				"zashboard":  zashDir,
				"metacubexd": metaDir,
			},
			setups: map[string]string{
				"zashboard":  "/__mihari/panels/zashboard/#/setup?hostname=127.0.0.1:9191",
				"metacubexd": "/__mihari/panels/metacubexd/#/setup?hostname=127.0.0.1:9191&http=true",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = gateway.Serve(ctx) }()
	base := waitGatewayBase(t, gateway)

	for _, tc := range []struct {
		path string
		want string
	}{
		{"/__mihari/panels/zashboard/", "zashboard"},
		{"/__mihari/panels/metacubexd/", "metacubexd"},
		{"/", "zashboard"}, // default active remains available at root
	} {
		req, _ := http.NewRequest(http.MethodGet, base+tc.path, nil)
		req.AddCookie(&http.Cookie{Name: CookieName, Value: webToken})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), tc.want) {
			t.Fatalf("path=%s status=%d body=%s", tc.path, resp.StatusCode, body)
		}
	}

	// Opening metacubexd lands on metacubexd setup, not the default zashboard.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(base + "/__mihari/panels/metacubexd/?token=" + webToken)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "/__mihari/panels/metacubexd/") || strings.Contains(loc, "zashboard") || strings.Contains(loc, "token=") {
		t.Fatalf("open metacubexd location=%q", loc)
	}

	// Panel assets under /__mihari/panels/{id}/ load without a cookie (crossorigin omit).
	if err := os.WriteFile(filepath.Join(zashDir, "app.js"), []byte("console.log(1)"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zashDir, "manifest.webmanifest"), []byte(`{"name":"z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Vite base "/" index: rewrite root-absolute assets onto the panel mount.
	if err := os.WriteFile(filepath.Join(zashDir, "index.html"), []byte(
		`<html><script type="module" crossorigin src="/assets/index-BcsehOF5.js"></script>`+
			`<link rel="manifest" href="/manifest.webmanifest"><body>zashboard</body></html>`,
	), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/__mihari/panels/zashboard/app.js",
		"/__mihari/panels/zashboard/manifest.webmanifest",
		"/__mihari/panels/zashboard/index.html",
	} {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("path=%s status=%d body=%s", path, resp.StatusCode, body)
		}
		if strings.HasSuffix(path, "index.html") {
			if !strings.Contains(string(body), `/__mihari/panels/zashboard/assets/index-BcsehOF5.js`) {
				t.Fatalf("index missing rewritten asset path: %s", body)
			}
			if strings.Contains(string(body), `src="/assets/`) {
				t.Fatalf("index still has root-absolute asset: %s", body)
			}
		}
	}
	// Root-absolute asset with panel Referer resolves from that panel tree (not only active).
	if err := os.WriteFile(filepath.Join(metaDir, "only-meta.js"), []byte("meta"), 0o600); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, base+"/only-meta.js", nil)
	req.Header.Set("Referer", base+"/__mihari/panels/metacubexd/")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "meta" {
		t.Fatalf("referer panel asset status=%d body=%s", resp.StatusCode, body)
	}
	// API under the same origin still requires auth.
	resp, err = http.Get(base + "/version")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth api status=%d", resp.StatusCode)
	}
}

func TestRewriteRootAbsoluteURLs(t *testing.T) {
	in := []byte(`<script src="/assets/a.js"></script><link href="//cdn.example/x.css"><a href="/__mihari/panels/zashboard/ok">`)
	out := string(rewriteRootAbsoluteURLs(in, "/__mihari/panels/zashboard"))
	if !strings.Contains(out, `src="/__mihari/panels/zashboard/assets/a.js"`) {
		t.Fatalf("out=%s", out)
	}
	if !strings.Contains(out, `href="//cdn.example/x.css"`) {
		t.Fatalf("protocol-relative rewritten: %s", out)
	}
	if !strings.Contains(out, `href="/__mihari/panels/zashboard/ok"`) {
		t.Fatalf("existing mount broken: %s", out)
	}
}

func TestGatewayWrongControllerSecretDoesNotAuthorize(t *testing.T) {
	auth := Authenticator{
		WebCredential:    "web-token-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ControllerSecret: "controller-secret-bbbbbbbbbbbbbbbbbbbbbbbb",
	}
	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	req.Header.Set("Authorization", "Bearer "+auth.ControllerSecret)
	if auth.Authorized(req) {
		t.Fatal("controller secret must not authorize")
	}
}

func TestGatewayConfigPatchAllowlistTUN(t *testing.T) {
	const webToken = "web-token-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const controllerSecret = "controller-secret-bbbbbbbbbbbbbbbbbbbbbbbb"

	var gotConfigsPath bool
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/configs" {
			gotConfigsPath = true
			_, _ = w.Write([]byte(`{"mode":"rule","tun":{"enable":false}}`))
			return
		}
		// Mutations must not reach the controller.
		t.Errorf("unexpected controller hit: %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}))
	defer controller.Close()

	mutator := &recordingMutator{}
	gateway, err := New(Options{
		Addr:             "127.0.0.1:0",
		Auth:             Authenticator{WebCredential: webToken, ControllerSecret: controllerSecret},
		ControllerURL:    controller.URL,
		ControllerSecret: controllerSecret,
		Mutator:          mutator,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- gateway.Serve(ctx) }()
	base := waitGatewayBase(t, gateway)

	authHeader := func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+webToken)
		req.Header.Set("Content-Type", "application/json")
	}

	// PATCH {"tun":{"enable":true}} → mutator called, 204.
	req, _ := http.NewRequest(http.MethodPatch, base+"/configs", strings.NewReader(`{"tun":{"enable":true}}`))
	authHeader(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("tun enable status=%d body=%s", resp.StatusCode, body)
	}
	patch := mutator.lastPatch()
	if patch == nil {
		t.Fatal("ApplyConfigPatch not called")
	}
	tun, ok := patch["tun"].(map[string]any)
	if !ok {
		t.Fatalf("patch=%#v", patch)
	}
	if enable, _ := tun["enable"].(bool); !enable {
		t.Fatalf("tun=%#v", tun)
	}

	// PATCH with secret/controller → reject managed.
	for _, payload := range []string{
		`{"secret":"x"}`,
		`{"external-controller":"127.0.0.1:1"}`,
		`{"tun":{"enable":true},"secret":"x"}`,
		`{"mixed-port":7890}`,
		`{"bind-address":"*"}`,
		`{"external-ui":"/tmp"}`,
		`{"external-ui-name":"z"}`,
	} {
		req, _ = http.NewRequest(http.MethodPatch, base+"/configs", strings.NewReader(payload))
		authHeader(req)
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ = io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "managed_field") {
			t.Fatalf("managed payload %s status=%d body=%s", payload, resp.StatusCode, body)
		}
	}

	// PATCH with unknown key → reject unsupported.
	req, _ = http.NewRequest(http.MethodPatch, base+"/configs", strings.NewReader(`{"mode":"global"}`))
	authHeader(req)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "unsupported_mutation") {
		t.Fatalf("unknown key status=%d body=%s", resp.StatusCode, body)
	}

	// Empty body / empty object rejected.
	req, _ = http.NewRequest(http.MethodPatch, base+"/configs", strings.NewReader(`{}`))
	authHeader(req)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		t.Fatal("empty patch must not succeed")
	}

	// GET /configs still proxies read.
	req, _ = http.NewRequest(http.MethodGet, base+"/configs", nil)
	req.Header.Set("Authorization", "Bearer "+webToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"mode"`) {
		t.Fatalf("GET configs status=%d body=%s", resp.StatusCode, body)
	}
	if !gotConfigsPath {
		t.Fatal("GET /configs did not reach controller")
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("gateway did not shut down")
	}
}

func waitGatewayBase(t *testing.T, gateway *Server) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if addr := gateway.ListenAddr(); addr != "" && addr != "127.0.0.1:0" {
			return "http://" + addr
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("gateway did not bind")
	return ""
}
