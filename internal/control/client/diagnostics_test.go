package client

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"github.com/mihari-proxy/mihari/internal/logging"
)

type diagnosticCapture struct {
	mu       sync.Mutex
	records  []diagnostics.Record
	contexts []logging.OperationMetadata
}

func (c *diagnosticCapture) report(ctx context.Context, record diagnostics.Record) {
	c.mu.Lock()
	defer c.mu.Unlock()
	operation, _ := logging.OperationFromContext(ctx)
	c.records = append(c.records, record)
	c.contexts = append(c.contexts, operation)
}

func (c *diagnosticCapture) snapshot() ([]diagnostics.Record, []logging.OperationMetadata) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]diagnostics.Record(nil), c.records...), append([]logging.OperationMetadata(nil), c.contexts...)
}

func TestUpdateLogging_LocalResponseFailureReportsBoundRequestOperation(t *testing.T) {
	capture := new(diagnosticCapture)
	client := NewHTTP("http://mihari", "token", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`not json`))}, nil
	})})
	if err := client.SetDiagnosticReporter(capture.report); err != nil {
		t.Fatal(err)
	}

	stale := logging.WithOperation(context.Background(), logging.OperationMetadata{ID: "stale", Name: "other.operation"})
	_, err := client.UpdateLogging(stale, protocol.LoggingUpdateRequest{OperationID: "logging-op"})
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure {
		t.Fatalf("error=%v", err)
	}

	records, operations := capture.snapshot()
	if len(records) != 2 {
		t.Fatalf("records=%d want 2", len(records))
	}
	if records[0].Level != slog.LevelDebug || records[1].Level != slog.LevelError {
		t.Fatalf("levels=%v,%v want debug,error", records[0].Level, records[1].Level)
	}
	for _, operation := range operations {
		if operation.ID != "logging-op" || operation.Name != "logging.update" {
			t.Fatalf("operation=%#v", operation)
		}
	}
}

func TestUpdateLogging_RemoteDataFailureIsDebugResponse(t *testing.T) {
	capture := new(diagnosticCapture)
	client := NewHTTP("http://mihari", "token", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusInternalServerError, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"schema":"mihari.error/v1","error":{"code":"data_failure","message":"daemon failed"}}`))}, nil
	})})
	if err := client.SetDiagnosticReporter(capture.report); err != nil {
		t.Fatal(err)
	}

	_, err := client.UpdateLogging(context.Background(), protocol.LoggingUpdateRequest{OperationID: "logging-op"})
	var apiError protocol.APIError
	if !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure || apiError.Message != "daemon failed" {
		t.Fatalf("error=%v", err)
	}
	records, operations := capture.snapshot()
	if len(records) != 2 || records[0].Level != slog.LevelDebug || records[1].Level != slog.LevelDebug {
		t.Fatalf("records=%#v", records)
	}
	if operations[1] != (logging.OperationMetadata{ID: "logging-op", Name: "logging.update"}) {
		t.Fatalf("operation=%#v", operations[1])
	}
}

func TestUpdateLogging_TransportFailureKeepsDiagnosticCause(t *testing.T) {
	capture := new(diagnosticCapture)
	cause := errors.New("dial local control")
	client := NewHTTP("http://mihari", "token", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, cause
	})})
	if err := client.SetDiagnosticReporter(capture.report); err != nil {
		t.Fatal(err)
	}

	_, err := client.UpdateLogging(context.Background(), protocol.LoggingUpdateRequest{OperationID: "offline"})
	assertControlCode(t, err, protocol.CodeDataFailure)
	records, operations := capture.snapshot()
	if len(records) != 2 || records[1].Level != slog.LevelError || !errors.Is(records[1].Err, cause) {
		t.Fatalf("records=%#v", records)
	}
	if operations[1] != (logging.OperationMetadata{ID: "offline", Name: "logging.update"}) {
		t.Fatalf("operation=%#v", operations[1])
	}
}

func TestUpdateLogging_InvalidErrorEnvelopeIsLocalFailure(t *testing.T) {
	capture := new(diagnosticCapture)
	client := NewHTTP("http://mihari", "token", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusInternalServerError, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`not an envelope`))}, nil
	})})
	if err := client.SetDiagnosticReporter(capture.report); err != nil {
		t.Fatal(err)
	}

	_, err := client.UpdateLogging(context.Background(), protocol.LoggingUpdateRequest{OperationID: "logging-op"})
	assertControlCode(t, err, protocol.CodeDataFailure)
	records, _ := capture.snapshot()
	if len(records) != 2 || records[1].Level != slog.LevelError {
		t.Fatalf("records=%#v", records)
	}
}

func TestUpdateLogging_ResponseLimitAndAuthenticationEnvelopeRemainCompatible(t *testing.T) {
	t.Run("response limit", func(t *testing.T) {
		capture := new(diagnosticCapture)
		client := NewHTTP("http://mihari", "token", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("x", maxControlResponseSize+1)))}, nil
		})})
		if err := client.SetDiagnosticReporter(capture.report); err != nil {
			t.Fatal(err)
		}
		_, err := client.UpdateLogging(context.Background(), protocol.LoggingUpdateRequest{OperationID: "too-large"})
		assertControlCode(t, err, protocol.CodeDataFailure)
		records, operations := capture.snapshot()
		if len(records) != 2 || records[1].Level != slog.LevelError || operations[1].ID != "too-large" {
			t.Fatalf("records=%#v operations=%#v", records, operations)
		}
	})

	t.Run("valid unauthorized envelope", func(t *testing.T) {
		capture := new(diagnosticCapture)
		client := NewHTTPWithCredentialProvider("http://mihari", &sequenceProvider{value: "token"}, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusUnauthorized, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"schema":"mihari.error/v1","error":{"code":"permission_denied","message":"denied"}}`))}, nil
		})})
		if err := client.SetDiagnosticReporter(capture.report); err != nil {
			t.Fatal(err)
		}
		_, err := client.UpdateLogging(context.Background(), protocol.LoggingUpdateRequest{OperationID: "unauthorized"})
		assertControlCode(t, err, protocol.CodePermissionDenied)
		var hint interface{ Hint() string }
		if !errors.As(err, &hint) || !strings.Contains(hint.Hint(), "restart") {
			t.Fatalf("authentication hint=%v", err)
		}
		records, _ := capture.snapshot()
		if len(records) != 2 || records[1].Level != slog.LevelDebug {
			t.Fatalf("records=%#v", records)
		}
	})

	t.Run("invalid unauthorized envelope", func(t *testing.T) {
		capture := new(diagnosticCapture)
		client := NewHTTPWithCredentialProvider("http://mihari", &sequenceProvider{value: "token"}, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusUnauthorized, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`not an envelope`))}, nil
		})})
		if err := client.SetDiagnosticReporter(capture.report); err != nil {
			t.Fatal(err)
		}

		_, err := client.UpdateLogging(context.Background(), protocol.LoggingUpdateRequest{OperationID: "invalid-unauthorized"})
		var apiError protocol.APIError
		if !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure || apiError.Message != "invalid control error response" {
			t.Fatalf("error=%v", err)
		}
		var hint interface{ Hint() string }
		if errors.As(err, &hint) {
			t.Fatalf("invalid envelope acquired authentication hint: %v", err)
		}
		records, operations := capture.snapshot()
		if len(records) != 2 || records[0].Level != slog.LevelDebug || records[1].Level != slog.LevelError {
			t.Fatalf("records=%#v", records)
		}
		if operations[1] != (logging.OperationMetadata{ID: "invalid-unauthorized", Name: "logging.update"}) {
			t.Fatalf("operation=%#v", operations[1])
		}
	})
}

