package app

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/service"
)

type barrierStore struct {
	files       map[string]service.DefinitionFile
	links       map[string]string
	beforeWrite func()
}

func (s *barrierStore) Read(_ context.Context, name string) (service.DefinitionFile, error) {
	if link, ok := s.links[name]; ok {
		kind := "link"
		if link == "/dev/null" {
			kind = "mask"
		}
		return service.DefinitionFile{Path: name, Kind: kind, Mode: 0777}, nil
	}
	f, ok := s.files[name]
	if !ok {
		return f, os.ErrNotExist
	}
	return f, nil
}
func (s *barrierStore) Write(_ context.Context, f service.DefinitionFile) error {
	if s.beforeWrite != nil {
		s.beforeWrite()
	}
	delete(s.links, f.Path)
	s.files[f.Path] = f
	return nil
}
func (s *barrierStore) Mask(_ context.Context, p, target string) error {
	delete(s.files, p)
	s.links[p] = target
	return nil
}
func (s *barrierStore) Remove(_ context.Context, p string) error {
	delete(s.files, p)
	delete(s.links, p)
	return nil
}
func (s *barrierStore) ReadLink(_ context.Context, p string) (string, error) {
	v, ok := s.links[p]
	if !ok {
		return "", os.ErrNotExist
	}
	return v, nil
}
func (*barrierStore) List(context.Context, string) ([]string, error) { return nil, nil }

type barrierRunner struct{ store *barrierStore }

func (r barrierRunner) Run(_ context.Context, args []string) (service.CommandResult, error) {
	if len(args) > 2 && args[2] == "show" {
		load, fragment, state := "loaded", service.DefaultSystemdPaths().UnitFile, "disabled"
		if r.store.links[fragment] == "/dev/null" {
			load, fragment, state = "masked", "/dev/null", "masked"
		}
		return service.CommandResult{Stdout: []byte("LoadState=" + load + "\nActiveState=inactive\nSubState=dead\nMainPID=0\nControlGroup=\nFragmentPath=" + fragment + "\nDropInPaths=\nUnitFileState=" + state + "\n")}, nil
	}
	return service.CommandResult{}, nil
}

type barrierValidation struct {
	ValidationChild
	check func()
}

// restartWaitRunner models the legacy service repeatedly exiting before the
// installer replaces its definition. Masking still stops its restart job.
type restartWaitRunner struct{ barrierRunner }

func (r restartWaitRunner) Run(ctx context.Context, args []string) (service.CommandResult, error) {
	result, err := r.barrierRunner.Run(ctx, args)
	unit := r.store.files[service.DefaultSystemdPaths().UnitFile]
	if err == nil && strings.Contains(string(result.Stdout), "LoadState=loaded") && !strings.Contains(string(unit.Bytes), "Restart=on-failure") {
		result.Stdout = []byte(strings.NewReplacer("ActiveState=inactive", "ActiveState=activating", "SubState=dead", "SubState=auto-restart").Replace(string(result.Stdout)))
	}
	return result, err
}

func (v barrierValidation) ReapValidation(ctx context.Context, id ProcessStartIdentity, j InstallJournal) error {
	return v.ValidationChild.(*FakeValidationChild).ReapValidation(ctx, id, j)
}
func (v barrierValidation) Start(ctx context.Context, s ValidationStart) (ValidationSession, error) {
	v.check()
	return v.ValidationChild.Start(ctx, s)
}

func TestInstallActivation_RealAdapterKeepsMaskUntilAuthority(t *testing.T) {
	for _, scenario := range []struct {
		name                string
		failed, restartWait bool
	}{
		{"activation", false, false},
		{"source recovery", true, false},
		{"auto-restart activation", false, true},
		{"auto-restart source recovery", true, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			failed := scenario.failed
			h := newInstallHarness(t, InstallDataRetain)
			h.art.OldRunning = false
			h.art.OldEnabled = false
			h.art.TargetRunning = false
			h.art.TargetEnabled = false
			h.tx.Artifacts = h.art
			path := service.DefaultSystemdPaths().UnitFile
			old := service.DefinitionFile{Path: path, Bytes: []byte("[Service]\nExecStart=/usr/local/lib/mihari/mihari daemon\n"), Mode: 0644, Kind: "unit"}
			target := old
			target.Bytes = append(append([]byte{}, old.Bytes...), []byte("Restart=on-failure\n")...)
			store := &barrierStore{files: map[string]service.DefinitionFile{path: old}, links: map[string]string{}}
			var runner service.CommandRunner = barrierRunner{store}
			if scenario.restartWait {
				runner = restartWaitRunner{barrierRunner{store}}
			}
			adapter := service.NewSystemdAdapterWithConfig(service.SystemdConfig{Runner: runner, Files: store, Hook: h.tx.journaledHook})
			h.tx.Service = adapter
			oldDef := service.Definition{Status: service.StatusStopped, Binary: "/usr/local/lib/mihari/mihari", Files: []service.DefinitionFile{old}}
			targetDef := oldDef
			targetDef.Files = []service.DefinitionFile{target}
			h.tx.preparedAuthority = &installPreparedAuthority{OldDefinition: oldDef, TargetDefinition: targetDef, Source: stampedObject([]byte("source"), "1", testBootID, testTxnID), Target: stampedObject([]byte("target"), "2", testBootID, testTxnID), Install: stampedObject([]byte("install"), "3", testBootID, testTxnID), ServiceBackup: ServiceBackup{Ref: "transactions/" + testTxnID + "/unit", SHA256: sha256Hex("private"), Identity: "12:4"}}
			h.tx.serviceEffects = &installServiceEffects{adapter: adapter, old: oldDef, target: targetDef}
			validation := h.tx.Validation.(*FakeValidationChild)
			if failed {
				validation.Result = ValidationFailed
			}
			checked := false
			h.tx.Validation = barrierValidation{ValidationChild: validation, check: func() {
				checked = true
				if store.links[path] != "/dev/null" {
					t.Error("actual unit mask removed before validation")
				}
				if h.tx.journal.Phase != InstallPhaseDefinitionCommitted || h.tx.journal.RecoveryAuthority != InstallAuthoritySource {
					t.Error("invalid validation authority")
				}
			}}
			store.beforeWrite = func() {
				if !failed && h.tx.journal.RecoveryAuthority != InstallAuthorityTarget {
					t.Error("actual definition published before durable activation")
				}
			}
			_, err := h.tx.Apply(context.Background(), h.req)
			if !checked {
				t.Fatal("validation not reached", err)
			}
			if failed {
				if err == nil {
					t.Fatal("validation failure accepted")
				}
				store.beforeWrite = nil
				h.lease.held = true
				if err := h.tx.RecoverLocked(context.Background(), h.lease); err != nil {
					t.Fatal(err)
				}
				if !strings.EqualFold(string(store.files[path].Bytes), string(old.Bytes)) {
					t.Fatal("source definition not restored")
				}
			} else if err != nil {
				t.Fatal(err)
			} else if string(store.files[path].Bytes) != string(target.Bytes) {
				t.Fatal("target definition not published")
			}
			if errors.Is(err, errInstallCrash) {
				t.Fatal(err)
			}
		})
	}
}
