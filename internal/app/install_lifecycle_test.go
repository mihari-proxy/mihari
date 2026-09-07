package app

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/service"
	"testing"
)

type lifecycleAdapter struct {
	*definitionEffectAdapter
	hook          service.ActionHook
	running       bool
	starts, stops int
}

func (a *lifecycleAdapter) InspectDefinition(context.Context) (service.Definition, error) {
	status := service.StatusStopped
	if a.running {
		status = service.StatusRunning
	}
	return service.Definition{Status: status, Running: a.running}, nil
}
func (a *lifecycleAdapter) DisableAutostartAndStop(ctx context.Context) error {
	return a.hook(ctx, service.DefinitionAction{Kind: service.DefinitionActionStop, TargetRole: JournalRoleDefinition, NewState: "stopped"}, func(context.Context) error { a.running = false; a.stops++; return nil })
}
func (a *lifecycleAdapter) WaitOwnedTreeExit(context.Context) error { return nil }
func (a *lifecycleAdapter) WriteDefinition(ctx context.Context, d service.Definition) error {
	for _, file := range d.Files {
		f := file
		if err := a.hook(ctx, service.DefinitionAction{Kind: service.DefinitionActionDefinition, Path: f.Path, File: &f, TargetRole: JournalRoleDefinition}, func(context.Context) error { a.files[f.Path] = f; return nil }); err != nil {
			return err
		}
	}
	return nil
}
func (a *lifecycleAdapter) RestoreDefinition(ctx context.Context, d service.Definition) error {
	return a.WriteDefinition(ctx, d)
}
func (a *lifecycleAdapter) Start(ctx context.Context) error {
	return a.hook(ctx, service.DefinitionAction{Kind: service.DefinitionActionStart, TargetRole: JournalRoleDefinition, NewState: "running"}, func(context.Context) error { a.running = true; a.starts++; return nil })
}
func (a *lifecycleAdapter) ObserveAction(ctx context.Context, d service.DefinitionAction) (string, error) {
	if d.Path != "" {
		return a.definitionEffectAdapter.ObserveAction(ctx, d)
	}
	if a.running {
		return "running", nil
	}
	return "stopped", nil
}
func TestInstallLifecycle_JournaledServiceEffectsPreserveData(t *testing.T) {
	for _, op := range []string{"stop", "restart", "uninstall"} {
		t.Run(op, func(t *testing.T) {
			h := newInstallHarness(t, InstallDataRetain)
			marker, err := h.tx.Store.CreateTransactionMarker(context.Background(), testTxnID)
			if err != nil {
				t.Fatal(err)
			}
			h.tx.journal, err = h.tx.buildJournal(h.req, testTxnID, marker, h.art)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = h.tx.Store.Save(context.Background(), h.tx.journal); err != nil {
				t.Fatal(err)
			}
			file := service.DefinitionFile{Path: "/etc/systemd/system/mihari.service", Bytes: []byte("original unit"), Mode: 0644}
			adapter := &lifecycleAdapter{definitionEffectAdapter: &definitionEffectAdapter{files: map[string]service.DefinitionFile{file.Path: file}}, running: true}
			old := service.Definition{Files: []service.DefinitionFile{file}, Status: service.StatusRunning, Running: true, Enabled: true}
			target := old
			target.Running = op == "restart"
			if op == "uninstall" {
				target = service.Definition{Status: service.StatusNotInstalled}
			}
			h.tx.Service = adapter
			adapter.hook = h.tx.journaledHook
			h.tx.serviceEffects = &installServiceEffects{adapter: adapter, old: old, target: target}
			h.tx.preparedAuthority = &installPreparedAuthority{OldDefinition: old, TargetDefinition: target}
			if err := h.tx.lifecycleActions(context.Background(), op); err != nil {
				t.Fatal(err)
			}
			if adapter.stops != 1 || adapter.running != (op == "restart") {
				t.Fatalf("operation %s did not produce requested process state: stops=%d running=%v", op, adapter.stops, adapter.running)
			}
			_, present := adapter.files[file.Path]
			if present == (op == "uninstall") {
				t.Fatalf("definition present=%v after %s", present, op)
			}
			journal := h.loadedJournal(t, h.disk)
			if journal.Phase != InstallPhaseComplete || journal.RecoveryAuthority != InstallAuthorityTarget {
				t.Fatalf("incomplete lifecycle: %+v", journal)
			}
			activated := false
			for _, a := range journal.Actions {
				if a.Kind == JournalActionActivation {
					activated = true
				}
				if a.Kind == JournalActionDefinition && !activated {
					t.Fatal("lifecycle published definition before activation")
				}
				if a.TargetRole == JournalRoleData || a.TargetRole == JournalRoleManagedBinary || a.TargetRole == JournalRoleChannel {
					t.Fatalf("lifecycle mutated business artifacts: %+v", a)
				}
			}
		})
	}
}
