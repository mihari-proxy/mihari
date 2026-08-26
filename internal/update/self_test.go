package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestSelectSelfAsset(t *testing.T) {
	linuxExact := Asset{Name: "mihari-linux-amd64", URL: "linux-exact", Size: 10}
	windowsExact := Asset{Name: "mihari-windows-amd64.exe", URL: "windows-exact", Size: 10}
	linuxArchive := Asset{Name: "mihari-linux-amd64.tar.gz", URL: "linux-archive", Size: 10}
	windowsArchive := Asset{Name: "mihari-windows-amd64.exe.zip", URL: "windows-archive", Size: 10}
	linuxDebug := Asset{Name: "mihari-linux-amd64-debug", URL: "linux-debug", Size: 10}
	windowsDebug := Asset{Name: "mihari-windows-amd64-debug.exe", URL: "windows-debug", Size: 10}
	linuxCase := Asset{Name: "Mihari-Linux-Amd64", URL: "linux-case", Size: 10}
	windowsCase := Asset{Name: "Mihari-Windows-Amd64.EXE", URL: "windows-case", Size: 10}
	linuxDup := Asset{Name: "mihari-linux-amd64", URL: "linux-dup", Size: 11}
	windowsDup := Asset{Name: "mihari-windows-amd64.exe", URL: "windows-dup", Size: 11}

	tests := []struct {
		name    string
		assets  []Asset
		goos    string
		goarch  string
		wantURL string
		wantErr bool
	}{
		{
			name:    "linux exact",
			assets:  []Asset{linuxExact, linuxArchive},
			goos:    "linux",
			goarch:  "amd64",
			wantURL: "linux-exact",
		},
		{
			name:    "windows exact",
			assets:  []Asset{windowsExact, windowsArchive},
			goos:    "windows",
			goarch:  "amd64",
			wantURL: "windows-exact",
		},
		{
			name:    "linux missing",
			assets:  []Asset{windowsExact, linuxArchive},
			goos:    "linux",
			goarch:  "amd64",
			wantErr: true,
		},
		{
			name:    "windows missing",
			assets:  []Asset{linuxExact},
			goos:    "windows",
			goarch:  "amd64",
			wantErr: true,
		},
		{
			name:    "darwin missing",
			assets:  []Asset{linuxExact, windowsExact},
			goos:    "darwin",
			goarch:  "arm64",
			wantErr: true,
		},
		{
			name:    "linux duplicate exact",
			assets:  []Asset{linuxExact, linuxDup},
			goos:    "linux",
			goarch:  "amd64",
			wantErr: true,
		},
		{
			name:    "windows duplicate exact",
			assets:  []Asset{windowsExact, windowsDup},
			goos:    "windows",
			goarch:  "amd64",
			wantErr: true,
		},
		{
			name:    "linux prefix debug only",
			assets:  []Asset{linuxDebug},
			goos:    "linux",
			goarch:  "amd64",
			wantErr: true,
		},
		{
			name:    "windows prefix debug only",
			assets:  []Asset{windowsDebug},
			goos:    "windows",
			goarch:  "amd64",
			wantErr: true,
		},
		{
			name:    "linux prefix debug with exact",
			assets:  []Asset{linuxDebug, linuxExact},
			goos:    "linux",
			goarch:  "amd64",
			wantURL: "linux-exact",
		},
		{
			name:    "linux case variant only",
			assets:  []Asset{linuxCase},
			goos:    "linux",
			goarch:  "amd64",
			wantErr: true,
		},
		{
			name:    "windows case variant only",
			assets:  []Asset{windowsCase},
			goos:    "windows",
			goarch:  "amd64",
			wantErr: true,
		},
		{
			name:    "linux case variant with exact",
			assets:  []Asset{linuxCase, linuxExact},
			goos:    "linux",
			goarch:  "amd64",
			wantURL: "linux-exact",
		},
		{
			name:    "linux archive neighbor only",
			assets:  []Asset{linuxArchive},
			goos:    "linux",
			goarch:  "amd64",
			wantErr: true,
		},
		{
			name:    "linux archive neighbor with exact",
			assets:  []Asset{linuxArchive, linuxExact},
			goos:    "linux",
			goarch:  "amd64",
			wantURL: "linux-exact",
		},
		{
			name:    "windows archive neighbor only",
			assets:  []Asset{windowsArchive},
			goos:    "windows",
			goarch:  "amd64",
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			asset, err := SelectSelfAsset(Release{TagName: "v1.2.3", Assets: test.assets}, test.goos, test.goarch)
			if test.wantErr {
				var apiError protocol.APIError
				if err == nil || !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure {
					t.Fatalf("asset=%#v err=%v", asset, err)
				}
				return
			}
			if err != nil || asset.URL != test.wantURL {
				t.Fatalf("asset=%#v err=%v", asset, err)
			}
		})
	}
}

