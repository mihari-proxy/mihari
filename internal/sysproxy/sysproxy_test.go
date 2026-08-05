package sysproxy

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeServer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		host string
		port int
		want string
	}{
		{
			name: "ipv4 loopback",
			host: "127.0.0.1",
			port: 9190,
			want: "127.0.0.1:9190",
		},
		{
			name: "ipv6 loopback with brackets",
			host: "::1",
			port: 7890,
			want: "[::1]:7890",
		},
		{
			name: "hostname",
			host: "localhost",
			port: 8080,
			want: "localhost:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := NormalizeServer(tt.host, tt.port)
			if got != tt.want {
				t.Fatalf("NormalizeServer(%q, %d) = %q, want %q", tt.host, tt.port, got, tt.want)
			}
		})
	}
}

func TestEnable_RejectsInvalidPort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		port int
	}{
		{name: "zero", port: 0},
		{name: "negative", port: -1},
		{name: "too large", port: 65536},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := Enable("127.0.0.1", tt.port)
			if err == nil {
				t.Fatal("Enable() error = nil, want invalid port error")
			}
			if !strings.Contains(err.Error(), "port") {
				t.Fatalf("Enable() error = %v, want mention of port", err)
			}
		})
	}
}

func TestClassifyOwnedForeign(t *testing.T) {
	t.Parallel()

	const target = "127.0.0.1:9190"

	tests := []struct {
		name    string
		state   State
		target  string
		owned   bool
		foreign bool
	}{
		{
			name:    "disabled is neither owned nor foreign",
			state:   State{Enabled: false, Server: ""},
			target:  target,
			owned:   false,
			foreign: false,
		},
		{
			name:    "disabled with leftover server is neither",
			state:   State{Enabled: false, Server: "127.0.0.1:7890"},
			target:  target,
			owned:   false,
			foreign: false,
		},
		{
			name:    "enabled matching target is owned",
			state:   State{Enabled: true, Server: "127.0.0.1:9190"},
			target:  target,
			owned:   true,
			foreign: false,
		},
		{
			name:    "enabled other server is foreign",
			state:   State{Enabled: true, Server: "127.0.0.1:7890"},
			target:  target,
			owned:   false,
			foreign: true,
		},
		{
			name:    "enabled empty server is neither owned nor foreign",
			state:   State{Enabled: true, Server: ""},
			target:  target,
			owned:   false,
			foreign: false,
		},
		{
			name:    "enabled ipv6 target match is owned",
			state:   State{Enabled: true, Server: "[::1]:7890"},
			target:  NormalizeServer("::1", 7890),
			owned:   true,
			foreign: false,
		},
		{
			name:    "enabled ipv6 vs ipv4 is foreign",
			state:   State{Enabled: true, Server: "[::1]:7890"},
			target:  target,
			owned:   false,
			foreign: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsOwned(tt.state, tt.target); got != tt.owned {
				t.Fatalf("IsOwned(%+v, %q) = %v, want %v", tt.state, tt.target, got, tt.owned)
			}
			if got := IsForeign(tt.state, tt.target); got != tt.foreign {
				t.Fatalf("IsForeign(%+v, %q) = %v, want %v", tt.state, tt.target, got, tt.foreign)
			}
		})
	}
}

func TestFakeBackend_RecordsCalls(t *testing.T) {
	t.Parallel()

	fake := &FakeBackend{
		State: State{Enabled: false},
	}

	got, err := fake.Get()
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Enabled {
		t.Fatalf("Get() Enabled = true, want false")
	}
	if fake.GetCalls != 1 {
		t.Fatalf("GetCalls = %d, want 1", fake.GetCalls)
	}

	if err := fake.Enable("127.0.0.1", 9190); err != nil {
		t.Fatalf("Enable() error = %v", err)
	}
	if fake.EnableCalls != 1 {
		t.Fatalf("EnableCalls = %d, want 1", fake.EnableCalls)
	}
	if fake.LastEnableHost != "127.0.0.1" || fake.LastEnablePort != 9190 {
		t.Fatalf("LastEnable = %s:%d, want 127.0.0.1:9190", fake.LastEnableHost, fake.LastEnablePort)
	}

	got, err = fake.Get()
	if err != nil {
		t.Fatalf("Get() after Enable error = %v", err)
	}
	if !got.Enabled || got.Server != "127.0.0.1:9190" {
		t.Fatalf("Get() after Enable = %+v, want enabled 127.0.0.1:9190", got)
	}

	if err := fake.Disable(); err != nil {
		t.Fatalf("Disable() error = %v", err)
	}
	if fake.DisableCalls != 1 {
		t.Fatalf("DisableCalls = %d, want 1", fake.DisableCalls)
	}

	got, err = fake.Get()
	if err != nil {
		t.Fatalf("Get() after Disable error = %v", err)
	}
	if got.Enabled {
		t.Fatalf("Get() after Disable Enabled = true, want false")
	}
}

func TestFakeBackend_PropagatesErrors(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("injected")
	fake := &FakeBackend{
		GetErr:     wantErr,
		EnableErr:  wantErr,
		DisableErr: wantErr,
	}

	if _, err := fake.Get(); !errors.Is(err, wantErr) {
		t.Fatalf("Get() error = %v, want %v", err, wantErr)
	}
	if err := fake.Enable("127.0.0.1", 1); !errors.Is(err, wantErr) {
		t.Fatalf("Enable() error = %v, want %v", err, wantErr)
	}
	if err := fake.Disable(); !errors.Is(err, wantErr) {
		t.Fatalf("Disable() error = %v, want %v", err, wantErr)
	}
}
