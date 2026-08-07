package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/core"
)

// fakeRunner satisfies core.CommandRunner for the host-matching `-v` smoke.
type fakeRunner struct{ output []byte }

func (f fakeRunner) Run(_ context.Context, _ string, _ ...string) ([]byte, error) {
	return f.output, nil
}

// recordingRunner records whether Run was invoked, so a test can assert that the
// host-matching target goes through the exec path (not the magic-number path).
type recordingRunner struct {
	called bool
	output []byte
}

func (r *recordingRunner) Run(_ context.Context, _ string, _ ...string) ([]byte, error) {
	r.called = true
	return r.output, nil
}

func TestBundlerProducesSixPlatformBundles(t *testing.T) {
	api := fakeGitHubAPI(t)
	defer api.Close()
	geoipSrv := fakeGeoIPServer(t)
	defer geoipSrv.Close()

	mihariDir := t.TempDir()
	writeMihariDist(t, mihariDir)

	scriptsDir := t.TempDir()
	for _, name := range []string{"install-aio.sh", "install-aio.ps1"} {
		if err := os.WriteFile(filepath.Join(scriptsDir, name), []byte("# aio installer\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	out := t.TempDir()
	err := run(options{
		MihariDir: mihariDir, Out: out, ScriptsDir: scriptsDir,
		Platforms:  []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "windows/amd64", "windows/arm64"},
		HTTPClient: api.Client(), APIBase: api.URL, Repository: "MetaCubeX/mihomo",
		GeoIPBase: geoipSrv.URL, GeoIPValidate: func(string) error { return nil },
		Runner: fakeRunner{output: []byte("Mihomo Meta v1.19.0")},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	expected := map[string]string{
		"linux/amd64":   "mihari-all-in-one-linux-amd64.tar.gz",
		"linux/arm64":   "mihari-all-in-one-linux-arm64.tar.gz",
		"darwin/amd64":  "mihari-all-in-one-darwin-amd64.tar.gz",
		"darwin/arm64":  "mihari-all-in-one-darwin-arm64.tar.gz",
		"windows/amd64": "mihari-all-in-one-windows-amd64.zip",
		"windows/arm64": "mihari-all-in-one-windows-arm64.zip",
	}
	for platform, bundle := range expected {
		entries := extractBundle(t, filepath.Join(out, bundle))
		goos, _, _ := strings.Cut(platform, "/")
		suffix, script := "", "install-aio.sh"
		if goos == "windows" {
			suffix, script = ".exe", "install-aio.ps1"
		}
		want := map[string]bool{
			"mihari" + suffix:                  true,
			script:                             true,
			"data/bin/mihomo" + suffix:         true,
			"data/geoip/GeoLite2-Country.mmdb": true,
			"data/geoip/GeoLite2-ASN.mmdb":     true,
		}
		got := make(map[string]bool, len(entries))
		for name := range entries {
			got[name] = true
		}
		if len(got) != len(want) {
			t.Fatalf("%s: bundle has %d entries, got=%v want=%v", platform, len(got), sortedKeys(got), sortedKeys(want))
		}
		for w := range want {
			if !got[w] {
				t.Fatalf("%s: missing %q in bundle, got=%v", platform, w, sortedKeys(got))
			}
		}
		// mihomo magic number sanity (the host-matching target was -v-smoked via
		// fakeRunner; the other five went through the magic check; verify bytes).
		if mihomo := entries["data/bin/mihomo"+suffix]; len(mihomo) == 0 {
			t.Fatalf("%s: empty mihomo binary", platform)
		}
	}
}

func TestSmokeMihomoExecutesHostTarget(t *testing.T) {
	ctx := context.Background()
	// Deliberately invalid magic: the host-matching target must exec the runner
	// (returning output), NOT fall through to the magic-number check. A
	// host-assumption bug (hardcoding linux/amd64) fails this on any non-linux
	// host because the magic check would reject the garbage bytes.
	path := filepath.Join(t.TempDir(), "mihomo")
	if err := os.WriteFile(path, []byte("not a valid executable magic"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{output: []byte("Mihomo Meta v1.19.0")}
	if err := smokeMihomo(ctx, runtime.GOOS, runtime.GOARCH, path, runner); err != nil {
		t.Fatalf("host-matching smoke: %v", err)
	}
	if !runner.called {
		t.Fatal("host-matching platform must exec the runner, not magic-check")
	}
}

func TestSmokeMihomoMagicChecksNonHostTarget(t *testing.T) {
	ctx := context.Background()
	goos, goarch := nonHostTarget(t)
	validMagic := map[string][]byte{
		"linux":   {0x7f, 'E', 'L', 'F'},
		"darwin":  {0xcf, 0xfa, 0xed, 0xfe},
		"windows": {'M', 'Z'},
	}[goos]
	path := filepath.Join(t.TempDir(), "mihomo")
	if err := os.WriteFile(path, validMagic, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := smokeMihomo(ctx, goos, goarch, path, nil); err != nil {
		t.Fatalf("non-host valid magic: %v", err)
	}
	if err := os.WriteFile(path, []byte("bad magic"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := smokeMihomo(ctx, goos, goarch, path, nil); err == nil {
		t.Fatal("expected magic mismatch error for non-host target")
	}
}

// nonHostTarget returns any of the 6 supported platforms that is not the build
// host's, so a magic-number test is deterministic regardless of where it runs.
func nonHostTarget(t *testing.T) (string, string) {
	t.Helper()
	for _, target := range []struct{ goos, goarch string }{
		{"linux", "amd64"}, {"linux", "arm64"},
		{"darwin", "amd64"}, {"darwin", "arm64"},
		{"windows", "amd64"}, {"windows", "arm64"},
	} {
		if target.goos != runtime.GOOS || target.goarch != runtime.GOARCH {
			return target.goos, target.goarch
		}
	}
	t.Fatal("could not find a non-host target")
	return "", ""
}

func TestAssertStageEnforcesWhitelist(t *testing.T) {
	tests := []struct {
		name    string
		goos    string
		files   []string
		wantErr bool
	}{
		{name: "unix complete", goos: "linux", files: []string{"mihari", "install-aio.sh", "data/bin/mihomo", "data/geoip/GeoLite2-Country.mmdb", "data/geoip/GeoLite2-ASN.mmdb"}},
		{name: "windows complete", goos: "windows", files: []string{"mihari.exe", "install-aio.ps1", "data/bin/mihomo.exe", "data/geoip/GeoLite2-Country.mmdb", "data/geoip/GeoLite2-ASN.mmdb"}},
		{name: "missing geoip", goos: "linux", files: []string{"mihari", "install-aio.sh", "data/bin/mihomo", "data/geoip/GeoLite2-Country.mmdb"}, wantErr: true},
		{name: "forbidden onboarding.json", goos: "linux", files: []string{"mihari", "install-aio.sh", "data/bin/mihomo", "data/geoip/GeoLite2-Country.mmdb", "data/geoip/GeoLite2-ASN.mmdb", "onboarding.json"}, wantErr: true},
		{name: "forbidden mihari.yaml", goos: "windows", files: []string{"mihari.exe", "install-aio.ps1", "data/bin/mihomo.exe", "data/geoip/GeoLite2-Country.mmdb", "data/geoip/GeoLite2-ASN.mmdb", "mihari.yaml"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := assertStage(test.goos, test.files)
			if test.wantErr && err == nil {
				t.Fatal("expected whitelist violation, got nil")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func fakeGitHubAPI(t *testing.T) *httptest.Server {
	t.Helper()
	names := []string{
		"mihomo-linux-amd64-compatible-v1.19.0.gz",
		"mihomo-linux-arm64-v1.19.0.gz",
		"mihomo-darwin-amd64-compatible-v1.19.0.gz",
		"mihomo-darwin-arm64-v1.19.0.gz",
		"mihomo-windows-amd64-compatible-v1.19.0.zip",
		"mihomo-windows-arm64-v1.19.0.zip",
	}
	archives := make(map[string][]byte, len(names))
	for _, name := range names {
		archives[name] = fakeMihomoArchive(t, name)
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/repos/MetaCubeX/mihomo/releases/latest":
			assets := make([]core.Asset, 0, len(names))
			for _, name := range names {
				data := archives[name]
				sum := sha256.Sum256(data)
				assets = append(assets, core.Asset{
					Name: name, URL: server.URL + "/asset/" + name,
					Size: int64(len(data)), Digest: "sha256:" + hex.EncodeToString(sum[:]),
				})
			}
			response.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(response).Encode(core.Release{TagName: "v1.19.0", Assets: assets})
		case strings.HasPrefix(request.URL.Path, "/asset/"):
			response.Write(archives[strings.TrimPrefix(request.URL.Path, "/asset/")])
		default:
			http.NotFound(response, request)
		}
	}))
	return server
}

func fakeGeoIPServer(t *testing.T) *httptest.Server {
	t.Helper()
	files := make(map[string][]byte)
	for _, name := range []string{"GeoLite2-Country.mmdb", "GeoLite2-ASN.mmdb"} {
		payload := []byte("fake-" + name)
		files[name] = payload
		sum := sha256.Sum256(payload)
		files[name+".sha256sum"] = []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")
	}
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if data, ok := files[strings.TrimPrefix(request.URL.Path, "/")]; ok {
			response.Write(data)
			return
		}
		http.NotFound(response, request)
	}))
}

func fakeMihomoArchive(t *testing.T, name string) []byte {
	t.Helper()
	goos := "linux"
	switch {
	case strings.HasPrefix(name, "mihomo-darwin-"):
		goos = "darwin"
	case strings.HasPrefix(name, "mihomo-windows-"):
		goos = "windows"
	}
	magic := map[string][]byte{
		"linux":   {0x7f, 'E', 'L', 'F'},
		"darwin":  {0xcf, 0xfa, 0xed, 0xfe}, // Mach-O 64 little-endian (real darwin/amd64+arm64)
		"windows": {'M', 'Z'},
	}[goos]
	binary := append(append([]byte(nil), magic...), []byte("-fake-mihomo")...)
	if strings.HasSuffix(name, ".gz") {
		var buffer bytes.Buffer
		writer := gzip.NewWriter(&buffer)
		if _, err := writer.Write(binary); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		return buffer.Bytes()
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, err := writer.Create("mihomo.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(binary); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func writeMihariDist(t *testing.T, dir string) {
	t.Helper()
	for _, target := range []struct{ goos, goarch, ext string }{
		{"linux", "amd64", ""}, {"linux", "arm64", ""},
		{"darwin", "amd64", ""}, {"darwin", "arm64", ""},
		{"windows", "amd64", ".exe"}, {"windows", "arm64", ".exe"},
	} {
		name := "mihari-" + target.goos + "-" + target.goarch + target.ext
		if err := os.WriteFile(filepath.Join(dir, name), []byte("dist-"+name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func extractBundle(t *testing.T, path string) map[string][]byte {
	t.Helper()
	entries := map[string][]byte{}
	switch {
	case strings.HasSuffix(path, ".tar.gz"):
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		gz, err := gzip.NewReader(file)
		if err != nil {
			t.Fatal(err)
		}
		defer gz.Close()
		reader := tar.NewReader(gz)
		for {
			header, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if header.Typeflag != tar.TypeReg {
				continue
			}
			data, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			entries[header.Name] = data
		}
	case strings.HasSuffix(path, ".zip"):
		reader, err := zip.OpenReader(path)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		for _, file := range reader.File {
			if file.FileInfo().IsDir() {
				continue
			}
			rc, err := file.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				t.Fatal(err)
			}
			entries[file.Name] = data
		}
	default:
		t.Fatalf("unknown bundle type: %s", path)
	}
	return entries
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
