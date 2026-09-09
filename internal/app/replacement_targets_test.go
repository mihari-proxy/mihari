package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/service"
	"github.com/mihari-proxy/mihari/internal/update"
)

type replacementServiceFake struct {
	view                 service.ServiceReplacementView
	calls, stops, stages int
	between              func()
	observe              func()
}

func (f *replacementServiceFake) ObserveReplacementService(context.Context) (service.ServiceReplacementView, error) {
	if f.observe != nil {
		f.observe()
	}
	return f.view, nil
}
func (f *replacementServiceFake) UpdateInstalledBinary() (bool, error) {
	f.calls++
	return f.view.Registered, nil
}
func (f *replacementServiceFake) UpdateInstalledBinaryChecked(ctx context.Context, checks service.ServiceReplacementChecks) (bool, error) {
	f.calls++
	if err := checks.BeforeStop(ctx); err != nil {
		return true, err
	}
	f.stops++
	if f.between != nil {
		f.between()
	}
	if err := checks.BeforeStage(ctx); err != nil {
		return true, err
	}
	f.stages++
	return true, nil
}
func TestServiceReplacement_ConsumesConfirmedServiceSnapshot(t *testing.T) {
	for _, scenario := range []string{"success", "definition changed", "copy changed", "during stop", "same file"} {
		t.Run(scenario, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "mihari")
			installed := filepath.Join(t.TempDir(), "service")
			if scenario == "same file" {
				installed = binary
			}
			for _, p := range []string{binary, installed} {
				if err := os.WriteFile(p, []byte("old"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			f := &replacementServiceFake{view: service.ServiceReplacementView{Registered: true, BinaryPath: installed, DefinitionSHA256: strings.Repeat("d", 64)}}
			c := NewSelfUpdateServiceCompletion(f, &fakeDaemonVersionClient{results: []statusResult{{status: protocol.Status{DaemonVersion: "v1.0.0"}}}})
			snapshot, err := c.ObserveReplacement(context.Background(), binary)
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Targets) == 0 {
				t.Fatal("missing real targets")
			}
			hash := sha256.Sum256([]byte("candidate"))
			digest := hex.EncodeToString(hash[:])
			preview, err := update.NewReplacementPreview(update.ReplacementCandidate{Version: "v1.0.0", SHA256: digest, Channel: "main"}, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			p := update.PreparedUpdate{Available: true, Version: "v1.0.0", SHA256: digest, Channel: "main", TargetPath: binary, Preview: preview, Consent: update.ReplacementConsent{Yes: true, ExpectedPreview: preview.ID}}
			if err := os.WriteFile(binary, []byte("candidate"), 0700); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "definition changed":
				f.view.DefinitionSHA256 = strings.Repeat("e", 64)
			case "copy changed":
				if err := os.WriteFile(installed, []byte("external"), 0700); err != nil {
					t.Fatal(err)
				}
			case "during stop":
				f.between = func() { f.view.DefinitionSHA256 = strings.Repeat("e", 64) }
			}
			err = c.AfterPreparedReplace(context.Background(), p)
			if scenario == "success" || scenario == "same file" {
				if err != nil || f.stages != 1 {
					t.Fatalf("stages=%d err=%v", f.stages, err)
				}
			} else if err == nil || f.stages != 0 {
				t.Fatalf("changed service staged: %d %v", f.stages, err)
			}
		})
	}
}

func TestServiceReplacement_ChangedDuringPreviewDoesNotClaimUpdated(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "mihari")
	if err := os.WriteFile(binary, []byte("unchanged"), 0700); err != nil {
		t.Fatal(err)
	}
	f := &replacementServiceFake{}
	observations := 0
	f.observe = func() {
		observations++
		if observations == 2 {
			f.view.DefinitionSHA256 = "changed"
		}
	}
	completion := NewSelfUpdateServiceCompletion(f, nil)
	_, err := completion.ObserveReplacement(context.Background(), binary)
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeInvalidState {
		t.Fatalf("unstable observation: %v", err)
	}
	if strings.Contains(strings.ToLower(api.Message), "mihari updated") {
		t.Fatalf("preview incorrectly claims a completed replacement: %s", api.Message)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "unchanged" || f.calls != 0 {
		t.Fatal("preview changed the installation")
	}
}
