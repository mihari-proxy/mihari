package service

import (
	"bytes"
	"github.com/mihari-proxy/mihari/internal/platform"
	"strings"
	"testing"
)

func TestBuildUnixDefinition_MatchesTrustedSystemdSnapshot(t *testing.T) {
	layout := platform.ResolvedLayout{Mode: platform.SystemMode, InstallRoot: "/usr/local/lib/mihari", ControlEndpoint: "/var/lib/mihari/control.sock", CredentialPath: "/var/lib/mihari/control.token"}
	def, err := BuildUnixDefinition(layout, "linux")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(def.Files[0].Bytes, trustedUnitFile(t).Bytes) {
		t.Fatal("trusted systemd snapshot differs from the installed definition")
	}
}

func TestBuildUnixDefinition_FixedLayoutAndFullFiles(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		layout, err := platform.ResolveLayout(platform.LayoutInput{CWD: "/", EUID: 0}, platform.LayoutDefaults{OS: goos, BaseDir: "/var/lib/mihari", InstallRoot: "/usr/local/lib/mihari", SocketLimit: 107})
		if err != nil {
			t.Fatal(err)
		}
		def, err := BuildUnixDefinition(layout, goos)
		if err != nil || def.Binary != "/usr/local/lib/mihari/mihari" || len(def.Files) != 1 || len(def.Env) != 3 {
			t.Fatalf("missing full fixed definition: %+v err=%v", def, err)
		}
		body := string(def.Files[0].Bytes)
		for _, forbidden := range []string{"HOME", "SUDO_USER", "XDG_", "MIHARI_DATA"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("ambient path leaked into service: %s", forbidden)
			}
		}
		if def.Files[0].Owner != 0 || def.Files[0].Mode != 0644 {
			t.Fatal("wrong service file permissions")
		}
	}
}

func TestBuildUnixDefinition_SystemServiceMarker(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		layout := platform.ResolvedLayout{Mode: platform.PrivateMode, InstallRoot: "/opt/mihari", Data: platform.Paths{Root: "/portable"}, ControlEndpoint: "/portable/control.sock", CredentialPath: "/portable/control.token"}
		def, err := BuildUnixDefinition(layout, goos)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(def.Args, " ") != "daemon --system-service" {
			t.Fatalf("%s missing explicit service gate: %v", goos, def.Args)
		}
		var parsed parsedExec
		if goos == "linux" {
			parsed, err = parseSystemdUnit(def.Files[0].Bytes)
		} else {
			parsed, err = parseLaunchdPlist(def.Files[0].Bytes)
		}
		if err != nil || strings.Join(parsed.Args, " ") != "daemon --system-service" {
			t.Fatalf("%s service marker roundtrip: %v %v", goos, parsed.Args, err)
		}
	}
}

func TestServiceArguments_ExactLegacyOrMarked(t *testing.T) {
	for _, args := range [][]string{{"daemon"}, {"daemon", "--system-service"}} {
		if err := rejectStrangeExec(append([]string{"/opt/mihari/mihari"}, args...)); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"daemon", "--other"}, {"daemon", "--system-service", "--system-service"}, {"self"}, {"daemon", "--system-service=true"}} {
		if err := rejectStrangeExec(append([]string{"/opt/mihari/mihari"}, args...)); err == nil {
			t.Fatalf("unexpected service argv accepted: %v", args)
		}
	}
}
