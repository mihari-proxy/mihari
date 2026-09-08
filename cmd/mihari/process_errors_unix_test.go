//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestUnixProcess_LocalFailureExitContracts(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("ordinary portable fixture requires unprivileged UID")
	}
	for _, tc := range []struct {
		name string
		args []string
		code int
		api  protocol.ErrorCode
	}{
		{"invalid-layout", []string{"status"}, 2, protocol.CodeInvalidArgument},
		{"daemon-lock", []string{"daemon"}, 4, protocol.CodeInvalidState},
		{"channel-lock", []string{"self", "channel", "dev"}, 4, protocol.CodeInvalidState},
		{"unsafe-channel-root", []string{"self", "channel", "dev"}, 5, protocol.CodePermissionDenied},
		{"channel-IO", []string{"self", "channel", "dev"}, 9, protocol.CodeDataFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, err := os.MkdirTemp("", "m18-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.RemoveAll(root); err != nil {
					t.Error(err)
				}
			})
			t.Setenv("MIHARI_DATA", root)
			t.Setenv("MIHARI_CONTROL_ENDPOINT", filepath.Join(root, "control.sock"))
			t.Setenv("MIHARI_CONTROL_CREDENTIAL", filepath.Join(root, "control.token"))
			t.Setenv("MIHARI_INSTALL_ROOT", filepath.Join(root, "lib"))
			if tc.name == "daemon-lock" || tc.name == "channel-lock" || tc.name == "channel-IO" {
				parent, parentErr := platform.OpenTrustedParent(context.Background(), filepath.Dir(root), uint32(os.Geteuid()))
				if parentErr != nil {
					t.Skipf("native lease/IO fixture needs a protected TMPDIR: %v", parentErr)
				}
				if err := parent.Close(); err != nil {
					t.Fatal(err)
				}
			}
			switch tc.name {
			case "invalid-layout":
				t.Setenv("MIHARI_CONTROL_ENDPOINT", "/"+strings.Repeat("private-value", 20))
			case "channel-IO":
				t.Setenv("MIHARI_DATA", filepath.Join(root, strings.Repeat("x", 256)))
			case "unsafe-channel-root":
				if err := os.Chmod(root, 0777); err != nil {
					t.Fatal(err)
				}
			default:
				layout, _, err := platform.CaptureLayout(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				var closeLease func() error
				if tc.name == "daemon-lock" {
					lease, err := platform.AcquireDaemonLease(context.Background(), layout)
					if err != nil {
						t.Fatal(err)
					}
					closeLease = lease.Close
				} else {
					lease, err := platform.AcquireChannelLease(context.Background(), layout)
					if err != nil {
						t.Fatal(err)
					}
					closeLease = lease.Close
				}
				t.Cleanup(func() {
					if err := closeLease(); err != nil {
						t.Error(err)
					}
				})
			}
			var out bytes.Buffer
			code := executeProcess(context.Background(), append(tc.args, "--json"), &out, &out)
			var envelope protocol.ErrorEnvelope
			if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
				t.Fatalf("non-JSON failure: %v output=%s", err, out.String())
			}
			if code != tc.code || envelope.Error.Code != tc.api {
				t.Fatalf("exit=%d API=%s want %d/%s", code, envelope.Error.Code, tc.code, tc.api)
			}
			if strings.Contains(out.String(), root) || strings.Contains(out.String(), "private-value") {
				t.Fatal("failure exposed native path input")
			}
		})
	}
}
