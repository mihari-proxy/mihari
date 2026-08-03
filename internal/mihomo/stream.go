package mihomo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/LeeShunEE/mihari/internal/control/protocol"
	"github.com/coder/websocket"
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
		return protocol.APIError{Code: protocol.CodeInternal, Message: "invalid mihomo controller address"}
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+c.secret)
	connection, response, err := websocket.Dial(ctx, streamURL, &websocket.DialOptions{
		HTTPClient: c.http,
		HTTPHeader: header,
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		if response != nil && (response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden) {
			return protocol.APIError{Code: protocol.CodePermissionDenied, Message: "mihomo authentication failed"}
		}
		return protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "mihomo stream is unavailable"}
	}
	defer connection.CloseNow()
	connection.SetReadLimit(maxStreamMessageSize)

	for {
		messageType, message, err := connection.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				return nil
			}
			if errors.Is(err, websocket.ErrMessageTooBig) {
				return protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihomo stream message is too large"}
			}
			return protocol.APIError{Code: protocol.CodeUpstreamFailure, Message: "mihomo stream closed unexpectedly"}
		}
		if messageType != websocket.MessageText && messageType != websocket.MessageBinary {
			continue
		}
		if !json.Valid(message) {
			return protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihomo stream returned invalid JSON"}
		}
		if err := receive(json.RawMessage(message)); err != nil {
			return err
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
