package client

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/coder/websocket"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
)

type sequenceProvider struct {
	calls int
	value string
	err   error
}

type failNextWriteConn struct {
	net.Conn
	fail *atomic.Bool
}

func (c failNextWriteConn) Write(p []byte) (int, error) {
	if c.fail.CompareAndSwap(true, false) {
		return 0, io.ErrClosedPipe
	}
	return c.Conn.Write(p)
}

func TestProvider_MutationWriteFailureIsNotRetried(t *testing.T) {
	for _, withBody := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "json"}[withBody], func(t *testing.T) {
			var requests, dials atomic.Int32
			var fail atomic.Bool
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					t.Error(err)
				}
				if _, err := io.WriteString(w, `{}`); err != nil {
					t.Error(err)
				}
			}))
			defer s.Close()
			tr := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				dials.Add(1)
				conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
				if err != nil {
					return nil, err
				}
				return failNextWriteConn{conn, &fail}, nil
			}}
			defer tr.CloseIdleConnections()
			p := &sequenceProvider{value: "current"}
			c := NewHTTP(s.URL, "", &http.Client{Transport: tr})
			c.provider = p
			if _, err := c.Status(context.Background()); err != nil {
				t.Fatal(err)
			}
			fail.Store(true)
			var err error
			if withBody {
				_, err = c.UpdateLogging(context.Background(), protocol.LoggingUpdateRequest{})
			} else {
				_, err = c.OpenWebGUI(context.Background(), "")
			}
			if err == nil || dials.Load() != 1 || requests.Load() != 1 || p.calls != 2 {
				t.Fatalf("mutation retried: err=%v dials=%d requests=%d loads=%d", err, dials.Load(), requests.Load(), p.calls)
			}
		})
	}
}

func TestProvider_AuthenticationHintPreservesEnvelope(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			want := protocol.APIError{Code: protocol.CodePermissionDenied, Message: "denied", Details: map[string]any{"scope": "control"}}
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				if err := json.NewEncoder(w).Encode(protocol.NewError(want.Code, want.Message, want.Details)); err != nil {
					t.Error(err)
				}
			}))
			defer s.Close()
			c := NewHTTP(s.URL, "", s.Client())
			c.provider = &sequenceProvider{value: "current"}
			for _, call := range []func() error{func() error { _, e := c.Status(context.Background()); return e }, func() error {
				return c.Stream(context.Background(), "logs", func(protocol.StreamEvent) error { return nil })
			}} {
				err := call()
				var got protocol.APIError
				if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
					t.Fatalf("envelope changed: %v", err)
				}
				var hint interface{ Hint() string }
				has := errors.As(err, &hint)
				if has != (status == http.StatusUnauthorized) {
					t.Fatalf("hint present=%v for HTTP %d", has, status)
				}
				if has && !strings.Contains(hint.Hint(), "restart") {
					t.Fatal("restart advice missing")
				}
			}
		})
	}
}

func TestProvider_RejectsRedirectWithoutReplay(t *testing.T) {
	for _, call := range []string{"status", "mutation", "stream"} {
		t.Run(call, func(t *testing.T) {
			var requests atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path == "/redirected" {
					if _, err := io.WriteString(w, `{}`); err != nil {
						t.Error(err)
					}
					return
				}
				w.Header().Set("Location", "/redirected")
				w.WriteHeader(http.StatusTemporaryRedirect)
			}))
			defer s.Close()
			p := &sequenceProvider{value: "current"}
			c := NewHTTP(s.URL, "", s.Client())
			c.provider = p
			var err error
			switch call {
			case "status":
				_, err = c.Status(context.Background())
			case "mutation":
				_, err = c.OpenWebGUI(context.Background(), "")
			case "stream":
				err = c.Stream(context.Background(), "logs", func(protocol.StreamEvent) error { return nil })
			}
			if err == nil || requests.Load() != 1 || p.calls != 1 {
				t.Fatalf("redirect replay: error=%v requests=%d loads=%d", err, requests.Load(), p.calls)
			}
		})
	}
}

func TestProvider_RetainsLoadedCredentialsInProcessRedactor(t *testing.T) {
	p := &sequenceProvider{value: strings.Repeat("a", 64)}
	c := NewHTTP("http://mihari", "", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, os.ErrNotExist })})
	c.provider = p
	r := logging.NewRedactor()
	if err := c.SetRedactor(r); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{strings.Repeat("a", 64), strings.Repeat("b", 64)} {
		p.value = token
		_, _ = c.Status(context.Background())
	}
	r.ReplaceExact([]string{"replacement-secret"})
	for _, token := range []string{strings.Repeat("a", 64), strings.Repeat("b", 64)} {
		if r.String("before"+token+"after") != "before***after" {
			t.Fatal("used credential leaked after secret update")
		}
	}
	if err := c.SetRedactor(logging.NewRedactor()); err == nil {
		t.Fatal("allowed replacing history after requests started")
	}
	c2 := NewHTTP("http://mihari", "", http.DefaultClient)
	if err := c2.SetRedactor(nil); err == nil {
		t.Fatal("accepted nil process redactor")
	}
}

