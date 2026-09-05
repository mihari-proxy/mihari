package platform

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveLayout_SystemIgnoresHome(t *testing.T) {
	got, err := ResolveLayout(
		LayoutInput{CWD: "/", Home: "/home/spoofed", EUID: 1000},
		LayoutDefaults{OS: "linux", BaseDir: "/var/lib/mihari", InstallRoot: "/usr/local/lib/mihari", TrustedHome: "/home/a", SocketLimit: 107},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.Data.Root != "/var/lib/mihari/data" || got.CredentialPath != "/var/lib/mihari/control.token" {
		t.Fatalf("wrong machine layout: %+v", got)
	}
	if got.ClientLogs.Root != "/home/a/.local/state/mihari" {
		t.Fatalf("ClientLogs.Root=%q want trusted-home state root", got.ClientLogs.Root)
	}
}

func TestResolveLayout_SystemDefaults(t *testing.T) {
	tests := []struct {
		name       string
		input      LayoutInput
		defaults   LayoutDefaults
		wantData   string
		wantClient string
		wantBase   string
	}{
		{name: "linux non-root uses absolute XDG state", input: LayoutInput{CWD: "/work", Home: "/ignored", XDGState: "/state/user", EUID: 1000}, defaults: linuxTestDefaults("/users/account"), wantData: "/var/lib/mihari/data", wantClient: "/state/user/mihari", wantBase: "/var/lib/mihari"},
		{name: "linux root ignores HOME and XDG state", input: LayoutInput{CWD: "/work", Home: "/home/sudo-user", XDGState: "/run/user/1000", EUID: 0}, defaults: linuxTestDefaults("/root"), wantData: "/var/lib/mihari/data", wantClient: "/root/.local/state/mihari", wantBase: "/var/lib/mihari"},
		{name: "linux ignores relative XDG state", input: LayoutInput{CWD: "/work", XDGState: "state", EUID: 1000}, defaults: linuxTestDefaults("/users/account"), wantData: "/var/lib/mihari/data", wantClient: "/users/account/.local/state/mihari", wantBase: "/var/lib/mihari"},
		{name: "darwin ignores XDG state", input: LayoutInput{CWD: "/work", Home: "/ignored", XDGState: "/state/user", EUID: 501}, defaults: darwinTestDefaults("/Users/account"), wantData: "/Library/Application Support/mihari/data", wantClient: "/Users/account/Library/Logs/mihari", wantBase: "/Library/Application Support/mihari"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveLayout(tt.input, tt.defaults)
			if err != nil {
				t.Fatal(err)
			}
			if got.Mode != SystemMode || got.BaseDir != tt.wantBase || got.Data.Root != tt.wantData || got.ClientLogs.Root != tt.wantClient {
				t.Fatalf("layout=%+v want base=%q data=%q client=%q", got, tt.wantBase, tt.wantData, tt.wantClient)
			}
			if got.ControlEndpoint != tt.wantBase+"/control.sock" || got.CredentialPath != tt.wantBase+"/control.token" || got.ChannelPath != tt.wantBase+"/mihari-channel" {
				t.Fatalf("wrong control values: %+v", got)
			}
			if got.Data.DaemonLog != tt.wantData+"/logs/mihari-daemon.log" || got.Data.MihomoLog != tt.wantData+"/logs/mihomo.log" {
				t.Fatalf("wrong machine logs: %+v", got.Data)
			}
			if got.ClientLogs.TUILog != tt.wantClient+"/logs/mihari-tui.log" || got.ClientLogs.LogExportDir != tt.wantClient+"/logs-export" {
				t.Fatalf("wrong client logs: %+v", got.ClientLogs)
			}
		})
	}
}

