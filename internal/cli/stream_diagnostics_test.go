package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	controlclient "github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
)

type streamOutputFailure struct{}

func (streamOutputFailure) Write([]byte) (int, error) { return 0, errors.New("private-output-token") }

func TestStreamCLI_DiagnosticInjectionPreservesOutputContracts(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		fail bool
		want string
		exit int
	}{
		{name: "one text", args: []string{"traffic"}, want: "{\"up\":1,\"down\":2}\n", exit: ExitOK},
		{name: "one json", args: []string{"traffic", "--json"}, want: "{\"schema\":\"mihari/v1\",\"stream\":\"traffic\",\"data\":{\"up\":1,\"down\":2}}\n", exit: ExitOK},
		{name: "follow json", args: []string{"traffic", "--json", "--follow"}, want: "{\"schema\":\"mihari/v1\",\"stream\":\"traffic\",\"data\":{\"up\":1,\"down\":2}}\n{\"schema\":\"mihari/v1\",\"stream\":\"traffic\",\"data\":{\"up\":3,\"down\":4}}\n", exit: ExitOK},
		{name: "text writer failure", args: []string{"traffic"}, fail: true, exit: ExitDaemonUnavailable},
		{name: "json writer failure", args: []string{"traffic", "--json", "--follow"}, fail: true, exit: ExitDaemonUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.CloseNow()
				for _, raw := range []string{`{"schema":"mihari/v1","stream":"traffic","data":{"up":1,"down":2}}`, `{"schema":"mihari/v1","stream":"traffic","data":{"up":3,"down":4}}`} {
					if err := conn.Write(r.Context(), websocket.MessageText, []byte(raw)); err != nil {
						return
					}
				}
				_ = conn.Close(websocket.StatusNormalClosure, "done")
			}))
			defer server.Close()
			c := controlclient.NewHTTP(server.URL, "private-output-token", server.Client())
			var logs, stdout, stderr bytes.Buffer
			level := new(slog.LevelVar)
			level.Set(slog.LevelDebug)
			redactor := logging.NewRedactor("private-output-token")
			if err := c.SetDiagnosticReporter(logging.NewDiagnosticReporter(slog.New(logging.NewJSONHandler(&logs, level, "cli", redactor)), redactor)); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var writer io.Writer = &stdout
			if tc.fail {
				writer = streamOutputFailure{}
			}
			exit := Execute(ctx, tc.args, writer, &stderr, Dependencies{RuntimeClient: c})
			if exit != tc.exit || stdout.String() != tc.want {
				t.Fatalf("exit=%d stdout=%q", exit, stdout.String())
			}
			if logs.Len() != 0 {
				t.Fatalf("callback/sentinel mislabeled as transport: %s", logs.String())
			}
			if !tc.fail && stderr.Len() != 0 {
				t.Fatalf("stderr=%q", stderr.String())
			}
			if tc.fail {
				if strings.Contains(stderr.String(), "private-output-token") {
					t.Fatal("writer cause leaked")
				}
				if strings.Contains(tc.name, "json") {
					var envelope protocol.ErrorEnvelope
					decoder := json.NewDecoder(&stderr)
					if err := decoder.Decode(&envelope); err != nil {
						t.Fatal(err)
					}
					if envelope.Schema != "mihari.error/v1" || envelope.Error.Code != protocol.CodeDaemonUnavailable || envelope.Error.Message != "daemon is unavailable" {
						t.Fatalf("envelope=%+v", envelope)
					}
					var extra any
					if err := decoder.Decode(&extra); err != io.EOF {
						t.Fatal("error envelope polluted")
					}
				} else if stderr.String() != "Error: daemon is unavailable\n" {
					t.Fatalf("stderr=%q", stderr.String())
				}
			}
		})
	}
}