func TestSelfUpdateDownloadsAndReplaces(t *testing.T) {
	payload := []byte("new-binary-content")
	digest := fixtureSHA256Hex(payload)
	sibling := fixtureSHA256Hex([]byte("neighbor-payload"))
	tests := []struct {
		name     string
		manifest string
	}{
		{
			name:     "empty lines",
			manifest: "\n" + digest + "  mihari-linux-amd64\n\n" + sibling + "  mihari-windows-amd64.exe\n",
		},
		{
			name:     "gnu star marker",
			manifest: digest + " *mihari-linux-amd64\n" + sibling + " *mihari-windows-amd64.exe\n",
		},
		{
			name:     "uppercase digest",
			manifest: strings.ToUpper(digest) + "  mihari-linux-amd64\n" + sibling + "  mihari-windows-amd64.exe\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := startSelfUpdateEnv(t, selfUpdateServerConfig{
				checksumBody: test.manifest,
				binaryBody:   payload,
			})
			result, err := env.updater.Update(context.Background(), env.binaryPath, "v1.0.0", ChannelMain)
			if err != nil || !result.Updated || result.Version != "v9.9.9" {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			got, err := os.ReadFile(env.binaryPath)
			if err != nil || string(got) != string(payload) {
				t.Fatalf("binary=%q err=%v", got, err)
			}
		})
	}
}

func TestSelfUpdateRejectsInvalidChecksumManifest(t *testing.T) {
	payload := []byte("new-binary-content")
	digest := fixtureSHA256Hex(payload)
	sibling := fixtureSHA256Hex([]byte("neighbor-payload"))
	validLine := digest + "  mihari-linux-amd64"
	siblingLine := sibling + "  mihari-windows-amd64.exe"
	oversize := strings.Repeat("a", (1<<20)+1)

	tests := []struct {
		name   string
		cfg    selfUpdateServerConfig
		assets func(string) []Asset
	}{
		{
			name: "checksum missing",
			cfg:  selfUpdateServerConfig{binaryBody: payload},
			assets: func(serverURL string) []Asset {
				return []Asset{{Name: "mihari-linux-amd64", URL: serverURL + "/asset", Size: int64(len(payload))}}
			},
		},
		{
			name: "checksum duplicate",
			cfg:  selfUpdateServerConfig{checksumBody: validLine + "\n", binaryBody: payload},
			assets: func(serverURL string) []Asset {
				return []Asset{
					{Name: "mihari-linux-amd64", URL: serverURL + "/asset", Size: int64(len(payload))},
					{Name: "SHA256SUMS.txt", URL: serverURL + "/checksum", Size: 64},
					{Name: "SHA256SUMS.txt", URL: serverURL + "/checksum2", Size: 64},
				}
			},
		},
		{
			name: "checksum negative metadata",
			cfg:  selfUpdateServerConfig{checksumBody: validLine + "\n", binaryBody: payload},
			assets: func(serverURL string) []Asset {
				return linuxReleaseAssets(serverURL, payload, -1)
			},
		},
		{
			name: "checksum metadata over 1MiB",
			cfg:  selfUpdateServerConfig{checksumBody: validLine + "\n", binaryBody: payload},
			assets: func(serverURL string) []Asset {
				return linuxReleaseAssets(serverURL, payload, (1<<20)+1)
			},
		},
		{
			name: "checksum non-200",
			cfg: selfUpdateServerConfig{
				checksumStatus: http.StatusBadGateway,
				checksumBody:   validLine + "\n",
				binaryBody:     payload,
			},
		},
		{
			name: "checksum read error",
			cfg: selfUpdateServerConfig{
				breakChecksum: true,
				binaryBody:    payload,
			},
		},
		{
			name: "checksum actual oversize",
			cfg: selfUpdateServerConfig{
				checksumBody: oversize,
				binaryBody:   payload,
			},
			assets: func(serverURL string) []Asset {
				return linuxReleaseAssets(serverURL, payload, 1024)
			},
		},
		{
			name: "target missing",
			cfg: selfUpdateServerConfig{
				checksumBody: siblingLine + "\n",
				binaryBody:   payload,
			},
		},
		{
			name: "target duplicate",
			cfg: selfUpdateServerConfig{
				checksumBody: validLine + "\n" + digest + " *mihari-linux-amd64\n",
				binaryBody:   payload,
			},
		},
		{
			name: "invalid target digest",
			cfg: selfUpdateServerConfig{
				checksumBody: strings.Repeat("z", 64) + "  mihari-linux-amd64\n",
				binaryBody:   payload,
			},
		},
		{
			name: "malformed unrelated line",
			cfg: selfUpdateServerConfig{
				checksumBody: validLine + "\nnot-a-checksum-line\n",
				binaryBody:   payload,
			},
		},
		{
			name: "extra field",
			cfg: selfUpdateServerConfig{
				checksumBody: digest + "  mihari-linux-amd64 extra\n",
				binaryBody:   payload,
			},
		},
		{
			name: "short digest",
			cfg: selfUpdateServerConfig{
				checksumBody: "abcd  mihari-linux-amd64\n",
				binaryBody:   payload,
			},
		},
		{
			name: "long digest",
			cfg: selfUpdateServerConfig{
				checksumBody: strings.Repeat("ab", 33) + "  mihari-linux-amd64\n",
				binaryBody:   payload,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := test.cfg
			if test.assets != nil {
				cfg.assets = test.assets
			}
			if cfg.assets == nil {
				cfg.assets = func(serverURL string) []Asset {
					return linuxReleaseAssets(serverURL, payload, int64(len(cfg.checksumBody)))
				}
			}
			env := startSelfUpdateEnv(t, cfg)
			wantCode := protocol.CodeDataFailure
			switch test.name {
			case "checksum non-200", "checksum read error":
				wantCode = protocol.CodeNetworkFailure
			}
			_, err := env.updater.Update(context.Background(), env.binaryPath, "v1.0.0", ChannelMain)
			assertFailClosedUpdate(t, env, err, wantCode, true)
		})
	}
}

