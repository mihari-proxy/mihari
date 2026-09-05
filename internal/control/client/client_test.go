package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestStatusDecodesSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"schema":"mihari/v1","protocol_version":"v1","daemon_version":"dev","revision":3,"health":"ok","started_at":"1970-01-01T00:01:40Z"}`))
	}))
	defer server.Close()

	control := NewHTTP(server.URL, "token", server.Client())
	status, err := control.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Revision != 3 || status.Health != "ok" {
		t.Fatalf("status=%#v", status)
	}
}

func TestSetToken_EmptyIsNoOp(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"schema":"mihari/v1","protocol_version":"v1","daemon_version":"dev","revision":1,"health":"ok","started_at":"1970-01-01T00:01:40Z"}`))
	}))
	defer server.Close()

	control := NewHTTP(server.URL, "token", server.Client())
	control.SetToken("")
	if _, err := control.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSetToken_UpdatesBearer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer new-token" {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"schema":"mihari/v1","protocol_version":"v1","daemon_version":"dev","revision":1,"health":"ok","started_at":"1970-01-01T00:01:40Z"}`))
	}))
	defer server.Close()

	control := NewHTTP(server.URL, "old-token", server.Client())
	control.SetToken("new-token")
	if _, err := control.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSetToken_ConcurrentRequests(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if authorization := request.Header.Get("Authorization"); authorization != "Bearer first-token" && authorization != "Bearer second-token" {
			t.Errorf("authorization=%q", authorization)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"schema":"mihari/v1","protocol_version":"v1","daemon_version":"dev","revision":1,"health":"ok","started_at":"1970-01-01T00:01:40Z"}`)),
		}, nil
	})
	control := NewHTTP("http://mihari", "first-token", &http.Client{Transport: transport})

	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		for range 1000 {
			control.SetToken("second-token")
			control.SetToken("first-token")
		}
	}()
	go func() {
		defer workers.Done()
		for range 1000 {
			if _, err := control.Status(context.Background()); err != nil {
				t.Errorf("Status: %v", err)
				return
			}
		}
	}()
	workers.Wait()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestStatusDecodesAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte(`{"schema":"mihari.error/v1","error":{"code":"permission_denied","message":"denied"}}`))
	}))
	defer server.Close()

	control := NewHTTP(server.URL, "bad", server.Client())
	_, err := control.Status(context.Background())
	var apiError protocol.APIError
	if !errors.As(err, &apiError) {
		t.Fatalf("error type=%T value=%v", err, err)
	}
	if apiError.Code != protocol.CodePermissionDenied || apiError.Message != "denied" {
		t.Fatalf("api error=%#v", apiError)
	}
}
