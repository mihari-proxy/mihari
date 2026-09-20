package app

import "testing"

func TestDecodeTUIBytes_ProxySettings(t *testing.T) {
	for _, body := range []string{
		`{"schema":"mihari.tui-preferences/v1","connections_columns":["host"]}`,
		`{"schema":"mihari.tui-preferences/v1","connections_columns":["host"],"proxies":{"extra_latency":false,"auto_latency_test":true}}`,
	} {
		if err := decodeTUIBytes([]byte(body)); err != nil {
			t.Fatalf("valid preferences rejected: %v", err)
		}
	}
	if err := decodeTUIBytes([]byte(`{"schema":"mihari.tui-preferences/v1","connections_columns":["host"],"proxies":{"unknown":true}}`)); err == nil {
		t.Fatal("unknown proxy setting accepted")
	}
}
