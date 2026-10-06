//go:build linux || darwin

package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNativeCoreInputs_AcceptsBundledVersionWithoutAdditionalDownload(t *testing.T) {
	ctx, root := nativeInstallFixture(t)
	appBinary, coreBinary := []byte("verified Mihari fixture"), []byte("newer official bundled core fixture")
	var packed bytes.Buffer
	gz := gzip.NewWriter(&packed)
	tw := tar.NewWriter(gz)
	for name, body := range map[string][]byte{"mihari": appBinary, "data/bin/mihomo": coreBinary, "data/bin/core-channel": []byte("alpha\nalpha-e183c58\n")} {
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
	t.Cleanup(func() {
		if err := inputs.Close(); err != nil {
			t.Error(err)
		}
	})
	if !bytes.Equal(inputs.core, coreBinary) || string(inputs.resources["bin/core-channel"]) != "alpha\nalpha-e183c58\n" {
		t.Fatalf("bundled core or channel changed: %q, %q", inputs.core, inputs.resources["bin/core-channel"])
	}
}

func TestNativeCoreInputs_AcceptsStampedCoreChannel(t *testing.T) {
	sidecar := []byte("stable\nstable-v1.19.30\n")
	inputs := preparePinnedCoreBundle(t, sidecar)
	if string(inputs.resources["bin/core-channel"]) != string(sidecar) {
		t.Fatalf("sidecar = %q", inputs.resources["bin/core-channel"])
	}
}

func TestNativeCoreInputs_RejectsCoreChannelWithoutStamp(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "channel only", body: "stable\n"},
		{name: "dev channel", body: "dev\ndev-1\n"},
		{name: "blank stamp", body: "stable\n \n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputs, err := preparePinnedCoreBundleResult(t, []byte(tt.body))
			if err == nil || inputs != nil || !strings.Contains(err.Error(), "unsupported bundled core channel") {
				t.Fatalf("inputs=%v err=%v", inputs, err)
			}
		})
	}
}

