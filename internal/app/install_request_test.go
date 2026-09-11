package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func readInstallFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "install", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestInstallRequest_FixtureRoundTrip(t *testing.T) {
	raw := readInstallFixture(t, "request.json")
	got, err := DecodeInstallRequest(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != InstallRequestSchema || got.Operation != InstallOperationInstall {
		t.Fatalf("schema fields: %+v", got)
	}
	if got.Binary != "/tmp/mihari-candidate" || got.Bundle == "" || got.Source == "" {
		t.Fatalf("missing path fields: %+v", got)
	}
	if got.Channel != InstallChannelMain || got.Layout != InstallLayoutSystem || got.Data != "" {
		t.Fatalf("layout fields: %+v", got)
	}
	if got.Endpoint == "" || got.Credential == "" || got.InstallRoot == "" || got.PathBinary == "" {
		t.Fatalf("override fields: %+v", got)
	}
	if got.ReleaseTag != "v1.0.0" || len(got.ArtifactSHA256) != 64 || len(got.BundleSHA256) != 64 {
		t.Fatalf("artifact fields: %+v", got)
	}
	encoded, err := EncodeInstallRequest(got)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := DecodeInstallRequest(bytes.NewReader(encoded))
	if err != nil || roundTrip != got {
		t.Fatalf("encode round-trip: %+v %v", roundTrip, err)
	}

	resultRaw := readInstallFixture(t, "result.json")
	result, err := DecodeInstallResult(bytes.NewReader(resultRaw))
	if err != nil {
		t.Fatal(err)
	}
	if result.Schema != InstallResultSchema || !result.Changed || result.ServiceStatus != InstallServiceRunning {
		t.Fatalf("result fields: %+v", result)
	}
	if result.TransactionID != "0123456789abcdef0123456789abcdef" || !result.SourceRetained {
		t.Fatalf("result identity: %+v", result)
	}
	encodedResult, err := EncodeInstallResult(result)
	if err != nil {
		t.Fatal(err)
	}
	resultAgain, err := DecodeInstallResult(bytes.NewReader(encodedResult))
	if err != nil || resultAgain != result {
		t.Fatalf("result round-trip: %+v %v", resultAgain, err)
	}
}

func TestInstallRequest_RecoverRejectsSource(t *testing.T) {
	_, err := DecodeInstallRequest(strings.NewReader(`{"schema":"mihari.install-request/v1","operation":"recover","source":"/tmp/x"}`))
	if err == nil {
		t.Fatal("recover accepted caller source")
	}
}

func TestInstallRequest_RecoverRejectsCallerPaths(t *testing.T) {
	allowed, err := DecodeInstallRequest(strings.NewReader(`{"schema":"mihari.install-request/v1","operation":"recover"}`))
	if err != nil || allowed.Operation != InstallOperationRecover {
		t.Fatalf("recover schema/operation rejected: %+v %v", allowed, err)
	}
	for name, body := range map[string]string{
		"binary":       `{"schema":"mihari.install-request/v1","operation":"recover","binary":"/tmp/candidate"}`,
		"bundle":       `{"schema":"mihari.install-request/v1","operation":"recover","bundle":"/tmp/aio"}`,
		"data":         `{"schema":"mihari.install-request/v1","operation":"recover","data":"/tmp/data"}`,
		"path_binary":  `{"schema":"mihari.install-request/v1","operation":"recover","path_binary":"/usr/local/bin/mihari"}`,
		"channel":      `{"schema":"mihari.install-request/v1","operation":"recover","channel":"main"}`,
		"empty source": `{"schema":"mihari.install-request/v1","operation":"recover","source":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeInstallRequest(strings.NewReader(body)); err == nil {
				t.Fatal("recover accepted caller path")
			}
		})
	}
}

func TestInstallRequest_SizeLimit(t *testing.T) {
	valid := `{"schema":"mihari.install-request/v1","operation":"recover"}`
	boundary := valid + strings.Repeat(" ", MaxInstallRequestBytes-len(valid))
	if _, err := DecodeInstallRequest(strings.NewReader(boundary)); err != nil {
		t.Fatalf("64KiB request rejected: %v", err)
	}
	reader := strings.NewReader(boundary + strings.Repeat(" ", 100))
	if _, err := DecodeInstallRequest(reader); err == nil {
		t.Fatal("oversized request accepted")
	}
	if reader.Len() != 99 {
		t.Fatalf("body read was not bounded: %d bytes remaining", reader.Len())
	}
}

func TestInstallRequest_RejectsMalformed(t *testing.T) {
	valid := string(readInstallFixture(t, "request.json"))
	cases := []struct{ name, body string }{
		{"unknown field", `{"schema":"mihari.install-request/v1","operation":"recover","env":"LD_LIBRARY_PATH=/evil"}`},
		{"duplicate operation", `{"schema":"mihari.install-request/v1","operation":"recover","operation":"install"}`},
		{"escaped duplicate", `{"schema":"mihari.install-request/v1","operation":"recover","opera\u0074ion":"install"}`},
		{"wrong schema", `{"schema":"mihari.install-request/v2","operation":"recover"}`},
		{"missing schema", `{"operation":"recover"}`},
		{"bad operation", `{"schema":"mihari.install-request/v1","operation":"migrate"}`},
		{"bad channel", strings.Replace(valid, `"main"`, `"nightly"`, 1)},
		{"bad layout", strings.Replace(valid, `"system"`, `"windows"`, 1)},
		{"relative binary", strings.Replace(valid, `"/tmp/mihari-candidate"`, `"candidate"`, 1)},
		{"nul path", strings.Replace(valid, `"/tmp/mihari-candidate"`, "\"/tmp/mihari\\u0000x\"", 1)},
		{"system data", strings.Replace(valid, `"layout": "system"`, `"layout": "system", "data": "/var/lib/mihari/data"`, 1)},
		{"private missing data", `{"schema":"mihari.install-request/v1","operation":"install","binary":"/tmp/mihari","channel":"main","layout":"private"}`},
		{"bundle missing hash", `{"schema":"mihari.install-request/v1","operation":"install","binary":"/tmp/mihari","bundle":"/tmp/aio","channel":"main","layout":"system"}`},
		{"short hash", strings.Replace(valid, `"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`, `"abc"`, 1)},
		{"trailing object", valid + `{}`},
		{"array", `[]`},
		{"null", `null`},
		{"truncated", `{"schema":"mihari.install-request/v1",`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeInstallRequest(strings.NewReader(test.body))
			if err == nil {
				t.Fatal("accepted")
			}
			if strings.Contains(err.Error(), "LD_LIBRARY_PATH") || strings.Contains(err.Error(), "/evil") {
				t.Fatal("env leaked into error")
			}
			var api protocol.APIError
			if errors.As(err, &api) && api.Code != protocol.CodeInvalidArgument {
				t.Fatalf("wrong code %s", api.Code)
			}
		})
	}
}

