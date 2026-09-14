package mihomo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/coder/websocket"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

const maxStreamMessageSize = 1 << 20

type StreamKind string

const (
	StreamTraffic     StreamKind = "traffic"
	StreamMemory      StreamKind = "memory"
	StreamLogs        StreamKind = "logs"
	StreamConnections StreamKind = "connections"
)

func (c *Client) Stream(ctx context.Context, kind StreamKind, receive func(json.RawMessage) error) error {
	if !validStreamKind(kind) {
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "unsupported mihomo stream"}
	}
	if receive == nil {
		return protocol.APIError{Code: protocol.CodeInvalidArgument, Message: "stream receiver is required"}
	}
	streamURL, err := c.streamURL(kind)
	if err != nil {
		return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeInternal, Message: "invalid mihomo controller address"}, &diagnostics.HTTPError{Operation: "mihomo stream", Phase: "request", Cause: err})
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+c.secret)
	connection, response, err := websocket.Dial(ctx, streamURL, &websocket.DialOptions{
		HTTPClient: c.http,
		HTTPHeader: header,
	})
	if err != nil {
		detail := diagnostics.HandshakeError("mihomo stream "+string(kind), response, err)
		detail.URL = streamURL
		if ctx.Err() != nil {
			c.reportStreamCancellation(ctx, detail)
			return nil
		}
		var details map[string]any
		if response != nil {
			details = map[string]any{"status": response.StatusCode}
		}
		if response != nil && (response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden) {
			return diagnostics.Wrap(protocol.APIError{Code: protocol.CodePermissionDenied, Message: "mihomo authentication failed", Details: details}, detail)
		}
		return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "mihomo stream is unavailable", Details: details}, detail)
	}
	defer func() { c.reportStreamClose(ctx, connection.CloseNow()) }()
	connection.SetReadLimit(maxStreamMessageSize)

	for {
		messageType, message, err := connection.Read(ctx)
		if err != nil {
			detail := &diagnostics.HTTPError{Operation: "mihomo stream " + string(kind), URL: streamURL, Phase: "read", Cause: err}
			if ctx.Err() != nil {
				c.reportStreamCancellation(ctx, detail)
				return nil
			}
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				return nil
			}
			if errors.Is(err, websocket.ErrMessageTooBig) {
				return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihomo stream message is too large"}, detail)
			}
			return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "mihomo stream closed unexpectedly"}, detail)
		}
		if messageType != websocket.MessageText && messageType != websocket.MessageBinary {
			continue
		}
		var validated json.RawMessage
		if err := json.Unmarshal(message, &validated); err != nil {
			return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihomo stream returned invalid JSON"}, &diagnostics.HTTPError{Operation: "mihomo stream " + string(kind), URL: streamURL, Phase: "decode", Body: diagnostics.HTTPBody(message), Cause: err})
		}
		if err := receive(json.RawMessage(message)); err != nil {
			return err
		}
	}
}

func (c *Client) reportStreamClose(ctx context.Context, err error) {
	if c.reporter == nil || expectedWebSocketClose(err) {
		return
	}
	if level, emit := diagnostics.FailureLevel(ctx, err); emit {
		c.reporter(ctx, diagnostics.Record{Component: "mihomo", Event: "stream.close.failed", Level: min(level, slog.LevelWarn), Err: err})
	}
}

func expectedWebSocketClose(err error) bool {
	if err == nil || errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return true
	}
	status := websocket.CloseStatus(err)
	return status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway
}

// Stream cancellation retains the existing nil result, so the adapter owns
// this diagnostic instead of passing an error to its caller for reporting.
func (c *Client) reportStreamCancellation(ctx context.Context, err error) {
	if c.reporter != nil {
		if level, emit := diagnostics.FailureLevel(ctx, err); emit {
			c.reporter(ctx, diagnostics.Record{Component: "mihomo", Event: "stream.canceled", Level: level, Err: err})
		}
	}
}

func validStreamKind(kind StreamKind) bool {
	switch kind {
	case StreamTraffic, StreamMemory, StreamLogs, StreamConnections:
		return true
	default:
		return false
	}
}

func (c *Client) streamURL(kind StreamKind) (string, error) {
	parsed, err := url.Parse(c.baseURL)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", errors.New("unsupported controller URL scheme")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + string(kind)
	return parsed.String(), nil
}
