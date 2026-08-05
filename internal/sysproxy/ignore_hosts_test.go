package sysproxy

import "testing"

func TestIgnoreHostsArray(t *testing.T) {
	t.Parallel()

	got := ignoreHostsArray()
	want := "['localhost', '127.0.0.1', '::1']"
	if got != want {
		t.Fatalf("ignoreHostsArray() = %q, want %q", got, want)
	}
}
