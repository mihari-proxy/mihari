package platform

import "testing"

func TestTCPAddressFamiliesAndLocalDestinations(t *testing.T) {
	for _, test := range []struct{ listen, dial, network string }{
		{"0.0.0.0:9190", "127.0.0.1:9190", "tcp4"},
		{"[::]:9190", "[::1]:9190", "tcp6"},
		{"192.0.2.1:9190", "192.0.2.1:9190", "tcp4"},
		{"127.0.0.1:9190", "127.0.0.1:9190", "tcp4"},
		{"invalid", "invalid", "tcp"},
	} {
		if got := TCPDialAddress(test.listen); got != test.dial {
			t.Errorf("%s: dial=%s want %s", test.listen, got, test.dial)
		}
		if got := TCPNetwork(test.listen); got != test.network {
			t.Errorf("%s: network=%s want %s", test.listen, got, test.network)
		}
	}
}
