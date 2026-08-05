package sysproxy

import (
	"net"
	"strings"
)

// parseGetWebProxy extracts the enabled flag and server from networksetup
// -getwebproxy output, whose lines look like "Enabled: Yes", "Server: 127.0.0.1"
// and "Port: 9190".
func parseGetWebProxy(out string) State {
	var st State
	var server, port string
	for _, line := range strings.Split(out, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch strings.TrimSpace(key) {
		case "Enabled":
			st.Enabled = strings.EqualFold(val, "Yes")
		case "Server":
			server = val
		case "Port":
			port = val
		}
	}
	if st.Enabled && server != "" && port != "" {
		st.Server = net.JoinHostPort(server, port)
	}
	return st
}
