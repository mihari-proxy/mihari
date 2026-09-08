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
		want := "daemon --system-service"
		if goos == "darwin" {
			want += " --launchd-process-group"
			if !strings.Contains(string(def.Files[0].Bytes), "<key>AbandonProcessGroup</key><false/>") {
				t.Fatal("launchd definition does not retain process-group cleanup")
			}
		}
		if strings.Join(def.Args, " ") != want {
			t.Fatalf("%s missing explicit service gate: %v", goos, def.Args)
		}
		var parsed parsedExec
		if goos == "linux" {
			parsed, err = parseSystemdUnit(def.Files[0].Bytes)
		} else {
			parsed, err = parseLaunchdPlist(def.Files[0].Bytes)
		}
		if err != nil || strings.Join(parsed.Args, " ") != want {
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

func TestServiceArguments_LaunchdMarkerIsPlatformSpecific(t *testing.T) {
	valid := []string{"/opt/mihari/mihari", "daemon", "--system-service", "--launchd-process-group"}
	if err := rejectStrangeLaunchdExec(valid); err != nil {
		t.Fatal(err)
	}
	if err := rejectStrangeExec(valid); err == nil {
		t.Fatal("systemd accepted Darwin-only process mode")
	}
	for _, args := range [][]string{
		{"/opt/mihari/mihari", "daemon", "--launchd-process-group"},
		{"/opt/mihari/mihari", "daemon", "--system-service", "--launchd-process-group=false"},
		{"/opt/mihari/mihari", "daemon", "--launchd-process-group", "--system-service"},
		{"/opt/mihari/mihari", "daemon", "--system-service", "--launchd-process-group", "--launchd-process-group"},
	} {
		if err := rejectStrangeLaunchdExec(args); err == nil {
			t.Fatal("noncanonical shared service arguments accepted")
		}
	}
}

func TestLaunchdPlist_SharedGroupRequiresCleanup(t *testing.T) {
	def, err := BuildUnixDefinition(platform.ResolvedLayout{Mode: platform.SystemMode, InstallRoot: "/opt/mihari"}, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	for _, replacement := range []string{"", "<key>AbandonProcessGroup</key><true/>", "<key>AbandonProcessGroup</key><string>false</string>"} {
		raw := strings.Replace(string(def.Files[0].Bytes), "<key>AbandonProcessGroup</key><false/>", replacement, 1)
		if _, err := parseLaunchdPlist([]byte(raw)); err == nil {
			t.Fatal("shared service without explicit group cleanup accepted")
		}
	}
}