func TestProvider_NilAdapterProviderRefusesWithoutHTTP(t *testing.T) {
	c := NewHTTPWithCredentialProvider("http://mihari", nil, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("missing provider reached transport")
		return nil, os.ErrPermission
	})})
	_, err := c.Status(context.Background())
	assertControlCode(t, err, protocol.CodeInvalidArgument)
}

func TestProvider_ReadFailureNeverUsesPreviousToken(t *testing.T) {
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer current" {
			t.Error("used stale credential")
		}
		if _, err := io.WriteString(w, `{}`); err != nil {
			t.Error(err)
		}
	}))
	defer s.Close()
	p := &sequenceProvider{err: os.ErrNotExist}
	c := NewHTTP(s.URL, "cached", s.Client())
	c.provider = p
	_, err := c.Status(context.Background())
	assertControlCode(t, err, protocol.CodeDaemonUnavailable)
	p.err, p.value = nil, "current"
	if _, err := c.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	p.err = os.ErrPermission
	_, err = c.Core(context.Background())
	assertControlCode(t, err, protocol.CodePermissionDenied)
	if p.calls != 3 || requests.Load() != 1 {
		t.Fatalf("loads=%d requests=%d", p.calls, requests.Load())
	}
}

func assertControlCode(t *testing.T, err error, want protocol.ErrorCode) {
	t.Helper()
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != want {
		t.Fatalf("error=%v, want code %s", err, want)
	}
}

func TestClient_LocalErrorBoundary(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want protocol.ErrorCode
	}{
		{"missing", os.ErrNotExist, protocol.CodeDaemonUnavailable},
		{"refused", syscall.ECONNREFUSED, protocol.CodeDaemonUnavailable},
		{"denied", os.ErrPermission, protocol.CodePermissionDenied},
		{"badpath", os.ErrInvalid, protocol.CodeInvalidArgument},
		{"io", io.ErrUnexpectedEOF, protocol.CodeDataFailure},
		{"busy", protocol.APIError{Code: protocol.CodeInvalidState, Message: "busy"}, protocol.CodeInvalidState},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, call := range []func(*Client) error{
				func(c *Client) error { _, e := c.Status(context.Background()); return e },
				func(c *Client) error { _, e := c.Core(context.Background()); return e },
				func(c *Client) error {
					return c.Stream(context.Background(), "logs", func(protocol.StreamEvent) error { return nil })
				},
			} {
				c := NewHTTP("http://mihari", "static", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, tc.err })})
				assertControlCode(t, call(c), tc.want)
			}
		})
	}
}

func TestProvider_StreamReconnectLoadsFreshToken(t *testing.T) {
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		want := "Bearer first"
		if n == 2 {
			want = "Bearer second"
		}
		if r.Header.Get("Authorization") != want {
			t.Error("stream used stale credential")
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"schema":"mihari/v1","stream":"logs"}`)); err != nil {
			t.Error(err)
		}
	}))
	defer s.Close()
	p := &sequenceProvider{value: "first"}
	c := NewHTTP(s.URL, "cached", s.Client())
	c.provider = p
	stop := errors.New("received")
	for range 2 {
		err := c.Stream(context.Background(), "logs", func(protocol.StreamEvent) error { return stop })
		if !errors.Is(err, stop) {
			t.Fatalf("stream: %v", err)
		}
		p.value = "second"
	}
	if p.calls != 2 || requests.Load() != 2 {
		t.Fatalf("loads=%d requests=%d", p.calls, requests.Load())
	}
}

func (p *sequenceProvider) Load(context.Context) (string, error) {
	p.calls++
	return p.value, p.err
}

func TestProvider_EachStatusAndRESTRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		want := "Bearer first"
		if n > 1 {
			want = "Bearer second"
		}
		if r.Header.Get("Authorization") != want {
			t.Errorf("request %d did not use current credential", n)
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"revision": n}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	p := &sequenceProvider{value: "first"}
	c := NewHTTPWithCredentialProvider(server.URL, p, server.Client())
	status, err := c.Status(context.Background())
	if err != nil || status.Revision != 1 {
		t.Fatalf("status failed: %v", err)
	}
	p.value = "second"
	if _, err := c.Core(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.WebGUI(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.OpenWebGUI(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UpdateLogging(context.Background(), protocol.LoggingUpdateRequest{}); err != nil {
		t.Fatal(err)
	}
	if p.calls != 5 || requests.Load() != 5 {
		t.Fatalf("loads=%d requests=%d, want five each", p.calls, requests.Load())
	}
}