func TestSelfUpdateChecksumRequestUsesContextAndHeaders(t *testing.T) {
	payload := []byte("new-binary-content")
	digest := fixtureSHA256Hex(payload)
	manifest := digest + "  mihari-linux-amd64\n"

	t.Run("user agent and injected client", func(t *testing.T) {
		var userAgent, host string
		env := startSelfUpdateEnv(t, selfUpdateServerConfig{
			checksumBody: manifest,
			binaryBody:   payload,
			onChecksum: func(r *http.Request) {
				userAgent = r.Header.Get("User-Agent")
				host = r.Host
			},
		})
		result, err := env.updater.Update(context.Background(), env.binaryPath, "v1.0.0", ChannelMain)
		if err != nil || !result.Updated {
			t.Fatalf("result=%#v err=%v", result, err)
		}
		if userAgent != "mihari" {
			t.Fatalf("User-Agent=%q", userAgent)
		}
		if host == "" || !strings.Contains(env.serverURL, host) {
			t.Fatalf("checksum host=%q server=%s", host, env.serverURL)
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		env := startSelfUpdateEnv(t, selfUpdateServerConfig{
			checksumBody: manifest,
			binaryBody:   payload,
			onChecksum: func(r *http.Request) {
				cancel()
				<-r.Context().Done()
			},
		})
		_, err := env.updater.Update(ctx, env.binaryPath, "v1.0.0", ChannelMain)
		assertFailClosedUpdate(t, env, err, protocol.CodeNetworkFailure, true)
	})
}

func TestSelfUpdateRejectsInvalidBinary(t *testing.T) {
	payload := []byte("new-binary-content")
	digest := fixtureSHA256Hex(payload)
	manifest := digest + "  mihari-linux-amd64\n"
	wrongDigest := fixtureSHA256Hex([]byte("other-payload"))

	tests := []struct {
		name            string
		cfg             selfUpdateServerConfig
		assets          func(string) []Asset
		openCandidate   func(string) (io.WriteCloser, error)
		wantCode        protocol.ErrorCode
		requireAPIError bool
		wantErrSubstr   string
	}{
		{
			name: "binary non-200",
			cfg: selfUpdateServerConfig{
				checksumBody: manifest,
				binaryBody:   payload,
				binaryStatus: http.StatusBadGateway,
			},
			wantCode:        protocol.CodeNetworkFailure,
			requireAPIError: true,
		},
		{
			name: "binary read error",
			cfg: selfUpdateServerConfig{
				checksumBody: manifest,
				binaryBody:   payload,
				breakBinary:  true,
			},
			wantCode:        protocol.CodeNetworkFailure,
			requireAPIError: true,
		},
		{
			name: "binary actual oversize",
			cfg: selfUpdateServerConfig{
				checksumBody: manifest,
				binaryBody:   payload,
				streamBinary: maxSelfBinarySize + 1,
			},
			wantCode:        protocol.CodeDataFailure,
			requireAPIError: true,
		},
		{
			name: "positive metadata mismatch",
			cfg: selfUpdateServerConfig{
				checksumBody: manifest,
				binaryBody:   payload,
			},
			assets: func(serverURL string) []Asset {
				return []Asset{
					{Name: "mihari-linux-amd64", URL: serverURL + "/asset", Size: int64(len(payload) + 5)},
					{Name: "SHA256SUMS.txt", URL: serverURL + "/checksum", Size: int64(len(manifest))},
				}
			},
			wantCode:        protocol.CodeDataFailure,
			requireAPIError: true,
		},
		{
			name: "digest mismatch",
			cfg: selfUpdateServerConfig{
				checksumBody: wrongDigest + "  mihari-linux-amd64\n",
				binaryBody:   payload,
			},
			wantCode:        protocol.CodeDataFailure,
			requireAPIError: true,
		},
		{
			name: "candidate open error",
			cfg: selfUpdateServerConfig{
				checksumBody: manifest,
				binaryBody:   payload,
			},
			openCandidate: func(string) (io.WriteCloser, error) {
				return nil, errors.New("disk full")
			},
			wantErrSubstr: "create mihari candidate",
		},
		{
			name: "writer write error",
			cfg: selfUpdateServerConfig{
				checksumBody: manifest,
				binaryBody:   payload,
			},
			openCandidate: func(string) (io.WriteCloser, error) {
				return failWriter{writeErr: errors.New("write failed")}, nil
			},
			wantCode:        protocol.CodeNetworkFailure,
			requireAPIError: true,
		},
		{
			name: "writer close error",
			cfg: selfUpdateServerConfig{
				checksumBody: manifest,
				binaryBody:   payload,
			},
			openCandidate: func(string) (io.WriteCloser, error) {
				return failWriter{closeErr: errors.New("close failed")}, nil
			},
			wantCode:        protocol.CodeNetworkFailure,
			requireAPIError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := test.cfg
			if test.assets != nil {
				cfg.assets = test.assets
			}
			env := startSelfUpdateEnv(t, cfg)
			if test.openCandidate != nil {
				env.updater.openCandidate = test.openCandidate
			}
			_, err := env.updater.Update(context.Background(), env.binaryPath, "v1.0.0", ChannelMain)
			if test.wantErrSubstr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErrSubstr)) {
				t.Fatalf("err=%v want substring %q", err, test.wantErrSubstr)
			}
			assertFailClosedUpdate(t, env, err, test.wantCode, test.requireAPIError)
		})
	}
}