func TestResolveLayout_UnusableUserLogsDoNotLoseMachineLayout(t *testing.T) {
	tests := []struct {
		name       string
		input      LayoutInput
		defaults   LayoutDefaults
		wantClient string
	}{
		{
			name:       "invalid XDG falls back to trusted home",
			input:      LayoutInput{CWD: "/", XDGState: "/state\x00user", EUID: 1000},
			defaults:   linuxTestDefaults("/users/account"),
			wantClient: "/users/account/.local/state/mihari",
		},
		{
			name:     "missing Linux candidates use memory diagnostics",
			input:    LayoutInput{CWD: "/", XDGState: "relative-state", EUID: 1000},
			defaults: linuxTestDefaults(""),
		},
		{
			name:     "relative trusted home uses memory diagnostics",
			input:    LayoutInput{CWD: "/", EUID: 1000},
			defaults: linuxTestDefaults("relative-home"),
		},
		{
			name:     "missing Darwin home uses memory diagnostics",
			input:    LayoutInput{CWD: "/", EUID: 501},
			defaults: darwinTestDefaults(""),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveLayout(tt.input, tt.defaults)
			if err != nil {
				t.Fatalf("optional client logs blocked machine layout: %v", err)
			}
			if got.Mode != SystemMode || got.Data.Root == "" || got.ControlEndpoint == "" || got.CredentialPath == "" {
				t.Fatalf("machine layout was lost: %+v", got)
			}
			if tt.wantClient != "" {
				if got.ClientLogs.Root != tt.wantClient {
					t.Fatalf("ClientLogs.Root=%q want=%q", got.ClientLogs.Root, tt.wantClient)
				}
				return
			}
			if got.ClientLogs != (Paths{}) {
				t.Fatalf("unavailable user logs=%+v want zero Paths", got.ClientLogs)
			}
		})
	}
}

func TestResolveLayout_PrivateOverridesAreAnchoredOnce(t *testing.T) {
	tests := []struct {
		name           string
		input          LayoutInput
		wantData       string
		wantEndpoint   string
		wantCredential string
		wantInstall    string
	}{
		{
			name:           "relative overrides",
			input:          LayoutInput{CWD: "/work/process", Data: "portable/../instance", Endpoint: "../run/control.sock", Credential: "credentials/control.token", InstallRoot: "../lib/mihari", Home: "/ignored", XDGState: "/ignored", EUID: 1000},
			wantData:       "/work/process/instance",
			wantEndpoint:   "/work/run/control.sock",
			wantCredential: "/work/process/credentials/control.token",
			wantInstall:    "/work/lib/mihari",
		},
		{
			name:           "absolute overrides",
			input:          LayoutInput{CWD: "/ignored", Data: "/srv/mihari-private", Endpoint: "/run/mihari-private/control.sock", Credential: "/etc/mihari-private/control.token", InstallRoot: "/opt/mihari", EUID: 1000},
			wantData:       "/srv/mihari-private",
			wantEndpoint:   "/run/mihari-private/control.sock",
			wantCredential: "/etc/mihari-private/control.token",
			wantInstall:    "/opt/mihari",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveLayout(tt.input, linuxTestDefaults("/users/account"))
			if err != nil {
				t.Fatal(err)
			}
			if got.Mode != PrivateMode || got.BaseDir != tt.wantData || got.Data.Root != got.BaseDir || got.ClientLogs.Root != got.BaseDir {
				t.Fatalf("wrong private roots: %+v", got)
			}
			if got.ControlEndpoint != tt.wantEndpoint || got.CredentialPath != tt.wantCredential || got.InstallRoot != tt.wantInstall {
				t.Fatalf("wrong anchored overrides: %+v", got)
			}
			if got.ChannelPath != tt.wantData+"/mihari-channel" {
				t.Fatalf("ChannelPath=%q", got.ChannelPath)
			}
		})
	}
}