func TestSetDiagnosticReporter_OnlyBeforeFirstRequest(t *testing.T) {
	capture := new(diagnosticCapture)
	client := NewHTTP("http://mihari", "token", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"schema":"mihari/v1","protocol_version":"v1","daemon_version":"dev","health":"ok","started_at":"1970-01-01T00:01:40Z"}`))}, nil
	})})
	if err := client.SetDiagnosticReporter(capture.report); err != nil {
		t.Fatalf("reporter before request: %v", err)
	}
	if _, err := client.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	if records, _ := capture.snapshot(); len(records) != 0 {
		t.Fatalf("non-logging request reported diagnostics: %#v", records)
	}
	err := client.SetDiagnosticReporter(func(context.Context, diagnostics.Record) {})
	assertControlCode(t, err, protocol.CodeInvalidState)

	other := NewHTTP("http://mihari", "token", http.DefaultClient)
	if err := other.SetDiagnosticReporter(nil); err != nil {
		t.Fatalf("nil reporter before request: %v", err)
	}
}

func TestUpdateLogging_ConcurrentRequestsKeepDiagnosticOperationIDsSeparate(t *testing.T) {
	capture := new(diagnosticCapture)
	client := NewHTTP("http://mihari", "token", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"schema":"mihari/v1","revision":1,"level":"info","max_size_mb":10,"max_files":3}`))}, nil
	})})
	if err := client.SetDiagnosticReporter(capture.report); err != nil {
		t.Fatal(err)
	}

	var workers sync.WaitGroup
	for _, id := range []string{"logging-one", "logging-two"} {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if _, err := client.UpdateLogging(context.Background(), protocol.LoggingUpdateRequest{OperationID: id}); err != nil {
				t.Errorf("UpdateLogging(%q): %v", id, err)
			}
		}()
	}
	workers.Wait()

	_, operations := capture.snapshot()
	counts := make(map[string]int)
	for _, operation := range operations {
		if operation.Name != "logging.update" {
			t.Fatalf("operation=%#v", operation)
		}
		counts[operation.ID]++
	}
	if counts["logging-one"] != 2 || counts["logging-two"] != 2 {
		t.Fatalf("operation counts=%#v", counts)
	}
}

