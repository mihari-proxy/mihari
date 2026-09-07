package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestInstallJournal_FixtureRoundTrip(t *testing.T) {
	raw := readInstallFixture(t, "journal.json")
	got, err := DecodeJournal(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != InstallJournalSchema || got.TransactionID != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("identity: %+v", got)
	}
	if got.Operation != InstallOperationInstall || got.Phase != InstallPhasePrepared || got.Mode != InstallLayoutSystem {
		t.Fatalf("summary: %+v", got)
	}
	if got.SourcePath == "" || got.TargetPath == "" || got.InstallPath == "" || got.EndpointPath == "" || got.CredentialPath == "" || got.DataRoot == "" {
		t.Fatalf("paths: %+v", got)
	}
	if got.BootID == "" || !got.SourceIdentity.Present || got.TargetIdentity.Present {
		t.Fatalf("identities: %+v", got)
	}
	if got.DataAction != InstallDataCreate || got.RecoveryAuthority != InstallAuthoritySource {
		t.Fatalf("authority: %+v", got)
	}
	if !got.OldRunning || !got.OldEnabled {
		t.Fatal("old service state")
	}
	if got.ServiceBackup.Ref == "" || got.ServiceBackup.SHA256 != sha256Hex("original-unit-bytes\n") {
		t.Fatalf("service backup: %+v", got.ServiceBackup)
	}
	if got.TransactionMarker.SHA256 != sha256Hex(got.TransactionID) {
		t.Fatal("transaction marker hash")
	}
	if len(got.Actions) != 3 || got.Actions[0].Status != JournalActionDone || got.Actions[2].Kind != JournalActionRestoreStop || got.Actions[2].Status != JournalActionIntent {
		t.Fatalf("actions: %+v", got.Actions)
	}
	encoded, err := EncodeJournal(got)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`"env"`)) || bytes.Contains(encoded, []byte("LD_")) {
		t.Fatalf("journal encoded env: %s", encoded)
	}
	roundTrip, err := DecodeJournal(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip.TransactionID != got.TransactionID || len(roundTrip.Actions) != 3 || roundTrip.ServiceBackup != got.ServiceBackup {
		t.Fatalf("round-trip mismatch: %+v", roundTrip)
	}
}

func TestInstallJournal_SizeLimit(t *testing.T) {
	raw := readInstallFixture(t, "journal.json")
	if _, err := DecodeJournal(bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	boundary := append(append([]byte{}, raw...), bytes.Repeat([]byte(" "), MaxInstallJournalBytes-len(raw))...)
	if _, err := DecodeJournal(bytes.NewReader(boundary)); err != nil {
		t.Fatalf("1MiB journal rejected: %v", err)
	}
	reader := bytes.NewReader(append(boundary, bytes.Repeat([]byte(" "), 100)...))
	if _, err := DecodeJournal(reader); err == nil {
		t.Fatal("oversized journal accepted")
	}
	if reader.Len() != 99 {
		t.Fatalf("journal read was not bounded: %d remaining", reader.Len())
	}
}

func TestInstallJournal_RejectsMalformed(t *testing.T) {
	valid := string(readInstallFixture(t, "journal.json"))
	cases := []struct{ name, body string }{
		{"unknown field", `{"schema":"mihari.install-transaction/v1","env":{"PATH":"/evil"}}`},
		{"duplicate phase", injectDuplicatePhase(valid)},
		{"wrong schema", strings.Replace(valid, InstallJournalSchema, "mihari.install-transaction/v0", 1)},
		{"bad phase", strings.Replace(valid, `"prepared"`, `"committing"`, 1)},
		{"bad data_action", strings.Replace(valid, `"create"`, `"delete"`, 1)},
		{"bad authority", strings.Replace(valid, `"source"`, `"both"`, 1)},
		{"bad action status", strings.Replace(valid, `"intent"`, `"pending"`, 1)},
		{"bad action kind", strings.Replace(valid, `"restore_stop"`, `"shell"`, 1)},
		{"bad role", strings.Replace(valid, `"definition"`, `"arbitrary"`, 1)},
		{"short tx", strings.Replace(valid, `"0123456789abcdef0123456789abcdef"`, `"abc"`, 1)},
		{"trailing", valid + `{}`},
		{"array", `[]`},
		{"null", `null`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeJournal(strings.NewReader(test.body))
			if err == nil {
				t.Fatal("accepted")
			}
			if strings.Contains(err.Error(), "/evil") || strings.Contains(err.Error(), "PATH") {
				t.Fatal("env leaked into error")
			}
		})
	}
}

