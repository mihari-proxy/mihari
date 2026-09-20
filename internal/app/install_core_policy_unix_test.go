//go:build linux || darwin

package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeCoreInputs_AcceptsBundledVersionWithoutAdditionalDownload(t *testing.T) {
	ctx, root := nativeInstallFixture(t)
	appBinary, coreBinary := []byte("verified Mihari fixture"), []byte("newer official bundled core fixture")
	var packed bytes.Buffer
	gz := gzip.NewWriter(&packed)
	tw := tar.NewWriter(gz)
	for name, body := range map[string][]byte{"mihari": appBinary, "data/bin/mihomo": coreBinary, "data/bin/core-channel": []byte("alpha\n")} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0700, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		t.Fatal(err)
	}
	trust := filepath.Join(root, "trust")
	if err := os.Mkdir(trust, 0700); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(compiledInstallTrustFile{Binaries: []string{sha256HexBytes(appBinary)}, Bundles: []string{sha256HexBytes(packed.Bytes())}})
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{"trust/manifest.json": manifest, "candidate": appBinary, "bundle.tar.gz": packed.Bytes()} {
		if err := os.WriteFile(filepath.Join(root, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	client := &http.Client{Transport: replacementTransport(func(*http.Request) (*http.Response, error) {
		t.Error("verified offline bundle requested another core")
		return nil, errors.New("network forbidden")
	})}
	inputs, err := prepareNativeReleaseInputs(ctx, InstallRequest{Binary: filepath.Join(root, "candidate"), Bundle: filepath.Join(root, "bundle.tar.gz"), ReleaseTag: "v1.0.0"}, "", trust, client)
	if err != nil {
		t.Fatal(err)
	}
	defer inputs.Close()
	if !bytes.Equal(inputs.core, coreBinary) || string(inputs.resources["bin/core-channel"]) != "alpha\n" {
		t.Fatalf("bundled core or channel changed: %q, %q", inputs.core, inputs.resources["bin/core-channel"])
	}
}
