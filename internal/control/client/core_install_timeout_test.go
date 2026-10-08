package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func invokeCoreInstall(ctx context.Context, c *Client, reinstall bool) error {
	if reinstall {
		_, err := c.ReinstallCore(ctx, protocol.MutationRequest{OperationID: "core-reinstall"})
		return err
	}
	_, err := c.InstallCore(ctx, protocol.MutationRequest{OperationID: "core-install"})
	return err
}

func TestCoreInstallTimeout_OutlivesOrdinaryBudget(t *testing.T) {
	for _, reinstall := range []bool{false, true} {
		for _, credential := range []bool{false, true} {
			t.Run(strings.Join([]string{map[bool]string{false: "install", true: "reinstall"}[reinstall], map[bool]string{false: "static", true: "credential"}[credential]}, "/"), func(t *testing.T) {
				started, ordinaryExpired := make(chan struct{}), make(chan struct{})
				var sawDeadline atomic.Bool
				transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
					if r.URL.Path == "/v1/core" {
						<-r.Context().Done()
						close(ordinaryExpired)
						return nil, r.Context().Err()
					}
					close(started)
					select {
					case <-ordinaryExpired:
					case <-r.Context().Done():
						return nil, r.Context().Err()
					}
					if _, ok := r.Context().Deadline(); ok {
						sawDeadline.Store(true)
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader(`{"schema":"mihari/v1","version":"v9","updated":true}`)),
					}, nil
				})
				hc := &http.Client{Transport: transport, Timeout: 30 * time.Millisecond}
				c := NewHTTP("http://mihari", "token", hc)
				var loads atomic.Int32
				if credential {
					c = NewHTTPWithCredentialProvider("http://mihari", timeoutCredentialFunc(func(context.Context) (string, error) {
						loads.Add(1)
						return "token", nil
					}), hc)
				}
				done := make(chan error, 1)
				go func() { done <- invokeCoreInstall(context.Background(), c, reinstall) }()
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("install request was not dispatched")
				}
				if _, err := c.Core(context.Background()); err == nil {
					t.Error("ordinary request lost its timeout")
				}
				select {
				case err := <-done:
					if err != nil {
						t.Errorf("core install cut off by ordinary budget: %v", err)
					}
				case <-time.After(time.Second):
					t.Fatal("core install hung")
				}
				if sawDeadline.Load() {
					t.Error("core install added its own deadline")
				}
				if hc.Timeout != 30*time.Millisecond {
					t.Error("shared client was modified")
				}
				if credential && loads.Load() != 2 {
					t.Errorf("credential reads = %d", loads.Load())
				}
			})
		}
	}
}

func TestCoreInstallTimeout_ParentCancelWins(t *testing.T) {
	for _, reinstall := range []bool{false, true} {
		for _, cancelExplicitly := range []bool{false, true} {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			c := NewHTTP("http://mihari", "token", &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if cancelExplicitly {
					cancel()
				}
				<-r.Context().Done()
				return nil, r.Context().Err()
			})})
			err := invokeCoreInstall(ctx, c, reinstall)
			if !errors.Is(err, ctx.Err()) || ctx.Err() == nil {
				t.Errorf("parent cancellation lost: reinstall=%v explicit=%v err=%v", reinstall, cancelExplicitly, err)
			}
			cancel()
		}
	}
}