func TestInstallRequest_PrivateRequiresData(t *testing.T) {
	body := `{
		"schema":"mihari.install-request/v1",
		"operation":"install",
		"binary":"/tmp/mihari-candidate",
		"channel":"dev",
		"layout":"private",
		"data":"/var/lib/mihari-private"
	}`
	got, err := DecodeInstallRequest(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if got.Layout != InstallLayoutPrivate || got.Data != "/var/lib/mihari-private" || got.Channel != InstallChannelDev {
		t.Fatalf("private request: %+v", got)
	}
}

func TestInstallRequest_EncodeValidates(t *testing.T) {
	_, err := EncodeInstallRequest(InstallRequest{
		Schema:    InstallRequestSchema,
		Operation: InstallOperationRecover,
		Source:    "/tmp/x",
	})
	if err == nil {
		t.Fatal("encoded recover with source")
	}
	_, err = EncodeInstallResult(InstallResult{
		Schema:        InstallResultSchema,
		ServiceStatus: "degraded",
		TransactionID: "0123456789abcdef0123456789abcdef",
	})
	if err == nil {
		t.Fatal("encoded invalid service status")
	}
}

func TestInstallRequest_ErrorOmitsEnv(t *testing.T) {
	_, err := DecodeInstallRequest(strings.NewReader(`{"schema":"mihari.install-request/v1","operation":"install","binary":"/bin/mihari","channel":"main","layout":"system","PATH":"/evil","LD_PRELOAD":"x.so"}`))
	if err == nil {
		t.Fatal("accepted env fields")
	}
	if strings.Contains(err.Error(), "/evil") || strings.Contains(err.Error(), "x.so") || strings.Contains(err.Error(), "LD_PRELOAD") {
		t.Fatalf("env leaked: %v", err)
	}
}

func TestInstallRequest_JSONKeysStable(t *testing.T) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(readInstallFixture(t, "request.json"), &raw); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"schema", "operation", "binary", "bundle", "source", "channel", "layout",
		"endpoint", "credential", "install_root", "path_binary", "release_tag",
		"artifact_sha256", "bundle_sha256",
	}
	if len(raw) != len(want) {
		t.Fatalf("fixture fields %d want %d: %v", len(raw), len(want), raw)
	}
	for _, key := range want {
		if _, ok := raw[key]; !ok {
			t.Fatalf("missing fixture field %s", key)
		}
	}
}