func TestResolveLayout_EmptyOverridesUseDefaults(t *testing.T) {
	got, err := ResolveLayout(LayoutInput{CWD: "/work", EUID: 1000}, linuxTestDefaults("/users/account"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != SystemMode || got.ControlEndpoint != "/var/lib/mihari/control.sock" || got.CredentialPath != "/var/lib/mihari/control.token" || got.InstallRoot != "/usr/local/lib/mihari" {
		t.Fatalf("layout=%+v", got)
	}
}

func TestResolveLayout_ControlOverridesAreIndependent(t *testing.T) {
	tests := []struct {
		name           string
		input          LayoutInput
		wantEndpoint   string
		wantCredential string
	}{
		{name: "endpoint only", input: LayoutInput{CWD: "/", Data: "/srv/private", Endpoint: "/run/private.sock", EUID: 1000}, wantEndpoint: "/run/private.sock", wantCredential: "/srv/private/control.token"},
		{name: "credential only", input: LayoutInput{CWD: "/", Data: "/srv/private", Credential: "/etc/private.token", EUID: 1000}, wantEndpoint: "/srv/private/control.sock", wantCredential: "/etc/private.token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveLayout(tt.input, linuxTestDefaults("/users/account"))
			if err != nil {
				t.Fatal(err)
			}
			if got.ControlEndpoint != tt.wantEndpoint || got.CredentialPath != tt.wantCredential {
				t.Fatalf("control values=%+v", got)
			}
		})
	}
}

func TestResolveLayout_RejectsPrivateSystemOverlap(t *testing.T) {
	for _, data := range []string{"/var/lib/mihari", "/var/lib/mihari/data", "/var/lib", "/var/lib/mihari/data/nested"} {
		t.Run(data, func(t *testing.T) {
			_, err := ResolveLayout(LayoutInput{CWD: "/", Data: data, EUID: 1000}, linuxTestDefaults("/users/account"))
			if err == nil {
				t.Fatalf("ResolveLayout accepted overlapping private root %q", data)
			}
		})
	}
}

func TestResolveLayout_RejectsNUL(t *testing.T) {
	tests := []LayoutInput{
		{CWD: "/", Data: "/private\x00root", EUID: 1000},
		{CWD: "/", Endpoint: "/run/control\x00.sock", EUID: 1000},
		{CWD: "/", Credential: "/run/control\x00.token", EUID: 1000},
		{CWD: "/", InstallRoot: "/usr/local\x00/lib", EUID: 1000},
	}
	for i, input := range tests {
		_, err := ResolveLayout(input, linuxTestDefaults("/users/account"))
		if err == nil {
			t.Errorf("case %d accepted NUL", i)
		}
	}
}

func TestResolveLayout_ValidatesFinalSocketByteLength(t *testing.T) {
	tests := []struct {
		name     string
		cwd      string
		endpoint string
		defaults LayoutDefaults
		wantErr  bool
	}{
		{name: "Linux 107 bytes accepted", cwd: "/", endpoint: "/" + strings.Repeat("a", 106), defaults: linuxTestDefaults("/users/account")},
		{name: "Linux 108 bytes rejected", cwd: "/", endpoint: "/" + strings.Repeat("a", 107), defaults: linuxTestDefaults("/users/account"), wantErr: true},
		{name: "multibyte byte limit", cwd: "/", endpoint: "/" + strings.Repeat("界", 36), defaults: linuxTestDefaults("/users/account"), wantErr: true},
		{name: "relative endpoint validates anchored result", cwd: "/" + strings.Repeat("w", 100), endpoint: "control.sock", defaults: linuxTestDefaults("/users/account"), wantErr: true},
		{name: "Darwin 104 bytes rejected", cwd: "/", endpoint: "/" + strings.Repeat("a", 103), defaults: darwinTestDefaults("/Users/account"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ResolveLayout(LayoutInput{CWD: tt.cwd, Endpoint: tt.endpoint, EUID: 1000}, tt.defaults)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v endpoint bytes=%d", err, tt.wantErr, len(tt.endpoint))
			}
		})
	}
}

