package sysproxy

import "testing"

func TestParseGetWebProxy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		out  string
		want State
	}{
		{
			name: "enabled with server and port",
			out: "" +
				"Enabled: Yes\n" +
				"Server: 127.0.0.1\n" +
				"Port: 9190\n" +
				"Authenticated Proxy Enabled: 0\n",
			want: State{Enabled: true, Server: "127.0.0.1:9190"},
		},
		{
			name: "disabled",
			out: "" +
				"Enabled: No\n" +
				"Server: 127.0.0.1\n" +
				"Port: 9190\n",
			want: State{Enabled: false, Server: ""},
		},
		{
			name: "enabled yes case insensitive",
			out: "" +
				"Enabled: yes\n" +
				"Server: localhost\n" +
				"Port: 8080\n",
			want: State{Enabled: true, Server: "localhost:8080"},
		},
		{
			name: "enabled ipv6 uses join host port",
			out: "" +
				"Enabled: Yes\n" +
				"Server: ::1\n" +
				"Port: 7890\n",
			want: State{Enabled: true, Server: "[::1]:7890"},
		},
		{
			name: "enabled missing port leaves server empty",
			out: "" +
				"Enabled: Yes\n" +
				"Server: 127.0.0.1\n",
			want: State{Enabled: true, Server: ""},
		},
		{
			name: "empty output",
			out:  "",
			want: State{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseGetWebProxy(tt.out)
			if got != tt.want {
				t.Fatalf("parseGetWebProxy() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
