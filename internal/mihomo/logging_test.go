package mihomo

import (
	"net/url"
	"testing"
)

func TestStreamLogs_RequestsDebugIndependentlyOfFileLevel(t *testing.T) {
	client := NewClient("http://127.0.0.1:1234", "fixture", nil)
	for _, kind := range []StreamKind{StreamLogs, StreamTraffic, StreamConnections, StreamMemory} {
		value, err := client.streamURL(kind)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := url.Parse(value)
		if err != nil {
			t.Fatal(err)
		}
		want := ""
		if kind == StreamLogs {
			want = "debug"
		}
		if parsed.Query().Get("level") != want {
			t.Fatalf("%s query=%s want level=%s", kind, parsed.RawQuery, want)
		}
	}
}