func TestInstallJournal_UnknownIdentityRejected(t *testing.T) {
	ctx := context.Background()
	journal := mustDecodeJournal(t)
	fs := newRecordingJournalFiles()
	store := &InstallJournalStore{files: fs}
	if _, err := store.Save(ctx, journal); err != nil {
		t.Fatal(err)
	}
	marker := fs.objects[transactionMarkerPath(journal.TransactionID)]
	if marker == nil {
		fs.objects[transactionMarkerPath(journal.TransactionID)] = []byte(journal.TransactionID)
		fs.meta[transactionMarkerPath(journal.TransactionID)] = journal.TransactionMarker
	}
	wrong := journal.TransactionMarker
	wrong.SHA256 = strings.Repeat("f", 64)
	wrong.Marker = "deadbeefdeadbeefdeadbeefdeadbeef"
	fs.meta[transactionMarkerPath(journal.TransactionID)] = wrong
	if _, err := store.Load(ctx); err == nil {
		t.Fatal("loaded journal with unknown marker identity")
	}
}

func TestInstallJournal_RecordsIntentDoneAndReverseRecovery(t *testing.T) {
	ctx := context.Background()
	journal := mustDecodeJournal(t)
	journal.Actions = []JournalAction{}
	fs := newRecordingJournalFiles()
	seedJournal(t, fs, journal)
	store := &InstallJournalStore{files: fs}
	intent := JournalAction{Kind: JournalActionMask, TargetRole: JournalRoleDefinition, OldState: "unmasked", NewState: "masked", BackupRef: journal.ServiceBackup.Ref, Status: JournalActionIntent}
	if _, err := store.RecordAction(ctx, &journal, intent); err != nil {
		t.Fatal(err)
	}
	if len(journal.Actions) != 1 || journal.Actions[0].Seq != 1 || journal.Actions[0].Status != JournalActionIntent {
		t.Fatalf("intent: %+v", journal.Actions)
	}
	done := journal.Actions[0]
	done.Status = JournalActionDone
	if _, err := store.RecordAction(ctx, &journal, done); err != nil {
		t.Fatal(err)
	}
	if len(journal.Actions) != 1 || journal.Actions[0].Status != JournalActionDone {
		t.Fatalf("done replaced intent: %+v", journal.Actions)
	}
	restore := JournalAction{Kind: JournalActionRestoreMask, TargetRole: JournalRoleDefinition, OldState: "masked", NewState: "unmasked", Status: JournalActionIntent}
	if _, err := store.RecordAction(ctx, &journal, restore); err != nil {
		t.Fatal(err)
	}
	if len(journal.Actions) != 2 || journal.Actions[1].Kind != JournalActionRestoreMask || journal.Actions[1].Seq != 2 {
		t.Fatalf("reverse recovery missing: %+v", journal.Actions)
	}
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Actions) != 2 || loaded.Actions[1].Kind != JournalActionRestoreMask {
		t.Fatalf("persisted reverse action: %+v", loaded.Actions)
	}
}

func TestInstallJournal_PhaseDoesNotReplaceActions(t *testing.T) {
	journal := mustDecodeJournal(t)
	journal.Phase = InstallPhaseComplete
	pending := IncompleteActions(journal)
	if len(pending) != 1 || pending[0].Kind != JournalActionRestoreStop || pending[0].Status != JournalActionIntent {
		t.Fatalf("phase hid actions: %+v", pending)
	}
	if progress, err := ClassifyObservedAction(pending[0], pending[0].OldState); err != nil || progress != ActionNotStarted {
		t.Fatalf("intent old-state: %v %v", progress, err)
	}
	if progress, err := ClassifyObservedAction(pending[0], pending[0].NewState); err != nil || progress != ActionStarted {
		t.Fatalf("intent new-state should be started: %v %v", progress, err)
	}
}

