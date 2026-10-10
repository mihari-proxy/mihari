package client

import "testing"

func TestNew_OrdinaryRequestUsesOneMinute(t *testing.T) {
	c := New("unused-endpoint", "token")
	if c.http.Timeout != DefaultTimeout {
		t.Fatalf("timeout = %s", c.http.Timeout)
	}
}