type failWriter struct {
	writeErr error
	closeErr error
}

func (w failWriter) Write(p []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return len(p), nil
}

func (w failWriter) Close() error { return w.closeErr }

func TestSelfUpdateRejectsAmbiguousTargetAsset(t *testing.T) {
	payload := []byte("new-binary-content")
	manifest := fixtureSHA256Hex(payload) + "  mihari-linux-amd64\n"
	tests := []struct {
		name   string
		assets func(string) []Asset
	}{
		{
			name: "binary missing",
			assets: func(serverURL string) []Asset {
				return []Asset{{Name: "SHA256SUMS.txt", URL: serverURL + "/checksum", Size: int64(len(manifest))}}
			},
		},
		{
			name: "duplicate exact",
			assets: func(serverURL string) []Asset {
				return []Asset{
					{Name: "mihari-linux-amd64", URL: serverURL + "/asset", Size: int64(len(payload))},
					{Name: "mihari-linux-amd64", URL: serverURL + "/asset", Size: int64(len(payload))},
					{Name: "SHA256SUMS.txt", URL: serverURL + "/checksum", Size: int64(len(manifest))},
				}
			},
		},
		{
			name: "prefix only",
			assets: func(serverURL string) []Asset {
				return []Asset{
					{Name: "mihari-linux-amd64-debug", URL: serverURL + "/asset", Size: int64(len(payload))},
					{Name: "SHA256SUMS.txt", URL: serverURL + "/checksum", Size: int64(len(manifest))},
				}
			},
		},
		{
			name: "case variant only",
			assets: func(serverURL string) []Asset {
				return []Asset{
					{Name: "Mihari-Linux-Amd64", URL: serverURL + "/asset", Size: int64(len(payload))},
					{Name: "SHA256SUMS.txt", URL: serverURL + "/checksum", Size: int64(len(manifest))},
				}
			},
		},
		{
			name: "archive only",
			assets: func(serverURL string) []Asset {
				return []Asset{
					{Name: "mihari-linux-amd64.tar.gz", URL: serverURL + "/asset", Size: int64(len(payload))},
					{Name: "SHA256SUMS.txt", URL: serverURL + "/checksum", Size: int64(len(manifest))},
				}
			},
		},
		{
			name: "debug only",
			assets: func(serverURL string) []Asset {
				return []Asset{
					{Name: "mihari-linux-amd64-debug", URL: serverURL + "/asset", Size: int64(len(payload))},
					{Name: "SHA256SUMS.txt", URL: serverURL + "/checksum", Size: int64(len(manifest))},
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := startSelfUpdateEnv(t, selfUpdateServerConfig{
				checksumBody: manifest,
				binaryBody:   payload,
				assets:       test.assets,
			})
			_, err := env.updater.Update(context.Background(), env.binaryPath, "v1.0.0", ChannelMain)
			assertFailClosedUpdate(t, env, err, protocol.CodeDataFailure, true)
		})
	}
}

func TestSelfUpdateSkipsSameVersion(t *testing.T) {
	payload := []byte("same")
	env := startSelfUpdateEnv(t, selfUpdateServerConfig{
		tag:          "v1.0.0",
		checksumBody: fixtureSHA256Hex(payload) + "  mihari-linux-amd64\n",
		binaryBody:   payload,
	})
	result, err := env.updater.Update(context.Background(), env.binaryPath, "v1.0.0", ChannelMain)
	if err != nil || result.Updated {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if env.checksumReqs != 0 || env.binaryReqs != 0 {
		t.Fatalf("checksum=%d binary=%d, want 0", env.checksumReqs, env.binaryReqs)
	}
	if env.afterReplace {
		t.Fatal("AfterReplace called")
	}
	got, readErr := os.ReadFile(env.binaryPath)
	if readErr != nil || string(got) != oldBinaryContent {
		t.Fatalf("old binary changed: %q err=%v", got, readErr)
	}
}

func TestSelfUpdateSkipsAheadVersion(t *testing.T) {
	payload := []byte("same")
	env := startSelfUpdateEnv(t, selfUpdateServerConfig{
		tag:          "v0.8.2",
		checksumBody: fixtureSHA256Hex(payload) + "  mihari-linux-amd64\n",
		binaryBody:   payload,
	})
	result, err := env.updater.Update(context.Background(), env.binaryPath, "v0.9.0", ChannelMain)
	if err != nil || result.Updated || !result.Ahead || result.Version != "v0.8.2" || result.Channel != ChannelMain {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if env.checksumReqs != 0 || env.binaryReqs != 0 {
		t.Fatalf("checksum=%d binary=%d, want 0", env.checksumReqs, env.binaryReqs)
	}
}

func TestSelfUpdateInstallsOfficialWhenCurrentIsPrerelease(t *testing.T) {
	payload := []byte("official-binary")
	env := startSelfUpdateEnv(t, selfUpdateServerConfig{
		tag:          "v0.8.2",
		checksumBody: fixtureSHA256Hex(payload) + "  mihari-linux-amd64\n",
		binaryBody:   payload,
	})
	result, err := env.updater.Update(context.Background(), env.binaryPath, "v0.9.0-dev.8", ChannelMain)
	if err != nil || !result.Updated || result.Ahead || result.Version != "v0.8.2" || result.Channel != ChannelMain {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	got, readErr := os.ReadFile(env.binaryPath)
	if readErr != nil || string(got) != string(payload) {
		t.Fatalf("binary=%q err=%v", got, readErr)
	}
}

func TestSelfUpdateInstallsPrereleaseWhenCurrentIsOfficial(t *testing.T) {
	payload := []byte("prerelease-binary")
	env := startSelfUpdateEnv(t, selfUpdateServerConfig{
		tag:          "v0.9.0-dev.8",
		checksumBody: fixtureSHA256Hex(payload) + "  mihari-linux-amd64\n",
		binaryBody:   payload,
	})
	result, err := env.updater.Update(context.Background(), env.binaryPath, "v0.8.2", ChannelDev)
	if err != nil || !result.Updated || result.Ahead || result.Version != "v0.9.0-dev.8" || result.Channel != ChannelDev {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	got, readErr := os.ReadFile(env.binaryPath)
	if readErr != nil || string(got) != string(payload) {
		t.Fatalf("binary=%q err=%v", got, readErr)
	}
}

func TestSelfUpdaterCheckReportsAvailability(t *testing.T) {
	tests := []struct {
		name      string
		current   string
		latest    string
		available bool
		ahead     bool
	}{
		{name: "new release", current: "v1.0.0", latest: "v1.1.0", available: true},
		{name: "same release", current: "v1.0.0", latest: "v1.0.0"},
		{name: "ahead of latest", current: "v2.0.0", latest: "v1.0.0", ahead: true},
		{name: "prerelease current on main", current: "v0.9.0-dev.8", latest: "v0.8.2", available: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var path string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				_ = json.NewEncoder(w).Encode(Release{TagName: test.latest})
			}))
			defer server.Close()
			result, err := (SelfUpdater{HTTPClient: server.Client(), APIBase: server.URL}).Check(context.Background(), test.current, ChannelMain)
			if err != nil || result.Current != test.current || result.Latest != test.latest || result.Available != test.available || result.Ahead != test.ahead || result.Channel != ChannelMain {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			if path != "/repos/mihari-proxy/mihari/releases/latest" {
				t.Fatalf("path=%q", path)
			}
		})
	}
}

func TestSelfUpdaterCheckCrossChannelAvailability(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		latest   string
		channel  string
		wantPath string
	}{
		{
			name:     "dev to main",
			current:  "v0.9.0-dev.8",
			latest:   "v0.8.2",
			channel:  ChannelMain,
			wantPath: "/repos/mihari-proxy/mihari/releases/latest",
		},
		{
			name:     "main to dev",
			current:  "v0.8.2",
			latest:   "v0.9.0-dev.8",
			channel:  ChannelDev,
			wantPath: "/repos/mihari-proxy/mihari/releases",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var path string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				release := Release{TagName: test.latest, Assets: []Asset{{Name: "mihari-linux-amd64"}}}
				if test.channel == ChannelDev {
					_ = json.NewEncoder(w).Encode([]Release{release})
					return
				}
				_ = json.NewEncoder(w).Encode(release)
			}))
			defer server.Close()
			result, err := (SelfUpdater{HTTPClient: server.Client(), APIBase: server.URL}).Check(context.Background(), test.current, test.channel)
			if err != nil || result.Current != test.current || result.Latest != test.latest || !result.Available || result.Ahead || result.Channel != test.channel {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			if path != test.wantPath {
				t.Fatalf("path=%q want=%q", path, test.wantPath)
			}
		})
	}
}