func TestInstallJournal_DurabilitySyscallFailures(t *testing.T) {
	ctx := context.Background()
	base := mustDecodeJournal(t)
	base.Actions = []JournalAction{}
	for _, step := range []string{"create-excl", "fsync", "rename", "parent-sync"} {
		t.Run(step, func(t *testing.T) {
			fs := newRecordingJournalFiles()
			journal := base
			seedJournal(t, fs, journal)
			store := &InstallJournalStore{files: fs}
			fs.failAt = step
			before := append([]JournalAction(nil), journal.Actions...)
			action := JournalAction{Kind: JournalActionStop, TargetRole: JournalRoleDefinition, OldState: "running", NewState: "stopped", Status: JournalActionIntent}
			dur, err := store.RecordAction(ctx, &journal, action)
			if err == nil {
				t.Fatal("expected durability failure")
			}
			switch step {
			case "create-excl", "fsync", "rename":
				if dur.Published || dur.Durable {
					t.Fatalf("unpublished %s marked started: %+v", step, dur)
				}
				if len(journal.Actions) != len(before) {
					t.Fatal("action started without published intent")
				}
				loaded, loadErr := store.Load(ctx)
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				if len(loaded.Actions) != 0 {
					t.Fatalf("disk started unpublished action: %+v", loaded.Actions)
				}
			case "parent-sync":
				if !dur.Published || dur.Durable {
					t.Fatalf("rename/parent-sync not distinguished: %+v", dur)
				}
				if len(journal.Actions) != 1 {
					t.Fatal("published intent not visible to caller")
				}
				loaded, loadErr := store.Load(ctx)
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				if len(loaded.Actions) != 1 || loaded.Actions[0].Status != JournalActionIntent {
					t.Fatalf("visible journal missing started action: %+v", loaded.Actions)
				}
			}
			if len(fs.steps) == 0 || fs.steps[0] != "create-excl" {
				t.Fatalf("missing O_EXCL: %v", fs.steps)
			}
			if step != "create-excl" && !containsStep(fs.steps, "fsync") {
				t.Fatalf("missing fsync: %v", fs.steps)
			}
			if (step == "rename" || step == "parent-sync") && !containsStep(fs.steps, "rename") {
				t.Fatalf("missing rename: %v", fs.steps)
			}
			if step == "parent-sync" && !containsStep(fs.steps, "parent-sync") {
				t.Fatalf("missing parent fsync: %v", fs.steps)
			}
		})
	}
}

func TestInstallJournal_CrossBootResolvesWithoutMountID(t *testing.T) {
	recorded := mustDecodeJournal(t).InstallIdentity
	actual := recorded
	actual.BootID = "22222222-2222-2222-2222-222222222222"
	actual.Dev = "99"
	actual.Ino = "1"
	actual.MountID = "7"
	actual.Identity = "99:1"
	if err := MatchJournalObject(recorded, actual); err != nil {
		t.Fatalf("cross-boot required mount/dev equality: %v", err)
	}
	same := recorded
	same.MountID = "7"
	if err := MatchJournalObject(recorded, same); err == nil {
		t.Fatal("same-boot mount mismatch accepted")
	}
	wrongHash := actual
	wrongHash.SHA256 = strings.Repeat("0", 64)
	if err := MatchJournalObject(recorded, wrongHash); err == nil {
		t.Fatal("cross-boot hash mismatch accepted")
	}
	wrongMarker := actual
	wrongMarker.Marker = "ffffffffffffffffffffffffffffffff"
	if err := MatchJournalObject(recorded, wrongMarker); err == nil {
		t.Fatal("cross-boot marker mismatch accepted")
	}
	ctx := context.Background()
	fs := newRecordingJournalFiles()
	journal := mustDecodeJournal(t)
	seedJournal(t, fs, journal)
	fs.boot = actual.BootID
	fs.meta["install"] = actual
	store := &InstallJournalStore{files: fs}
	got, err := store.ResolveObject(ctx, "install", recorded)
	if err != nil {
		t.Fatal(err)
	}
	if got.SHA256 != recorded.SHA256 || got.MountID == recorded.MountID {
		t.Fatalf("resolved object: %+v", got)
	}
}

func TestInstallJournal_UnknownActualStateStopsRecovery(t *testing.T) {
	action := JournalAction{Kind: JournalActionStop, OldState: "running", NewState: "stopped", Status: JournalActionIntent}
	if _, err := ClassifyObservedAction(action, "unknown-cgroup"); err == nil {
		t.Fatal("unknown actual state allowed recovery")
	}
	var api protocol.APIError
	_, err := ClassifyObservedAction(action, "foreign-pid")
	if !errors.As(err, &api) || api.Code != protocol.CodeInvalidState {
		t.Fatalf("want invalid_state, got %v", err)
	}
}

func TestInstallJournal_RecordsServiceBytesAndAuthorities(t *testing.T) {
	journal := mustDecodeJournal(t)
	if journal.ServiceBackup.SHA256 != sha256Hex("original-unit-bytes\n") {
		t.Fatal("original service bytes were not hashed into the journal")
	}
	if journal.DataAction != InstallDataCreate || journal.RecoveryAuthority != InstallAuthoritySource {
		t.Fatal("data_action or recovery authority missing")
	}
	if journal.TransactionID == "" || journal.CandidateHash == "" || journal.SourceIdentity.Identity == "" {
		t.Fatal("object identity/hash/transaction missing")
	}
	encoded, err := EncodeJournal(journal)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("original-unit-bytes")) {
		t.Fatal("raw service bytes embedded in journal")
	}
	if bytes.Contains(encoded, []byte("LD_LIBRARY_PATH")) || bytes.Contains(encoded, []byte("\"env\"")) {
		t.Fatal("env encoded into journal")
	}
}

