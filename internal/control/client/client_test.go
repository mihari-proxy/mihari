package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