func TestSelfUpdaterCheckMainIgnoresHigherDevReleaseOnSameServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/mihari-proxy/mihari/releases/latest" {
			_ = json.NewEncoder(w).Encode(Release{TagName: "v0.8.2"})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	result, err := (SelfUpdater{HTTPClient: server.Client(), APIBase: server.URL}).Check(context.Background(), "v0.8.2", ChannelMain)
	if err != nil || result.Available || result.Latest != "v0.8.2" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestSelfUpdaterCheckRejectsInvalidChannel(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits++
	}))
	defer server.Close()
	_, err := (SelfUpdater{HTTPClient: server.Client(), APIBase: server.URL}).Check(context.Background(), "v0.8.2", "stable")
	var apiError protocol.APIError
	if err == nil || !errors.As(err, &apiError) || apiError.Code != protocol.CodeInvalidArgument {
		t.Fatalf("err=%v", err)
	}
	if hits != 0 {
		t.Fatalf("hits=%d", hits)
	}
}

func TestSelfUpdaterCheckDoesNotDownloadAsset(t *testing.T) {
	payload := []byte("new-binary")
	env := startSelfUpdateEnv(t, selfUpdateServerConfig{
		tag:          "v2.0.0",
		checksumBody: fixtureSHA256Hex(payload) + "  mihari-linux-amd64\n",
		binaryBody:   payload,
	})
	result, err := env.updater.Check(context.Background(), "v1.0.0", ChannelMain)
	if err != nil || !result.Available {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if env.checksumReqs != 0 || env.binaryReqs != 0 {
		t.Fatalf("checksum=%d binary=%d, want 0", env.checksumReqs, env.binaryReqs)
	}
}

func TestSelfUpdaterCheckDevUsesReleaseListNotLatest(t *testing.T) {
	var paths []string
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		queries = append(queries, r.URL.RawQuery)
		if r.URL.Path == "/repos/mihari-proxy/mihari/releases" {
			_ = json.NewEncoder(w).Encode([]Release{{TagName: "v0.9.0-dev.3", Assets: []Asset{{Name: "mihari-linux-amd64"}}}})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	result, err := (SelfUpdater{HTTPClient: server.Client(), APIBase: server.URL}).Check(context.Background(), "v0.8.2", ChannelDev)
	if err != nil || result.Latest != "v0.9.0-dev.3" || !result.Available || result.Channel != ChannelDev {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if len(paths) != 1 || paths[0] != "/repos/mihari-proxy/mihari/releases" {
		t.Fatalf("paths=%v", paths)
	}
	if !strings.Contains(queries[0], "per_page=100") {
		t.Fatalf("query=%q", queries[0])
	}
	for _, path := range paths {
		if strings.Contains(path, "/releases/latest") {
			t.Fatalf("requested latest: %v", paths)
		}
	}
}

func TestSelfUpdaterCheckDevSelectsGreatestCanonicalNotArrayOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/mihari-proxy/mihari/releases" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]Release{
			{TagName: "v0.8.0-dev.99", Assets: []Asset{{Name: "old"}}},
			{TagName: "v0.9.0"},
			{TagName: "v0.9.0-dev"},
			{TagName: "v0.9.0-dev.9", Draft: true},
			{TagName: "v0.9.0-dev.3", Assets: []Asset{{Name: "mihari-linux-amd64"}}},
		})
	}))
	defer server.Close()
	result, err := (SelfUpdater{HTTPClient: server.Client(), APIBase: server.URL}).Check(context.Background(), "v0.8.2", ChannelDev)
	if err != nil || result.Latest != "v0.9.0-dev.3" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestSelfUpdaterCheckDevFollowsNextNotLast(t *testing.T) {
	var pages []string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/mihari-proxy/mihari/releases" {
			http.NotFound(w, r)
			return
		}
		pages = append(pages, r.URL.Query().Get("page"))
		switch r.URL.Query().Get("page") {
		case "", "1":
			w.Header().Set("Link", `<`+server.URL+`/repos/mihari-proxy/mihari/releases?page=2>; rel="next", <`+server.URL+`/repos/mihari-proxy/mihari/releases?page=9>; rel="last"`)
			_ = json.NewEncoder(w).Encode([]Release{{TagName: "v0.9.0-dev.1", Assets: []Asset{{Name: "a"}}}})
		case "2":
			_ = json.NewEncoder(w).Encode([]Release{{TagName: "v0.9.0-dev.4", Assets: []Asset{{Name: "b"}}}})
		case "9":
			_ = json.NewEncoder(w).Encode([]Release{{TagName: "v0.9.0-dev.99", Assets: []Asset{{Name: "c"}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	result, err := (SelfUpdater{HTTPClient: server.Client(), APIBase: server.URL}).Check(context.Background(), "v0.8.2", ChannelDev)
	if err != nil || result.Latest != "v0.9.0-dev.4" {
		t.Fatalf("result=%#v err=%v pages=%v", result, err, pages)
	}
	for _, page := range pages {
		if page == "9" {
			t.Fatalf("followed last: %v", pages)
		}
	}
}

func TestSelfUpdaterCheckDevStopsWhenOnlyLastLink(t *testing.T) {
	var pages []string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/mihari-proxy/mihari/releases" {
			http.NotFound(w, r)
			return
		}
		pages = append(pages, r.URL.Query().Get("page"))
		w.Header().Set("Link", `<`+server.URL+`/repos/mihari-proxy/mihari/releases?page=9>; rel="last"`)
		_ = json.NewEncoder(w).Encode([]Release{{TagName: "v0.9.0-dev.1", Assets: []Asset{{Name: "a"}}}})
	}))
	defer server.Close()
	result, err := (SelfUpdater{HTTPClient: server.Client(), APIBase: server.URL}).Check(context.Background(), "v0.8.2", ChannelDev)
	if err != nil || result.Latest != "v0.9.0-dev.1" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if len(pages) != 1 {
		t.Fatalf("pages=%v", pages)
	}
}

