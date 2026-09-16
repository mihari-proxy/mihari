package diagnostics

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestCapture_OriginalTextAndLiteralTruncationMarker(t *testing.T) {
	const raw = "token=fixture-token https://user:password@example.test/sub?token=fixture-token\n/private/settings.yaml [truncated]"
	got := Capture(errors.New(raw))
	if got.Text != raw || got.Truncated || got.Reason != "" {
		t.Fatalf("capture changed original text or inferred a limit from content: %+v", got)
	}
}

func TestCapture_ByteLimitIsExplicitAndRetainsLeaf(t *testing.T) {
	got := Capture(&os.PathError{Op: strings.Repeat("界", MaxBytes), Path: "/fixture/settings", Err: io.ErrUnexpectedEOF})
	if !got.Truncated || got.Reason != "byte_limit" {
		t.Fatalf("missing structured byte limit: truncated=%v reason=%q", got.Truncated, got.Reason)
	}
	if len(got.Text) > MaxBytes || !utf8.ValidString(got.Text) || !strings.Contains(got.Text, "unexpected EOF") {
		t.Fatal("bounded text lost a leaf or exceeded its UTF-8 budget")
	}
}

func TestCapture_GraphLimitIsExplicit(t *testing.T) {
	err := error(errors.New("original leaf"))
	for range MaxDepth + 2 {
		err = fmt.Errorf("context: %w", err)
	}
	got := Capture(err)
	if !got.Truncated || got.Reason != "graph_limit" {
		t.Fatalf("missing structured graph limit: %+v", got)
	}
}

func TestCapture_CycleIsExplicit(t *testing.T) {
	got := Capture(&cyclicError{})
	if !got.Truncated || got.Reason != "cycle" {
		t.Fatalf("missing cycle diagnostic: %+v", got)
	}
}

func TestCapture_UpstreamLimitIsExplicit(t *testing.T) {
	err := HandshakeError("GET controller", &http.Response{
		StatusCode: http.StatusBadGateway, ContentLength: 4096,
		Body: io.NopCloser(strings.NewReader("original upstream body")),
	}, errors.New("handshake failed"))
	got := Capture(err)
	if !got.Truncated || got.Reason != "upstream_limit" || !strings.Contains(got.Text, "original upstream body") {
		t.Fatalf("missing upstream limit: %+v", got)
	}
}

func TestCapture_JoinedCausesAndNil(t *testing.T) {
	if got := Capture(nil); got.Text != "" || got.Truncated {
		t.Fatalf("nil capture = %+v", got)
	}
	got := Capture(errors.Join(errors.New("first token=fixture-a"), errors.New("second password=fixture-b")))
	for _, want := range []string{"first token=fixture-a", "second password=fixture-b"} {
		if !strings.Contains(got.Text, want) || got.Truncated {
			t.Fatalf("joined capture = %+v", got)
		}
	}
}

func TestCapture_AttachedRemoteSnapshotRetainsOriginal(t *testing.T) {
	err := protocol.APIError{Code: protocol.CodeNetworkFailure, Message: "request failed", Diagnostic: &protocol.Diagnostic{
		ID: "remote:1", Detail: "HTTP 403 token=fixture\noriginal response", State: protocol.DiagnosticAvailable, Truncated: true, TruncationReason: "upstream_limit",
	}}
	got := Capture(err)
	if got.Text != err.Diagnostic.Detail || !got.Truncated || got.Reason != "upstream_limit" {
		t.Fatalf("remote original was reclassified: %+v", got)
	}
}