func TestSubscriptionMutations_BindDiagnosticOperationMetadata(t *testing.T) {
	tests := []struct {
		name      string
		operation logging.OperationMetadata
		invoke    func(*Client) error
	}{
		{
			name: "add", operation: logging.OperationMetadata{ID: "add-op", Name: "subscription.add"},
			invoke: func(client *Client) error {
				_, err := client.AddSubscription(context.Background(), protocol.SubscriptionAddRequest{OperationID: "add-op", Name: "main", URL: "https://example.test/private"})
				return err
			},
		},
		{
			name: "refresh", operation: logging.OperationMetadata{ID: "refresh-op", Name: "subscription.refresh"},
			invoke: func(client *Client) error {
				_, err := client.RefreshSubscription(context.Background(), "one", protocol.MutationRequest{OperationID: "refresh-op"})
				return err
			},
		},
		{
			name: "use", operation: logging.OperationMetadata{ID: "use-op", Name: "subscription.use"},
			invoke: func(client *Client) error {
				_, err := client.UseSubscription(context.Background(), "one", protocol.MutationRequest{OperationID: "use-op"})
				return err
			},
		},
		{
			name: "enabled", operation: logging.OperationMetadata{ID: "enabled-op", Name: "subscription.enabled"},
			invoke: func(client *Client) error {
				_, err := client.SetSubscriptionEnabled(context.Background(), "one", protocol.SubscriptionEnabledRequest{OperationID: "enabled-op", Enabled: true})
				return err
			},
		},
		{
			name: "update", operation: logging.OperationMetadata{ID: "update-op", Name: "subscription.set"},
			invoke: func(client *Client) error {
				_, err := client.UpdateSubscription(context.Background(), "one", protocol.SubscriptionUpdateRequest{OperationID: "update-op"})
				return err
			},
		},
		{
			name: "remove", operation: logging.OperationMetadata{ID: "remove-op", Name: "subscription.remove"},
			invoke: func(client *Client) error {
				_, err := client.RemoveSubscription(context.Background(), "one", protocol.MutationRequest{OperationID: "remove-op"})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			capture := new(diagnosticCapture)
			client := NewHTTP("http://mihari", "token", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`not json`))}, nil
			})})
			if err := client.SetDiagnosticReporter(capture.report); err != nil {
				t.Fatal(err)
			}
			if err := test.invoke(client); err == nil {
				t.Fatal("mutation unexpectedly succeeded")
			}
			records, operations := capture.snapshot()
			if len(records) != 2 || records[0].Level != slog.LevelDebug || records[1].Level != slog.LevelError {
				t.Fatalf("records=%#v", records)
			}
			if operations[0] != test.operation || operations[1] != test.operation {
				t.Fatalf("operations=%#v want=%#v", operations, test.operation)
			}
		})
	}
}

func TestCoreMutations_BindDiagnosticOperationMetadata(t *testing.T) {
	tests := []struct {
		name      string
		response  string
		operation logging.OperationMetadata
		invoke    func(context.Context, *Client) error
	}{
		{
			name: "install", response: `{"schema":"mihari/v1","operation_id":"install-op","version":"v1.19.0","updated":true}`,
			operation: logging.OperationMetadata{ID: "install-op", Name: "core.install"},
			invoke: func(ctx context.Context, client *Client) error {
				_, err := client.InstallCore(ctx, protocol.MutationRequest{OperationID: "install-op"})
				return err
			},
		},
		{
			name: "restart", response: `{"schema":"mihari/v1","operation_id":"restart-op","revision":4}`,
			operation: logging.OperationMetadata{ID: "restart-op", Name: "core.restart"},
			invoke: func(ctx context.Context, client *Client) error {
				_, err := client.RestartCore(ctx, protocol.MutationRequest{OperationID: "restart-op"})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			capture := new(diagnosticCapture)
			client := NewHTTP("http://mihari", "token", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.response))}, nil
			})})
			if err := client.SetDiagnosticReporter(capture.report); err != nil {
				t.Fatal(err)
			}
			stale := logging.WithOperation(context.Background(), logging.OperationMetadata{ID: "stale", Name: "other.operation"})

			if err := test.invoke(stale, client); err != nil {
				t.Fatal(err)
			}

			records, operations := capture.snapshot()
			if len(records) != 2 || records[0].Level != slog.LevelDebug || records[1].Level != slog.LevelDebug {
				t.Fatalf("records=%#v", records)
			}
			if operations[0] != test.operation || operations[1] != test.operation {
				t.Fatalf("operations=%#v want=%#v", operations, test.operation)
			}
		})
	}
}