func TestSelfUpdaterCheckDevZeroCanonicalDoesNotFallbackLatest(t *testing.T) {
	var latestHits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			latestHits++
			_ = json.NewEncoder(w).Encode(Release{TagName: "v0.8.2"})
			return
		}
		if r.URL.Path == "/repos/mihari-proxy/mihari/releases" {
			_ = json.NewEncoder(w).Encode([]Release{
				{TagName: "v0.9.0"},
				{TagName: "v0.9.0-dev"},
				{TagName: "v0.9.0-dev.9", Draft: true},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	_, err := (SelfUpdater{HTTPClient: server.Client(), APIBase: server.URL}).Check(context.Background(), "v0.8.2", ChannelDev)
	var apiError protocol.APIError
	if err == nil || !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure {
		t.Fatalf("err=%v", err)
	}
	if latestHits != 0 {
		t.Fatalf("latestHits=%d", latestHits)
	}
}

func TestSelfUpdaterCheckDevRejectsOversizedListBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/mihari-proxy/mihari/releases" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(bytes.Repeat([]byte("x"), (8<<20)+1))
	}))
	defer server.Close()
	_, err := (SelfUpdater{HTTPClient: server.Client(), APIBase: server.URL}).Check(context.Background(), "v0.8.2", ChannelDev)
	var apiError protocol.APIError
	if err == nil || !errors.As(err, &apiError) || apiError.Code != protocol.CodeDataFailure {
		t.Fatalf("err=%v", err)
	}
}

