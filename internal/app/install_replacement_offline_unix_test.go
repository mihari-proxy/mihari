//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
	"github.com/mihari-proxy/mihari/internal/update"
)

func TestNativeInstallReplacement_OfflineDigestCannotClaimAnotherTag(t *testing.T) {
	ctx, root := nativeInstallFixture(t)
	install := filepath.Join(root, "install")
	trust := filepath.Join(install, "install-trust")
	if err := os.MkdirAll(trust, 0700); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(root, "candidate")
	raw := []byte("#!/bin/sh\nprintf '%s\\n' '{\"schema\":\"mihari/v1\",\"version\":\"v1.0.0\"}'\n")
	if err := os.WriteFile(candidate, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trust, "manifest.json"), []byte(`{"binaries":["`+sha256HexBytes(raw)+`"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	req := InstallRequest{Schema: InstallRequestSchema, Operation: InstallOperationInstall, Layout: InstallLayoutPrivate, Data: filepath.Join(root, "data"), InstallRoot: install, Binary: candidate, ReleaseTag: "v9.0.0", Channel: "main"}
	layout, err := platform.ResolveLayout(platform.LayoutInput{CWD: "/", Data: req.Data, InstallRoot: install, EUID: 0}, platform.SystemLayoutDefaults())
	if err != nil {
		t.Fatal(err)
	}
	i := &UnixInstaller{layout: layout, binary: filepath.Join(root, "helper"), channel: "main", offlineRoot: trust, adapter: func(service.ActionHook) service.RecoveryAdapter {
		return replacementDefinitionAdapter{definition: service.Definition{Status: service.StatusNotInstalled}}
	}, client: &http.Client{Transport: replacementTransport(func(*http.Request) (*http.Response, error) {
		t.Error("offline version binding contacted network")
		return nil, errors.New("network forbidden")
	})}}
	for _, yes := range []bool{false, true} {
		result, err := i.ApplyWithConsent(ctx, req, update.ReplacementConsent{Yes: yes})
		if err == nil || !strings.Contains(err.Error(), "offline candidate version does not match release tag") || result.Changed {
			t.Fatalf("digest-only trust accepted forged version: yes=%v result=%+v err=%v", yes, result, err)
		}
		if _, err := os.Stat(req.Data); !os.IsNotExist(err) {
			t.Fatalf("refusal created business data: %v", err)
		}
		entries, err := os.ReadDir(install)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Name() != "install-trust" {
			t.Fatalf("refusal left install mutation: %v", entries)
		}
	}
}

func TestNativeInstallReplacement_OfflineVersionBinding(t *testing.T) {
	ctx, root := nativeInstallFixture(t)
	for _, tc := range []struct {
		name, version, tag string
		want               bool
	}{
		{"matching", "1.2.3", "v1.2.3", true},
		{"mismatch", "v1.0.0", "v2.0.0", false},
		{"unknown", "dirty", "v1.0.0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte("#!/bin/sh\nprintf '%s\\n' '{\"schema\":\"mihari/v1\",\"version\":\"" + tc.version + "\"}'\n")
			err := verifyOfflineReplacementTag(ctx, root, raw, tc.tag)
			if (err == nil) != tc.want {
				t.Fatalf("accepted=%v want=%v err=%v", err == nil, tc.want, err)
			}
			entries, e := os.ReadDir(root)
			if e != nil {
				t.Fatal(e)
			}
			if len(entries) != 0 {
				t.Fatal("version staging leaked")
			}
		})
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := verifyOfflineReplacementTag(cancelled, root, []byte("inert"), "v1.0.0"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestNativeInstallReplacement_OfflineProbeTimeoutCleansStage(t *testing.T) {
	ctx, root := nativeInstallFixture(t)
	bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	err := verifyOfflineReplacementTag(bounded, root, []byte("#!/bin/sh\nwhile :; do :; done\n"), "v1.0.0")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("probe ignored timeout: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("timeout left candidate staging")
	}
}

func TestNativeInstallReplacement_UntrustedCandidateIsNotProbed(t *testing.T) {
	ctx, root := nativeInstallFixture(t)
	trust := filepath.Join(root, "trust")
	if err := os.Mkdir(trust, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trust, "manifest.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "executed")
	candidate := filepath.Join(root, "candidate")
	raw := []byte("#!/bin/sh\nprintf x > '" + marker + "'\n")
	if err := os.WriteFile(candidate, raw, 0755); err != nil {
		t.Fatal(err)
	}
	calls := 0
	client := &http.Client{Transport: replacementTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("official fixture unavailable")
	})}
	req := InstallRequest{Binary: candidate, ReleaseTag: "v1.0.0"}
	inputs, err := prepareNativeReleaseInputs(ctx, req, "", trust, client)
	if err == nil || inputs != nil || calls != 1 {
		t.Fatalf("untrusted preparation: inputs=%v calls=%d err=%v", inputs, calls, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("untrusted candidate was executed")
	}
}
