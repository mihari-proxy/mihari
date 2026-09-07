package app

import (
	"context"
	"errors"
	"github.com/mihari-proxy/mihari/internal/service"
	"reflect"
	"testing"
)

type binaryTargetSpy struct {
	events               *[]string
	renamed              bool
	publishErr, closeErr error
}

func (s *binaryTargetSpy) Stage(context.Context, InstallRequest) error {
	*s.events = append(*s.events, "verify-candidate")
	return nil
}
func (s *binaryTargetSpy) Publish(context.Context) (bool, error) {
	*s.events = append(*s.events, "rename-sync")
	return s.renamed, s.publishErr
}
func (s *binaryTargetSpy) Close() error { *s.events = append(*s.events, "close"); return s.closeErr }
func TestBinaryOnly_OnlySiblingLeaseAndVerifiedRename(t *testing.T) {
	for _, tc := range []struct {
		name       string
		renamed    bool
		pub, close error
	}{
		{"success", true, nil, nil}, {"postrename-sync", true, errors.New("sync"), nil}, {"postrename-cleanup", true, nil, errors.New("cleanup")}, {"prerename", false, errors.New("rename"), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var events []string
			result, err := applyBinaryOnly(context.Background(), InstallRequest{Operation: "update", Channel: "main"}, "main", func(context.Context) (binaryUpdateTarget, error) {
				events = append(events, "binary-parent-lease")
				return &binaryTargetSpy{events: &events, renamed: tc.renamed, publishErr: tc.pub, closeErr: tc.close}, nil
			})
			if result.Changed != tc.renamed || result.ServiceStatus != "not_installed" {
				t.Fatalf("rename authority lost: %+v err=%v", result, err)
			}
			if (err != nil) != (tc.pub != nil || tc.close != nil) {
				t.Fatalf("failure lost: %v", err)
			}
			if !reflect.DeepEqual(events, []string{"binary-parent-lease", "verify-candidate", "rename-sync", "close"}) {
				t.Fatalf("wrong binary-only effects: %v", events)
			}
		})
	}
}
func TestBinaryOnly_RejectsInstallationFieldsBeforeAnyFileAccess(t *testing.T) {
	for _, change := range []func(*InstallRequest){func(r *InstallRequest) { r.Bundle = "/bundle" }, func(r *InstallRequest) { r.Source = "/source" }, func(r *InstallRequest) { r.Data = "/data" }, func(r *InstallRequest) { r.PathBinary = "/path" }, func(r *InstallRequest) { r.Channel = "dev" }} {
		req := InstallRequest{Operation: "update", Channel: "main"}
		change(&req)
		opened := false
		_, err := applyBinaryOnly(context.Background(), req, "main", func(context.Context) (binaryUpdateTarget, error) { opened = true; return &binaryTargetSpy{}, nil })
		if err == nil || opened {
			t.Fatalf("installation input accessed binary or B: opened=%v err=%v", opened, err)
		}
	}
}

func TestInstallEntry_NoServiceInspectsBeforeBaseAccess(t *testing.T) {
	events := []string{}
	result, err := dispatchInstall(context.Background(), InstallRequest{Operation: "update", Channel: "main"}, "main", func(context.Context) (service.Definition, error) {
		events = append(events, "inspect-service")
		return service.Definition{Status: service.StatusNotInstalled}, nil
	}, func(context.Context, InstallRequest, service.Definition) (InstallResult, error) {
		events = append(events, "open-base")
		return InstallResult{}, nil
	}, func(context.Context) (binaryUpdateTarget, error) {
		events = append(events, "binary-parent-lease")
		return &binaryTargetSpy{events: &events, renamed: true}, nil
	})
	if err != nil || !result.Changed || !reflect.DeepEqual(events, []string{"inspect-service", "binary-parent-lease", "verify-candidate", "rename-sync", "close"}) {
		t.Fatalf("no-service update accessed installation state: %v result=%+v err=%v", events, result, err)
	}
}