func TestSelfUpdaterCheckDevFetchesTagWhenAssetsMissing(t *testing.T) {
	var tagHits int
	assets := []Asset{{Name: "mihari-linux-amd64", URL: "https://example.invalid/bin", Size: 10}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/mihari-proxy/mihari/releases":
			_ = json.NewEncoder(w).Encode([]Release{{TagName: "v0.9.0-dev.3"}})
		case "/repos/mihari-proxy/mihari/releases/tags/v0.9.0-dev.3":
			tagHits++
			_ = json.NewEncoder(w).Encode(Release{TagName: "v0.9.0-dev.3", Assets: assets})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	result, err := (SelfUpdater{HTTPClient: server.Client(), APIBase: server.URL}).Check(context.Background(), "v0.8.2", ChannelDev)
	if err != nil || result.Latest != "v0.9.0-dev.3" || tagHits != 1 {
		t.Fatalf("result=%#v err=%v tagHits=%d", result, err, tagHits)
	}

	tagHits = 0
	serverComplete := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/mihari-proxy/mihari/releases":
			_ = json.NewEncoder(w).Encode([]Release{{TagName: "v0.9.0-dev.3", Assets: assets}})
		case "/repos/mihari-proxy/mihari/releases/tags/v0.9.0-dev.3":
			tagHits++
			_ = json.NewEncoder(w).Encode(Release{TagName: "v0.9.0-dev.3", Assets: assets})
		default:
			http.NotFound(w, r)
		}
	}))
	defer serverComplete.Close()
	result, err = (SelfUpdater{HTTPClient: serverComplete.Client(), APIBase: serverComplete.URL}).Check(context.Background(), "v0.8.2", ChannelDev)
	if err != nil || result.Latest != "v0.9.0-dev.3" || tagHits != 0 {
		t.Fatalf("complete result=%#v err=%v tagHits=%d", result, err, tagHits)
	}
}

func TestSelfUpdaterDevNextReleaseLink(t *testing.T) {
	tests := []struct {
		name string
		link string
		want string
	}{
		{
			name: "next and last",
			link: `<https://api.github.com/repos/mihari-proxy/mihari/releases?page=2>; rel="next", <https://api.github.com/repos/mihari-proxy/mihari/releases?page=9>; rel="last"`,
			want: "https://api.github.com/repos/mihari-proxy/mihari/releases?page=2",
		},
		{
			name: "only last",
			link: `<https://api.github.com/repos/mihari-proxy/mihari/releases?page=9>; rel="last"`,
		},
		{
			name: "prev and first",
			link: `<https://api.github.com/repos/mihari-proxy/mihari/releases?page=1>; rel="prev", <https://api.github.com/repos/mihari-proxy/mihari/releases?page=1>; rel="first"`,
		},
		{
			name: "empty",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header := http.Header{}
			if test.link != "" {
				header.Set("Link", test.link)
			}
			if got := nextReleaseLink(header); got != test.want {
				t.Fatalf("got=%q want=%q", got, test.want)
			}
		})
	}
}

const oldBinaryContent = "old"

