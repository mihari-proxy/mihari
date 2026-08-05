//go:build linux

package sysproxy

import (
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
)

// gsettingsSchema is the GNOME proxy schema. Mihari drives the desktop proxy
// through gsettings, which is honoured by GNOME apps and many GTK toolkits.
const gsettingsSchema = "org.gnome.system.proxy"

// enable sets the GNOME proxy to manual mode pointing at host:port for HTTP,
// HTTPS and SOCKS.
func enable(host string, port int) error {
	if err := requireGsettings(); err != nil {
		return err
	}
	p := strconv.Itoa(port)
	steps := [][]string{
		{"set", gsettingsSchema + ".http", "host", host},
		{"set", gsettingsSchema + ".http", "port", p},
		{"set", gsettingsSchema + ".https", "host", host},
		{"set", gsettingsSchema + ".https", "port", p},
		{"set", gsettingsSchema + ".socks", "host", host},
		{"set", gsettingsSchema + ".socks", "port", p},
		{"set", gsettingsSchema, "ignore-hosts", ignoreHostsArray()},
		{"set", gsettingsSchema, "mode", "manual"},
	}
	for _, s := range steps {
		if err := gsettings(s...); err != nil {
			return err
		}
	}
	return nil
}

// disable returns the GNOME proxy to "none".
func disable() error {
	if err := requireGsettings(); err != nil {
		return err
	}
	return gsettings("set", gsettingsSchema, "mode", "none")
}

// get reports the current GNOME proxy state.
func get() (State, error) {
	if err := requireGsettings(); err != nil {
		return State{}, err
	}
	mode, err := gsettingsGet(gsettingsSchema, "mode")
	if err != nil {
		return State{}, err
	}
	if mode != "manual" {
		return State{}, nil
	}
	host, err := gsettingsGet(gsettingsSchema+".http", "host")
	if err != nil {
		return State{}, err
	}
	port, err := gsettingsGet(gsettingsSchema+".http", "port")
	if err != nil {
		return State{}, err
	}
	st := State{Enabled: true}
	if host != "" {
		st.Server = net.JoinHostPort(host, port)
	}
	return st, nil
}

// requireGsettings verifies the gsettings tool is present, returning a clear
// error otherwise so headless or non-GNOME systems fail informatively.
func requireGsettings() error {
	if _, err := exec.LookPath("gsettings"); err != nil {
		return fmt.Errorf("sysproxy: gsettings not found; system proxy is only supported on GNOME-based desktops")
	}
	return nil
}

// gsettings runs the tool with args, surfacing any error output.
func gsettings(args ...string) error {
	out, err := exec.Command("gsettings", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sysproxy: gsettings %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// gsettingsGet reads a single key, trimming the surrounding quotes gsettings
// prints for string values.
func gsettingsGet(schema, key string) (string, error) {
	out, err := exec.Command("gsettings", "get", schema, key).Output()
	if err != nil {
		return "", fmt.Errorf("sysproxy: gsettings get %s %s: %w", schema, key, err)
	}
	return strings.Trim(strings.TrimSpace(string(out)), "'"), nil
}