func TestInstallJournal_JSONKeysStable(t *testing.T) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(readInstallFixture(t, "journal.json"), &raw); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"schema", "transaction_id", "operation", "phase", "mode",
		"source_path", "target_path", "install_path", "endpoint_path", "credential_path", "data_root",
		"boot_id", "source_identity", "target_identity", "install_identity", "endpoint_identity", "credential_identity",
		"candidate_hash", "backup_hash", "data_action", "service_backup",
		"old_running", "old_enabled", "recovery_authority", "created_at", "transaction_marker", "actions",
	}
	if len(raw) != len(want) {
		t.Fatalf("fixture fields %d want %d", len(raw), len(want))
	}
	for _, key := range want {
		if _, ok := raw[key]; !ok {
			t.Fatalf("missing journal field %s", key)
		}
	}
}

type recordingJournalFiles struct {
	objects map[string][]byte
	meta    map[string]JournalObject
	boot    string
	steps   []string
	failAt  string
	seq     int
}

func newRecordingJournalFiles() *recordingJournalFiles {
	return &recordingJournalFiles{
		objects: map[string][]byte{},
		meta:    map[string]JournalObject{},
		boot:    "11111111-1111-1111-1111-111111111111",
	}
}

func (f *recordingJournalFiles) inspect(_ context.Context, name string) (JournalObject, error) {
	obj, ok := f.meta[name]
	if !ok {
		return JournalObject{}, nil
	}
	return obj, nil
}

func (f *recordingJournalFiles) read(_ context.Context, name string, limit int64) ([]byte, error) {
	raw, ok := f.objects[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	if int64(len(raw)) > limit {
		return nil, io.ErrUnexpectedEOF
	}
	return append([]byte(nil), raw...), nil
}

func (f *recordingJournalFiles) write(_ context.Context, name string, data []byte, _ JournalObject) (JournalDurability, error) {
	fail := func() error { return errors.New("simulated durability failure") }
	f.steps = append(f.steps, "create-excl")
	if f.failAt == "create-excl" {
		return JournalDurability{}, fail()
	}
	f.steps = append(f.steps, "fsync")
	if f.failAt == "fsync" {
		return JournalDurability{}, fail()
	}
	f.steps = append(f.steps, "rename")
	if f.failAt == "rename" {
		return JournalDurability{}, fail()
	}
	f.seq++
	obj := JournalObject{
		Present:  true,
		SHA256:   sha256HexBytes(data),
		Dev:      "8",
		Ino:      itoa(1000 + f.seq),
		MountID:  "42",
		BootID:   f.boot,
		Marker:   "0123456789abcdef0123456789abcdef",
		Identity: "8:" + itoa(1000+f.seq),
	}
	f.objects[name] = append([]byte(nil), data...)
	f.meta[name] = obj
	f.steps = append(f.steps, "parent-sync")
	if f.failAt == "parent-sync" {
		return JournalDurability{Published: true, Durable: false, Object: obj}, fail()
	}
	return JournalDurability{Published: true, Durable: true, Object: obj}, nil
}

func seedJournal(t *testing.T, fs *recordingJournalFiles, journal InstallJournal) {
	t.Helper()
	if journal.Actions == nil {
		journal.Actions = []JournalAction{}
	}
	raw, err := EncodeJournal(journal)
	if err != nil {
		t.Fatal(err)
	}
	fs.objects[installJournalFileName] = raw
	fs.meta[installJournalFileName] = JournalObject{Present: true, SHA256: sha256HexBytes(raw), Dev: "8", Ino: "9", MountID: "42", BootID: fs.boot, Marker: journal.TransactionID, Identity: "8:9"}
	fs.objects[transactionMarkerPath(journal.TransactionID)] = []byte(journal.TransactionID)
	fs.meta[transactionMarkerPath(journal.TransactionID)] = journal.TransactionMarker
}

func mustDecodeJournal(t *testing.T) InstallJournal {
	t.Helper()
	got, err := DecodeJournal(bytes.NewReader(readInstallFixture(t, "journal.json")))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func containsStep(steps []string, name string) bool {
	for _, step := range steps {
		if step == name {
			return true
		}
	}
	return false
}

func injectDuplicatePhase(valid string) string {
	return strings.Replace(valid, `"phase": "prepared"`, `"phase": "prepared", "phase": "complete"`, 1)
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