type selfUpdateServerConfig struct {
	tag            string
	assets         func(string) []Asset
	checksumBody   string
	checksumStatus int
	breakChecksum  bool
	onChecksum     func(*http.Request)
	binaryBody     []byte
	binaryStatus   int
	breakBinary    bool
	streamBinary   int64
}

type selfUpdateEnv struct {
	binaryPath   string
	serverURL    string
	afterReplace bool
	checksumReqs int
	binaryReqs   int
	updater      SelfUpdater
}

func fixtureSHA256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func linuxReleaseAssets(serverURL string, payload []byte, checksumSize int64) []Asset {
	return []Asset{
		{Name: "mihari-linux-amd64", URL: serverURL + "/asset", Size: int64(len(payload))},
		{Name: "SHA256SUMS.txt", URL: serverURL + "/checksum", Size: checksumSize},
	}
}

func writeTruncatedHTTP(w http.ResponseWriter) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 32\r\n\r\npartial"))
	_ = conn.Close()
}

func startSelfUpdateEnv(t *testing.T, cfg selfUpdateServerConfig) *selfUpdateEnv {
	t.Helper()
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "mihari-test-bin")
	if err := os.WriteFile(binaryPath, []byte(oldBinaryContent), 0o755); err != nil {
		t.Fatal(err)
	}
	if cfg.tag == "" {
		cfg.tag = "v9.9.9"
	}
	if cfg.binaryBody == nil {
		cfg.binaryBody = []byte("new-binary-content")
	}
	if cfg.assets == nil {
		cfg.assets = func(serverURL string) []Asset {
			return linuxReleaseAssets(serverURL, cfg.binaryBody, int64(len(cfg.checksumBody)))
		}
	}
	env := &selfUpdateEnv{binaryPath: binaryPath}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/mihari-proxy/mihari/releases/latest":
			_ = json.NewEncoder(w).Encode(Release{TagName: cfg.tag, Assets: cfg.assets(server.URL)})
		case r.URL.Path == "/repos/mihari-proxy/mihari/releases":
			_ = json.NewEncoder(w).Encode([]Release{{TagName: cfg.tag, Assets: cfg.assets(server.URL)}})
		case strings.HasPrefix(r.URL.Path, "/repos/mihari-proxy/mihari/releases/tags/"):
			_ = json.NewEncoder(w).Encode(Release{TagName: cfg.tag, Assets: cfg.assets(server.URL)})
		case r.URL.Path == "/checksum" || r.URL.Path == "/checksum2":
			env.checksumReqs++
			if cfg.onChecksum != nil {
				cfg.onChecksum(r)
				if r.Context().Err() != nil {
					return
				}
			}
			if cfg.breakChecksum {
				writeTruncatedHTTP(w)
				return
			}
			status := cfg.checksumStatus
			if status == 0 {
				status = http.StatusOK
			}
			w.WriteHeader(status)
			if status == http.StatusOK {
				_, _ = io.WriteString(w, cfg.checksumBody)
			}
		case r.URL.Path == "/asset":
			env.binaryReqs++
			if cfg.breakBinary {
				writeTruncatedHTTP(w)
				return
			}
			status := cfg.binaryStatus
			if status == 0 {
				status = http.StatusOK
			}
			w.WriteHeader(status)
			if status != http.StatusOK {
				return
			}
			if cfg.streamBinary > 0 {
				_, _ = io.Copy(w, io.LimitReader(zeroReader{}, cfg.streamBinary))
				return
			}
			_, _ = w.Write(cfg.binaryBody)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	env.serverURL = server.URL
	env.updater = SelfUpdater{
		HTTPClient: server.Client(),
		APIBase:    server.URL,
		GOOS:       "linux",
		GOARCH:     "amd64",
		AfterReplace: func(context.Context, string) error {
			env.afterReplace = true
			return nil
		},
	}
	return env
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func errorContainsURL(err error, serverURL string) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	if serverURL != "" && strings.Contains(text, serverURL) {
		return true
	}
	return strings.Contains(text, "http://") || strings.Contains(text, "https://")
}

func assertFailClosedUpdate(t *testing.T, env *selfUpdateEnv, err error, wantCode protocol.ErrorCode, requireAPIError bool) {
	t.Helper()
	got, readErr := os.ReadFile(env.binaryPath)
	if readErr != nil || string(got) != oldBinaryContent {
		t.Fatalf("old binary changed: %q err=%v updateErr=%v", got, readErr, err)
	}
	if env.afterReplace {
		t.Fatalf("AfterReplace called; updateErr=%v", err)
	}
	staging := filepath.Join(filepath.Dir(env.binaryPath), ".mihari-update")
	if _, statErr := os.Stat(staging); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("staging still present: %v updateErr=%v", statErr, err)
	}
	if requireAPIError {
		var apiError protocol.APIError
		if err == nil || !errors.As(err, &apiError) || apiError.Code != wantCode {
			t.Fatalf("err=%v want code=%s", err, wantCode)
		}
	} else if err == nil {
		t.Fatal("expected error")
	}
	if errorContainsURL(err, env.serverURL) {
		t.Fatalf("error contains URL: %v", err)
	}
}