func TestPlatformLayoutDefaults_AreDeterministic(t *testing.T) {
	got := platformLayoutDefaults("HOME")
	switch runtime.GOOS {
	case "linux":
		if got.OS != "linux" || got.BaseDir != "/var/lib/mihari" || got.InstallRoot != "/usr/local/lib/mihari" || got.TrustedHome != "HOME" || got.SocketLimit != 107 {
			t.Fatalf("defaults=%+v", got)
		}
	case "darwin":
		if got.OS != "darwin" || got.BaseDir != "/Library/Application Support/mihari" || got.InstallRoot != "/usr/local/lib/mihari" || got.TrustedHome != "HOME" || got.SocketLimit != 103 {
			t.Fatalf("defaults=%+v", got)
		}
	case "windows":
		if got.OS != "windows" || got.BaseDir != `HOME\.mihari` || got.InstallRoot != `C:\Program Files\Mihari` || got.TrustedHome != "HOME" || got.SocketLimit != 0 {
			t.Fatalf("defaults=%+v", got)
		}
	default:
		t.Skip("T01 supports Windows, Linux, and macOS")
	}
}

func TestResolveLayout_WindowsPreservesLegacyValues(t *testing.T) {
	defaults := LayoutDefaults{OS: "windows", BaseDir: `C:\Users\account\.mihari`, InstallRoot: `C:\Program Files\Mihari`, TrustedHome: `C:\Users\account`}
	got, err := ResolveLayout(LayoutInput{CWD: `D:\work`, EUID: 1000}, defaults)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != WindowsMode || got.BaseDir != defaults.BaseDir || got.Data.Root != defaults.BaseDir || got.ClientLogs != got.Data {
		t.Fatalf("wrong Windows roots: %+v", got)
	}
	if got.ControlEndpoint != `\\.\pipe\mihari-control` || got.CredentialPath != `C:\Users\account\.mihari\control.token` || got.ChannelPath != `C:\Users\account\.mihari\mihari-channel` {
		t.Fatalf("wrong Windows control values: %+v", got)
	}
	if got.Data.CoreBinary != `C:\Users\account\.mihari\bin\mihomo.exe` || got.InstallRoot != defaults.InstallRoot {
		t.Fatalf("wrong Windows legacy paths: %+v", got)
	}
}

func TestResolveLayout_WindowsRelativeOverridesUseWindowsCWD(t *testing.T) {
	defaults := LayoutDefaults{OS: "windows", BaseDir: `C:\Users\account\.mihari`, InstallRoot: `C:\Program Files\Mihari`}
	got, err := ResolveLayout(LayoutInput{CWD: `D:\work\run`, Data: `..\data`, Endpoint: `pipes\control`, Credential: `credentials\control.token`, InstallRoot: `..\install`}, defaults)
	if err != nil {
		t.Fatal(err)
	}
	if got.Data.Root != `D:\work\data` || got.ControlEndpoint != `D:\work\run\pipes\control` || got.CredentialPath != `D:\work\run\credentials\control.token` || got.InstallRoot != `D:\work\install` {
		t.Fatalf("wrong Windows relative resolution: %+v", got)
	}
}

