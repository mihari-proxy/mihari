package runtime

import "testing"

func TestSystemProxyTarget_WildcardUsesLoopback(t *testing.T) {
	for _, test := range []struct{ listen, host string }{
		{"0.0.0.0:9190", "127.0.0.1"},
		{"[::]:9190", "::1"},
		{"192.0.2.1:9190", "192.0.2.1"},
	} {
		_, host, port, err := resolveSystemProxyTarget(test.listen)
		if err != nil || host != test.host || port != 9190 {
			t.Errorf("%s: host=%s port=%d err=%v", test.listen, host, port, err)
		}
	}
}
