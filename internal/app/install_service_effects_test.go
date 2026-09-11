package app

import (
	"context"
	"github.com/mihari-proxy/mihari/internal/service"
	"testing"
)

type definitionEffectAdapter struct {
	service.DefinitionAdapter
	files  map[string]service.DefinitionFile
	writes int
}

func (a *definitionEffectAdapter) ObserveAction(_ context.Context, d service.DefinitionAction) (string, error) {
	f, ok := a.files[d.Path]
	if !ok {
		return "absent", nil
	}
	return service.DefinitionFileState(f, ""), nil
}
func (a *definitionEffectAdapter) ReplayAction(_ context.Context, d service.DefinitionAction) error {
	a.writes++
	if d.NewState == "absent" {
		delete(a.files, d.Path)
		return nil
	}
	if d.File == nil {
		return unknownInstallState()
	}
	a.files[d.Path] = *d.File
	return nil
}
func TestInstallServiceEffects_ConcreteFileRecovery(t *testing.T) {
	ctx := context.Background()
	old := service.DefinitionFile{Path: "/etc/systemd/system/mihari.service.d/old.conf", Bytes: []byte("old definition"), Owner: 0, Mode: 0644}
	target := old
	target.Bytes = []byte("new definition")
	adapter := &definitionEffectAdapter{files: map[string]service.DefinitionFile{old.Path: old}}
	effects := &installServiceEffects{adapter: adapter, old: service.Definition{Files: []service.DefinitionFile{old}}, target: service.Definition{Files: []service.DefinitionFile{target}}}
	action, err := effects.prepare(ctx, service.DefinitionAction{Kind: service.DefinitionActionDropin, TargetRole: JournalRoleDropin, Path: target.Path, File: &target, OldState: "present", NewState: "present"})
	if err != nil || action.OldState != service.DefinitionFileState(old, "") || action.NewState != service.DefinitionFileState(target, "") || action.CandidateRef == "" {
		t.Fatalf("not bound to concrete backup and target: %+v err=%v", action, err)
	}
	if err := effects.apply(ctx, action); err != nil {
		t.Fatal(err)
	}
	if string(adapter.files[old.Path].Bytes) != "new definition" {
		t.Fatal("target definition not published")
	}
	if err := effects.apply(ctx, action); err != nil {
		t.Fatal(err)
	}
	if adapter.writes != 1 {
		t.Fatal("already committed effect replayed")
	}
	reverse := action
	reverse.Kind = JournalActionRestoreDropin
	reverse.OldState, reverse.NewState = action.NewState, action.OldState
	if err := effects.apply(ctx, reverse); err != nil {
		t.Fatal(err)
	}
	if string(adapter.files[old.Path].Bytes) != "old definition" {
		t.Fatal("backup definition not restored")
	}
	// Metadata is a strict allowlist; caller-controlled refs cannot grant a path.
	action.CandidateRef = "/etc/passwd"
	if err := effects.apply(ctx, action); err == nil {
		t.Fatal("accepted path not bound to private backup")
	}
}

type boundServiceFixture struct {
	state  string
	writes int
}

func (f *boundServiceFixture) Observe(context.Context, string) (string, error) { return f.state, nil }
func (f *boundServiceFixture) Apply(_ context.Context, a service.DefinitionAction) error {
	f.writes++
	f.state = a.NewState
	return nil
}
func TestInstallServiceEffects_ForwardHookConsumesBoundCandidate(t *testing.T) {
	h := newInstallHarness(t, InstallDataRetain)
	marker, err := h.tx.Store.CreateTransactionMarker(context.Background(), testTxnID)
	if err != nil {
		t.Fatal(err)
	}
	h.tx.journal, err = h.tx.buildJournal(h.req, testTxnID, marker, h.art)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.tx.Store.Save(context.Background(), h.tx.journal); err != nil {
		t.Fatal(err)
	}
	old := service.DefinitionFile{Path: "/etc/systemd/system/mihari.service", Bytes: []byte("old"), Mode: 0644}
	target := old
	target.Bytes = []byte("target")
	files := &boundServiceFixture{state: service.DefinitionFileState(old, "")}
	adapter := &definitionEffectAdapter{files: map[string]service.DefinitionFile{old.Path: old}}
	h.tx.serviceEffects = &installServiceEffects{files: files, adapter: adapter, old: service.Definition{Files: []service.DefinitionFile{old}}, target: service.Definition{Files: []service.DefinitionFile{target}}}
	freshCallback := 0
	err = h.tx.journaledHook(context.Background(), service.DefinitionAction{Path: target.Path, File: &target, Kind: JournalActionDefinition, TargetRole: JournalRoleDefinition}, func(context.Context) error { freshCallback++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if freshCallback != 0 || files.writes != 1 || files.state != service.DefinitionFileState(target, "") {
		t.Fatalf("bound candidate bypassed: fresh=%d bound=%d state=%s", freshCallback, files.writes, files.state)
	}
}