func TestResolveLayout_WindowsDriveRootRemainsAbsolute(t *testing.T) {
	defaults := LayoutDefaults{OS: "windows", BaseDir: `C:\base`, InstallRoot: `C:\Program Files\Mihari`}
	got, err := ResolveLayout(LayoutInput{CWD: `C:\work`, Data: `D:\`}, defaults)
	if err != nil {
		t.Fatal(err)
	}
	if got.Data.Root != `D:\` || got.BaseDir != `D:\` {
		t.Fatalf("drive root was not preserved: %+v", got)
	}
}

func TestResolveLayout_WindowsRootMatchesNativeClean(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "bare UNC share", data: `\\server\share`, want: `\\server\share`},
		{name: "rooted UNC share", data: `\\server\share\`, want: `\\server\share\`},
		{name: "drive root", data: `C:\`, want: `C:\`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if runtime.GOOS == "windows" {
				if native := filepath.Clean(tt.data); native != tt.want {
					t.Fatalf("native filepath.Clean(%q)=%q want=%q", tt.data, native, tt.want)
				}
			}
			defaults := LayoutDefaults{OS: "windows", BaseDir: `D:\base`, InstallRoot: `C:\Program Files\Mihari`}
			got, err := ResolveLayout(LayoutInput{CWD: `D:\work`, Data: tt.data}, defaults)
			if err != nil {
				t.Fatal(err)
			}
			if got.Data.Root != tt.want || got.BaseDir != tt.want {
				t.Fatalf("root=%q base=%q want native clean=%q", got.Data.Root, got.BaseDir, tt.want)
			}
		})
	}
}

func TestResolveLayout_WindowsTraversalClampsAtVolumeRoot(t *testing.T) {
	tests := []struct {
		name     string
		data     string
		wantRoot string
	}{
		{name: "drive root", data: `C:\..\portable`, wantRoot: `C:\portable`},
		{name: "UNC share root", data: `\\server\share\..\portable`, wantRoot: `\\server\share\portable`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defaults := LayoutDefaults{OS: "windows", BaseDir: `D:\base`, InstallRoot: `C:\Program Files\Mihari`}
			got, err := ResolveLayout(LayoutInput{CWD: `D:\work`, Data: tt.data}, defaults)
			if err != nil {
				t.Fatal(err)
			}
			if got.Data.Root != tt.wantRoot || got.BaseDir != tt.wantRoot {
				t.Fatalf("root=%q base=%q want=%q", got.Data.Root, got.BaseDir, tt.wantRoot)
			}
		})
	}
}

func TestResolvedLayout_LocatorPinsExpectedOwner(t *testing.T) {
	tests := []struct {
		name      string
		layout    ResolvedLayout
		euid      uint32
		wantOwner uint32
		wantErr   bool
	}{
		{name: "system owner is root", layout: ResolvedLayout{Mode: SystemMode, ControlEndpoint: "/run/control.sock", CredentialPath: "/run/control.token"}, euid: 1000, wantOwner: 0},
		{name: "private owner is caller", layout: ResolvedLayout{Mode: PrivateMode, ControlEndpoint: "/run/control.sock", CredentialPath: "/run/control.token"}, euid: 1001, wantOwner: 1001},
		{name: "Windows does not construct Unix locator", layout: ResolvedLayout{Mode: WindowsMode, ControlEndpoint: `\\.\pipe\mihari-control`, CredentialPath: `C:\data\control.token`}, euid: 1000, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.layout.Locator(tt.euid)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got.Mode != tt.layout.Mode || got.Endpoint != tt.layout.ControlEndpoint || got.Credential != tt.layout.CredentialPath || got.ExpectedOwner != tt.wantOwner {
				t.Fatalf("locator=%+v", got)
			}
		})
	}
}

func linuxTestDefaults(home string) LayoutDefaults {
	return LayoutDefaults{OS: "linux", BaseDir: "/var/lib/mihari", InstallRoot: "/usr/local/lib/mihari", TrustedHome: home, SocketLimit: 107}
}

func darwinTestDefaults(home string) LayoutDefaults {
	return LayoutDefaults{OS: "darwin", BaseDir: "/Library/Application Support/mihari", InstallRoot: "/usr/local/lib/mihari", TrustedHome: home, SocketLimit: 103}
}

func TestResolvedLayout_LocatorRetainsSelectedBase(t *testing.T) {
	layout := ResolvedLayout{Mode: PrivateMode, BaseDir: "/selected/private", ControlEndpoint: "/external/e", CredentialPath: "/other/c"}
	got, err := layout.Locator(1000)
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseDir != "/selected/private" {
		t.Fatalf("lost selected anchor: %q", got.BaseDir)
	}
}