type definitionCapture struct {
	service.DefinitionAdapter
	published service.Definition
}

func (c *definitionCapture) WriteDefinition(ctx context.Context, def service.Definition) error {
	c.published = def
	return c.DefinitionAdapter.WriteDefinition(ctx, def)
}
func TestInstallEntry_PreservesFullDefinitionAndShellStartPolicy(t *testing.T) {
	for _, shell := range []bool{false, true} {
		h := newInstallHarness(t, InstallDataRetain)
		h.req.Operation = InstallOperationInstall
		h.tx.StartAfterInstall = shell
		capture := &definitionCapture{DefinitionAdapter: h.service}
		h.tx.Service = capture
		target := service.Definition{Binary: "/usr/local/lib/mihari/mihari", Args: []string{"daemon"}, Files: []service.DefinitionFile{{Path: "/etc/systemd/system/mihari.service", Bytes: []byte("real target unit"), Mode: 0644}}}
		h.tx.preparedAuthority = &installPreparedAuthority{TargetDefinition: target,
			Source:        stampedObject(h.art.SourceBytes, "9160", h.art.BootID, testTxnID),
			ServiceBackup: ServiceBackup{Ref: "transactions/" + testTxnID + "/unit", SHA256: sha256Hex("private-backup"), Identity: "12:9161"}}
		result, err := h.tx.Apply(context.Background(), h.req)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(capture.published.Files, target.Files) || capture.published.Binary != target.Binary {
			t.Fatalf("actual service definition was discarded: %+v", capture.published)
		}
		if capture.published.Running != shell || (result.ServiceStatus == InstallServiceRunning) != shell {
			t.Fatalf("install start policy: shell=%v result=%+v definition=%+v", shell, result, capture.published)
		}
	}
}

func TestInstallEntry_UsesObservedJournalAuthority(t *testing.T) {
	h := newInstallHarness(t, InstallDataRetain)
	a := &installPreparedAuthority{
		Source:        stampedObject([]byte("observed-source"), "9127", h.art.BootID, testTxnID),
		Target:        stampedObject([]byte("observed-target"), "9128", h.art.BootID, testTxnID),
		Install:       stampedObject([]byte("observed-install"), "9129", h.art.BootID, testTxnID),
		ServiceBackup: ServiceBackup{Ref: "transactions/" + testTxnID + "/unit", SHA256: sha256Hex("private-unit-backup"), Identity: "12:9130"},
	}
	h.tx.preparedAuthority = a
	if _, err := h.tx.Apply(context.Background(), h.req); err != nil {
		t.Fatal(err)
	}
	j := h.loadedJournal(t, h.disk)
	if j.SourceIdentity != a.Source || j.TargetIdentity != a.Target || j.InstallIdentity != a.Install || j.ServiceBackup != a.ServiceBackup || j.BackupHash != a.ServiceBackup.SHA256 {
		t.Fatalf("journal replaced actual authority with synthesized observations: %+v", j)
	}
}

func TestInstallEntry_ReverseTracksConcreteObject(t *testing.T) {
	x := InstallTransaction{journal: InstallJournal{Actions: []JournalAction{{Seq: 4, Kind: JournalActionRestoreDropin, TargetRole: JournalRoleDropin, BackupRef: "unit/0", CandidateRef: "target/0"}}}}
	first := JournalAction{Seq: 1, Kind: JournalActionDropin, TargetRole: JournalRoleDropin, BackupRef: "unit/0", CandidateRef: "target/0"}
	second := JournalAction{Seq: 2, Kind: JournalActionDropin, TargetRole: JournalRoleDropin, BackupRef: "unit/1", CandidateRef: "target/1"}
	if !x.alreadyReversed(first) || x.alreadyReversed(second) {
		t.Fatal("reversing one drop-in suppressed recovery of another drop-in")
	}
}
