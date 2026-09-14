package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

type failingRequestJSON struct{ cause error }

func (v failingRequestJSON) MarshalJSON() ([]byte, error) { return nil, v.cause }

func TestRuntimeOutcome_RequestEncodingPreservesCause(t *testing.T) {
	cause := errors.New("marshal local configuration /private/token")
	c := NewHTTP("http://mihari", "token", &http.Client{})
	var output any
	outcome := c.doRuntimeOutcome(context.Background(), http.MethodPost, "/v1/config", failingRequestJSON{cause}, &output, maxControlResponseSize)
	var api protocol.APIError
	if !errors.Is(outcome.err, cause) || !errors.As(outcome.err, &api) || api.Code != protocol.CodeInternal || api.Message != "encode control request" || outcome.err.Error() != api.Message {
		t.Fatalf("encoding cause or public error changed: %v", outcome.err)
	}
}

func TestStatus_LocalCredentialFailureHasOneFileOwner(t *testing.T) {
	cause := errors.New("credential read /private/control.token failed")
	capture := new(diagnosticCapture)
	c := NewHTTPWithCredentialProvider("http://mihari", &sequenceProvider{err: cause}, &http.Client{})
	if err := c.SetDiagnosticReporter(capture.report); err != nil {
		t.Fatal(err)
	}
	_, err := c.Status(context.Background())
	records, _ := capture.snapshot()
	if len(records) != 1 || records[0].Event != "request_failed" || records[0].Level != slog.LevelError || !errors.Is(records[0].Err, cause) {
		t.Fatalf("credential cause or read owner missing: %+v", records)
	}
	if !diagnostics.AlreadyReported(err) || !errors.Is(err, cause) || err.Error() != "local control operation failed" {
		t.Fatalf("public response or owner marker changed: %v", err)
	}
}

type closeFailureBody struct {
	io.Reader
	closes int
	err    error
}

func (b *closeFailureBody) Close() error { b.closes++; return b.err }

func TestRuntimeOutcome_ResponseCloseOwnedOnce(t *testing.T) {
	for _, tc := range []struct {
		name, body           string
		status               int
		localFailure, remote bool
	}{
		{"success", "{}", http.StatusOK, false, false},
		{"decode failure", "{", http.StatusOK, true, false},
		{"bad error envelope", "{", http.StatusBadGateway, true, false},
		{"remote error", `{"schema":"mihari.error/v1","error":{"code":"data_failure","message":"daemon failed"}}`, http.StatusBadGateway, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			closeCause := errors.New("close response /private/body")
			body := &closeFailureBody{Reader: strings.NewReader(tc.body), err: closeCause}
			capture := new(diagnosticCapture)
			c := NewHTTP("http://mihari", "token", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: body}, nil
			})})
			if err := c.SetDiagnosticReporter(capture.report); err != nil {
				t.Fatal(err)
			}
			var output any
			outcome := c.doRuntimeOutcome(context.Background(), http.MethodGet, "/v1/status", nil, &output, maxControlResponseSize)
			if body.closes != 1 {
				t.Fatalf("Close calls=%d want=1", body.closes)
			}
			records, _ := capture.snapshot()
			if tc.localFailure {
				var syntax *json.SyntaxError
				if !errors.Is(outcome.err, closeCause) || (!errors.As(outcome.err, &syntax) && !errors.Is(outcome.err, io.ErrUnexpectedEOF)) {
					t.Fatalf("primary or close cause lost: %v", outcome.err)
				}
				if len(records) != 0 {
					t.Fatal("close duplicated before primary owner")
				}
			} else {
				if len(records) != 1 || records[0].Level != slog.LevelWarn || !errors.Is(records[0].Err, closeCause) {
					t.Fatalf("independent close warning lost: %+v", records)
				}
				if !tc.remote && outcome.err != nil {
					t.Fatalf("successful response changed: %v", outcome.err)
				}
			}
			if outcome.remoteEnvelope != tc.remote {
				t.Fatal("remote/local ownership changed")
			}
			if tc.remote && outcome.err.Error() != "daemon failed" {
				t.Fatal("remote public response changed")
			}
		})
	}
}