func TestNativeCoreInputs_BundleUsesChannelIndexNotGitHub(t *testing.T) {
	ctx, root := nativeInstallFixture(t)
	appBinary := []byte("verified Mihari fixture")
	coreBinary := []byte("newer official bundled core fixture")
	bundle := nativeCoreBundle(t, map[string][]byte{
		"mihari":                appBinary,
		"data/bin/mihomo":       coreBinary,
		"data/bin/core-channel": []byte("alpha\nalpha-e183c58\n"),
	})
	bundleHash := sha256HexBytes(bundle)
	if bundleHash == channelIndexOtherSum {
		t.Fatal("fixture bundle hash collided with the mismatch digest")
	}
	for name, body := range map[string][]byte{"candidate": appBinary, "bundle.tar.gz": bundle} {
		if err := os.WriteFile(filepath.Join(root, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	req := InstallRequest{
		Binary:     filepath.Join(root, "candidate"),
		Bundle:     filepath.Join(root, "bundle.tar.gz"),
		ReleaseTag: "v1.2.3-dev.1",
		Channel:    InstallChannelDev,
	}
	offline := filepath.Join(root, "missing-trust")
	client, calls := channelIndexInstallClient(t, "v1.2.3-dev.1", bundleHash)
	inputs, err := prepareNativeReleaseInputs(ctx, req, "", offline, client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := inputs.Close(); err != nil {
			t.Error(err)
		}
	})
	if *calls != 1 || inputs.offlineBinary || !inputs.trust.acceptsBundle(bundleHash) || !inputs.trust.acceptsBinary(sha256HexBytes(appBinary)) {
		t.Fatalf("calls=%d offline=%v bundle=%v binary=%v", *calls, inputs.offlineBinary, inputs.trust.acceptsBundle(bundleHash), inputs.trust.acceptsBinary(sha256HexBytes(appBinary)))
	}
	if !bytes.Equal(inputs.core, coreBinary) || string(inputs.resources["bin/core-channel"]) != "alpha\nalpha-e183c58\n" {
		t.Fatalf("bundled core or channel changed: %q, %q", inputs.core, inputs.resources["bin/core-channel"])
	}

	t.Run("checksum mismatch", func(t *testing.T) {
		client, calls := channelIndexInstallClient(t, "v1.2.3-dev.1", channelIndexOtherSum)
		inputs, err := prepareNativeReleaseInputs(ctx, req, "", offline, client)
		if err == nil || inputs != nil || *calls != 1 || !strings.Contains(err.Error(), "install bundle checksum mismatch") {
			t.Fatalf("inputs=%v calls=%d err=%v", inputs, *calls, err)
		}
	})
	t.Run("latest mismatch", func(t *testing.T) {
		client, calls := channelIndexInstallClient(t, "v1.2.3-dev.2", bundleHash)
		inputs, err := prepareNativeReleaseInputs(ctx, req, "", offline, client)
		if err == nil || inputs != nil || *calls != 1 || !strings.Contains(err.Error(), "channel index latest does not match release tag") {
			t.Fatalf("inputs=%v calls=%d err=%v", inputs, *calls, err)
		}
	})
	t.Run("member mismatch", func(t *testing.T) {
		other := filepath.Join(root, "other-candidate")
		if err := os.WriteFile(other, []byte("different Mihari fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		mismatched := req
		mismatched.Binary = other
		client, calls := channelIndexInstallClient(t, "v1.2.3-dev.1", bundleHash)
		inputs, err := prepareNativeReleaseInputs(ctx, mismatched, "", offline, client)
		if err == nil || inputs != nil || *calls != 1 || !strings.Contains(err.Error(), "bundle binary does not match verified candidate") {
			t.Fatalf("inputs=%v calls=%d err=%v", inputs, *calls, err)
		}
	})
}

func TestNativeCoreInputs_BinaryPinSkipsChannelIndex(t *testing.T) {
	ctx, root := nativeInstallFixture(t)
	appBinary := []byte("verified Mihari fixture")
	coreBinary := []byte("newer official bundled core fixture")
	bundle := nativeCoreBundle(t, map[string][]byte{
		"mihari":                appBinary,
		"data/bin/mihomo":       coreBinary,
		"data/bin/core-channel": []byte("alpha\nalpha-e183c58\n"),
	})
	trust := filepath.Join(root, "trust")
	if err := os.Mkdir(trust, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"binaries":["` + sha256HexBytes(appBinary) + `"],"bundles":[]}`)
	for name, body := range map[string][]byte{"trust/manifest.json": manifest, "candidate": appBinary, "bundle.tar.gz": bundle} {
		if err := os.WriteFile(filepath.Join(root, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	client := &http.Client{Transport: replacementTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		t.Errorf("binary pin requested the network: %s", r.URL.String())
		return nil, errors.New("network forbidden")
	})}
	inputs, err := prepareNativeReleaseInputs(ctx, InstallRequest{
		Binary:     filepath.Join(root, "candidate"),
		Bundle:     filepath.Join(root, "bundle.tar.gz"),
		ReleaseTag: "v1.2.3-dev.1",
		Channel:    InstallChannelDev,
	}, "", trust, client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := inputs.Close(); err != nil {
			t.Error(err)
		}
	})
	if calls != 0 || !inputs.offlineBinary || !inputs.trust.acceptsBundle(sha256HexBytes(bundle)) {
		t.Fatalf("calls=%d offline=%v bundle=%v", calls, inputs.offlineBinary, inputs.trust.acceptsBundle(sha256HexBytes(bundle)))
	}
	if !bytes.Equal(inputs.core, coreBinary) || string(inputs.resources["bin/core-channel"]) != "alpha\nalpha-e183c58\n" {
		t.Fatalf("bundled core or channel changed: %q, %q", inputs.core, inputs.resources["bin/core-channel"])
	}

	t.Run("member mismatch", func(t *testing.T) {
		other := []byte("different Mihari fixture")
		otherPath := filepath.Join(root, "other-candidate")
		if err := os.WriteFile(otherPath, other, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(trust, "manifest.json"), []byte(`{"binaries":["`+sha256HexBytes(other)+`"],"bundles":[]}`), 0600); err != nil {
			t.Fatal(err)
		}
		mismatchCalls := 0
		mismatchClient := &http.Client{Transport: replacementTransport(func(r *http.Request) (*http.Response, error) {
			mismatchCalls++
			t.Errorf("binary pin requested the network: %s", r.URL.String())
			return nil, errors.New("network forbidden")
		})}
		inputs, err := prepareNativeReleaseInputs(ctx, InstallRequest{
			Binary:     otherPath,
			Bundle:     filepath.Join(root, "bundle.tar.gz"),
			ReleaseTag: "v1.2.3-dev.1",
			Channel:    InstallChannelDev,
		}, "", trust, mismatchClient)
		if err == nil || inputs != nil || mismatchCalls != 0 || !strings.Contains(err.Error(), "bundle binary does not match verified candidate") {
			t.Fatalf("inputs=%v calls=%d err=%v", inputs, mismatchCalls, err)
		}
	})
}

func TestNativeCoreInputs_BundlePinSkipsChannelIndex(t *testing.T) {
	ctx, root := nativeInstallFixture(t)
	appBinary := []byte("verified Mihari fixture")
	coreBinary := []byte("newer official bundled core fixture")
	bundle := nativeCoreBundle(t, map[string][]byte{
		"mihari":                appBinary,
		"data/bin/mihomo":       coreBinary,
		"data/bin/core-channel": []byte("alpha\nalpha-e183c58\n"),
	})
	trust := filepath.Join(root, "trust")
	if err := os.Mkdir(trust, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"binaries":[],"bundles":["` + sha256HexBytes(bundle) + `"]}`)
	for name, body := range map[string][]byte{"trust/manifest.json": manifest, "candidate": appBinary, "bundle.tar.gz": bundle} {
		if err := os.WriteFile(filepath.Join(root, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	client := &http.Client{Transport: replacementTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		t.Errorf("bundle pin requested the network: %s", r.URL.String())
		return nil, errors.New("network forbidden")
	})}
	inputs, err := prepareNativeReleaseInputs(ctx, InstallRequest{
		Binary:     filepath.Join(root, "candidate"),
		Bundle:     filepath.Join(root, "bundle.tar.gz"),
		ReleaseTag: "v1.2.3-dev.1",
		Channel:    InstallChannelDev,
	}, "", trust, client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := inputs.Close(); err != nil {
			t.Error(err)
		}
	})
	if calls != 0 || inputs.offlineBinary || !inputs.trust.acceptsBundle(sha256HexBytes(bundle)) || !inputs.trust.acceptsBinary(sha256HexBytes(appBinary)) {
		t.Fatalf("calls=%d offline=%v bundle=%v binary=%v", calls, inputs.offlineBinary, inputs.trust.acceptsBundle(sha256HexBytes(bundle)), inputs.trust.acceptsBinary(sha256HexBytes(appBinary)))
	}
	if !bytes.Equal(inputs.core, coreBinary) {
		t.Fatalf("bundled core changed: %q", inputs.core)
	}
}

func TestNativeCoreInputs_MissingMemberRejected(t *testing.T) {
	ctx, root := nativeInstallFixture(t)
	appBinary := []byte("verified Mihari fixture")
	bundle := nativeCoreBundle(t, map[string][]byte{
		"data/bin/core-channel": []byte("alpha\nalpha-e183c58\n"),
	})
	trust := filepath.Join(root, "trust")
	if err := os.Mkdir(trust, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"binaries":[],"bundles":["` + sha256HexBytes(bundle) + `"]}`)
	for name, body := range map[string][]byte{"trust/manifest.json": manifest, "candidate": appBinary, "bundle.tar.gz": bundle} {
		if err := os.WriteFile(filepath.Join(root, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	client := &http.Client{Transport: replacementTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		t.Errorf("pinned bundle requested the network: %s", r.URL.String())
		return nil, errors.New("network forbidden")
	})}
	inputs, err := prepareNativeReleaseInputs(ctx, InstallRequest{
		Binary:     filepath.Join(root, "candidate"),
		Bundle:     filepath.Join(root, "bundle.tar.gz"),
		ReleaseTag: "v1.2.3-dev.1",
		Channel:    InstallChannelDev,
	}, "", trust, client)
	if err == nil || inputs != nil || calls != 0 || !strings.Contains(err.Error(), "bundle binary does not match verified candidate") {
		t.Fatalf("inputs=%v calls=%d err=%v", inputs, calls, err)
	}
}

func preparePinnedCoreBundle(t *testing.T, sidecar []byte) *nativeReleaseInputs {
	t.Helper()
	inputs, err := preparePinnedCoreBundleResult(t, sidecar)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := inputs.Close(); err != nil {
			t.Error(err)
		}
	})
	return inputs
}

func preparePinnedCoreBundleResult(t *testing.T, sidecar []byte) (*nativeReleaseInputs, error) {
	t.Helper()
	ctx, root := nativeInstallFixture(t)
	appBinary := []byte("verified Mihari fixture")
	coreBinary := []byte("newer official bundled core fixture")
	bundle := nativeCoreBundle(t, map[string][]byte{
		"mihari":                appBinary,
		"data/bin/mihomo":       coreBinary,
		"data/bin/core-channel": sidecar,
	})
	trust := filepath.Join(root, "trust")
	if err := os.Mkdir(trust, 0700); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(compiledInstallTrustFile{Binaries: []string{sha256HexBytes(appBinary)}, Bundles: []string{sha256HexBytes(bundle)}})
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{"trust/manifest.json": manifest, "candidate": appBinary, "bundle.tar.gz": bundle} {
		if err := os.WriteFile(filepath.Join(root, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	client := &http.Client{Transport: replacementTransport(func(*http.Request) (*http.Response, error) {
		t.Error("pinned bundle requested the network")
		return nil, errors.New("network forbidden")
	})}
	return prepareNativeReleaseInputs(ctx, InstallRequest{Binary: filepath.Join(root, "candidate"), Bundle: filepath.Join(root, "bundle.tar.gz"), ReleaseTag: "v1.0.0"}, "", trust, client)
}

func nativeCoreBundle(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var packed bytes.Buffer
	gz := gzip.NewWriter(&packed)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
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
	return packed.Bytes()
}

func channelIndexInstallClient(t *testing.T, latest, sum string) (*http.Client, *int) {
	t.Helper()
	calls := 0
	indexURL, err := channelIndexURL(InstallChannelDev)
	if err != nil || indexURL != channelIndexDevURL {
		t.Fatalf("dev index url=%q err=%v", indexURL, err)
	}
	platform := runtime.GOOS + "-" + runtime.GOARCH
	client := &http.Client{Transport: replacementTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		host := r.URL.Hostname()
		if host == "github.com" || strings.HasSuffix(host, ".github.com") || strings.Contains(host, "githubusercontent.com") {
			t.Errorf("bundle install requested GitHub: %s", r.URL.String())
			return nil, errors.New("github forbidden")
		}
		if r.URL.String() != indexURL {
			t.Errorf("bundle install requested %s", r.URL.String())
			return nil, errors.New("unexpected url")
		}
		body := "latest " + latest + "\n" + platform + " https://example.invalid/bundle " + sum + "\n"
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})}
	return client, &calls
}
