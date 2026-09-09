package client

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/logging"
)

func TestSystemProxyDiagnostic_ClientMetadata(t *testing.T) {
	for _, enable := range []bool{true, false} {
		name := "system_proxy.disable"
		if enable {
			name = "system_proxy.enable"
		}
		assertBusinessClientMetadata(t, name, func(ctx context.Context, c *Client) error {
			req := protocol.SystemProxyMutationRequest{OperationID: "business-id"}
			if enable {
				_, err := c.EnableSystemProxy(ctx, req)
				return err
			}
			_, err := c.DisableSystemProxy(ctx, req)
			return err
		})
	}
}

func assertBusinessClientMetadata(t *testing.T, name string, invoke func(context.Context, *Client) error) {
	t.Helper()
	capture := new(diagnosticCapture)
	c := NewHTTP("http://mihari", "token", &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		op, _ := logging.OperationFromContext(r.Context())
		if op != (logging.OperationMetadata{ID: "business-id", Name: name}) {
			t.Errorf("transport metadata=%#v", op)
		}
		return &http.Response{StatusCode: http.StatusInternalServerError, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"schema":"mihari.error/v1","error":{"code":"upstream_failure","message":"upstream unavailable"}}`))}, nil
	})})
	if err := c.SetDiagnosticReporter(capture.report); err != nil {
		t.Fatal(err)
	}
	if err := invoke(logging.WithOperation(context.Background(), logging.OperationMetadata{ID: "stale", Name: "other"}), c); err == nil {
		t.Fatal("missing remote failure")
	}
	records, ops := capture.snapshot()
	if len(records) != 2 || len(ops) != 2 {
		t.Fatalf("records=%d operations=%d", len(records), len(ops))
	}
	for _, op := range ops {
		if op != (logging.OperationMetadata{ID: "business-id", Name: name}) {
			t.Fatalf("metadata=%#v", op)
		}
	}
}

func TestTunDiagnostic_ClientMetadata(t *testing.T) {
	for _, enable := range []bool{true, false} {
		name := "tun.disable"
		if enable {
			name = "tun.enable"
		}
		assertBusinessClientMetadata(t, name, func(ctx context.Context, c *Client) error {
			req := protocol.TunMutationRequest{OperationID: "business-id"}
			if enable {
				_, err := c.EnableTun(ctx, req)
				return err
			}
			_, err := c.DisableTun(ctx, req)
			return err
		})
	}
}

func TestGeoIPDiagnostic_ClientMetadata(t *testing.T) {
	assertBusinessClientMetadata(t, "geoip.update", func(ctx context.Context, c *Client) error {
		_, err := c.UpdateGeoIP(ctx, protocol.MutationRequest{OperationID: "business-id"})
		return err
	})
}

func TestPanelDiagnostic_ClientMetadata(t *testing.T) {
	t.Run("install", func(t *testing.T) {
		assertBusinessClientMetadata(t, "panel.install", func(ctx context.Context, c *Client) error {
			_, err := c.InstallPanel(ctx, "business-secret", protocol.PanelInstallRequest{OperationID: "business-id"})
			return err
		})
	})
	t.Run("update", func(t *testing.T) {
		assertBusinessClientMetadata(t, "panel.update", func(ctx context.Context, c *Client) error {
			_, err := c.UpdatePanel(ctx, "business-secret", protocol.MutationRequest{OperationID: "business-id"})
			return err
		})
	})
	t.Run("activate", func(t *testing.T) {
		assertBusinessClientMetadata(t, "panel.activate", func(ctx context.Context, c *Client) error {
			_, err := c.ActivatePanel(ctx, "business-secret", protocol.MutationRequest{OperationID: "business-id"})
			return err
		})
	})
	t.Run("rollback", func(t *testing.T) {
		assertBusinessClientMetadata(t, "panel.rollback", func(ctx context.Context, c *Client) error {
			_, err := c.RollbackPanel(ctx, "business-secret", protocol.MutationRequest{OperationID: "business-id"})
			return err
		})
	})
	t.Run("uninstall", func(t *testing.T) {
		assertBusinessClientMetadata(t, "panel.uninstall", func(ctx context.Context, c *Client) error {
			_, err := c.UninstallPanel(ctx, "business-secret", protocol.MutationRequest{OperationID: "business-id"})
			return err
		})
	})
	t.Run("reinstall", func(t *testing.T) {
		assertBusinessClientMetadata(t, "panel.reinstall", func(ctx context.Context, c *Client) error {
			_, err := c.ReinstallPanel(ctx, "business-secret", protocol.MutationRequest{OperationID: "business-id"})
			return err
		})
	})
}

func TestProviderDiagnostic_ClientMetadata(t *testing.T) {
	assertBusinessClientMetadata(t, "rule_provider.refresh", func(ctx context.Context, c *Client) error {
		_, err := c.UpdateRuleProvider(ctx, "business-secret", protocol.MutationRequest{OperationID: "business-id"})
		return err
	})
}
