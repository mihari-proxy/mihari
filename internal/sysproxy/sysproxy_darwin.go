//go:build darwin

package sysproxy

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// enable points every enabled network service at host:port for web (HTTP) and
// secure web (HTTPS) traffic via networksetup.
func enable(host string, port int) error {
	services, err := networkServices()
	if err != nil {
		return err
	}
	if len(services) == 0 {
		return fmt.Errorf("sysproxy: no active network services found")
	}
	p := strconv.Itoa(port)
	var firstErr error
	for _, svc := range services {
		for _, cmd := range [][]string{
			{"-setwebproxy", svc, host, p},
			{"-setsecurewebproxy", svc, host, p},
			{"-setwebproxystate", svc, "on"},
			{"-setsecurewebproxystate", svc, "on"},
		} {
			if err := networksetup(cmd...); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// disable turns the web and secure-web proxies off on every network service.
func disable() error {
	services, err := networkServices()
	if err != nil {
		return err
	}
	var firstErr error
	for _, svc := range services {
		for _, cmd := range [][]string{
			{"-setwebproxystate", svc, "off"},
			{"-setsecurewebproxystate", svc, "off"},
		} {
			if err := networksetup(cmd...); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// get reports the web-proxy state of the first active network service.
func get() (State, error) {
	services, err := networkServices()
	if err != nil {
		return State{}, err
	}
	if len(services) == 0 {
		return State{}, nil
	}
	out, err := exec.Command("networksetup", "-getwebproxy", services[0]).Output()
	if err != nil {
		return State{}, fmt.Errorf("sysproxy: getwebproxy: %w", err)
	}
	return parseGetWebProxy(string(out)), nil
}

// networkServices lists the enabled network services. Disabled services are
// prefixed with '*' by networksetup and are skipped.
func networkServices() ([]string, error) {
	out, err := exec.Command("networksetup", "-listallnetworkservices").Output()
	if err != nil {
		return nil, fmt.Errorf("sysproxy: list network services: %w", err)
	}
	var services []string
	for i, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		// The first line is an informational header; skip it and disabled entries.
		if i == 0 || line == "" || strings.HasPrefix(line, "*") {
			continue
		}
		services = append(services, line)
	}
	return services, nil
}

// networksetup runs the tool with args, surfacing any error output.
func networksetup(args ...string) error {
	out, err := exec.Command("networksetup", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sysproxy: networksetup %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
