package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/service"
)

const (
	testTxnID     = "0123456789abcdef0123456789abcdef"
	testBootID    = "11111111-1111-1111-1111-111111111111"
	roleSource    = "source"
	roleData      = "data"
	roleManaged   = "managed_binary"
	rolePath      = "path_binary"
	roleChannel   = "channel"
	roleLive      = "definition"
	roleCandidate = "definition.candidate"
	roleIsolated  = "data.isolated"
	roleBackup    = "backup"
)

type memoryFile struct {
	data []byte
	obj  JournalObject
}

type memoryService struct {
	installed bool
	masked    bool
	enabled   bool
	running   bool
}

type memoryInstallDisk struct {
	mu          sync.Mutex
	files       map[string]memoryFile
	svc         memoryService
	authority   string
	validating  string
	reloads     int
	seq         int
	boot        string
	dataAction  string
	sourceBytes []byte
}

func newMemoryInstallDisk() *memoryInstallDisk {
	return &memoryInstallDisk{
		files:      map[string]memoryFile{},
		authority:  InstallAuthoritySource,
		validating: "idle",
		boot:       testBootID,
		dataAction: InstallDataCreate,
	}
}

func (d *memoryInstallDisk) clone() *memoryInstallDisk {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := &memoryInstallDisk{
		files:       map[string]memoryFile{},
		svc:         d.svc,
		authority:   d.authority,
		validating:  d.validating,
		reloads:     d.reloads,
		seq:         d.seq,
		boot:        d.boot,
		dataAction:  d.dataAction,
		sourceBytes: append([]byte(nil), d.sourceBytes...),
	}
	for name, file := range d.files {
		out.files[name] = memoryFile{data: append([]byte(nil), file.data...), obj: file.obj}
	}
	return out
}

func (d *memoryInstallDisk) snapshot() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	type row struct {
		Name        string
		SHA         string
		Masked      bool
		Enabled     bool
		Running     bool
		Authority   string
		Validating  string
		Reloads     int
		Source      bool
		Data        string
		Isolated    bool
		Phase       string
		Actions     []JournalAction
		Txn         string
		JournalAuth string
	}
	s := row{
		Masked:     d.svc.masked,
		Enabled:    d.svc.enabled,
		Running:    d.svc.running,
		Authority:  d.authority,
		Validating: d.validating,
		Reloads:    d.reloads,
		Source:     len(d.objectLocked(roleSource)) > 0,
		Data:       shaOf(d.objectLocked(roleData)),
		Isolated:   len(d.objectLocked(roleIsolated)) > 0,
	}
	if raw, ok := d.files[installJournalFileName]; ok {
		var journal InstallJournal
		if err := json.Unmarshal(raw.data, &journal); err == nil {
			s.Phase = journal.Phase
			s.Actions = journal.Actions
			s.Txn = journal.TransactionID
			s.JournalAuth = journal.RecoveryAuthority
		}
	}
	body, _ := json.Marshal(s)
	return string(body)
}

func (d *memoryInstallDisk) put(role string, data []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.putLocked(role, data)
}

func (d *memoryInstallDisk) putLocked(role string, data []byte) {
	d.seq++
	obj := JournalObject{
		Present:  true,
		SHA256:   shaOf(data),
		Dev:      "8",
		Ino:      itoa(1000 + d.seq),
		MountID:  "42",
		BootID:   d.boot,
		Marker:   testTxnID,
		Identity: "8:" + itoa(1000+d.seq),
	}
	d.files[role] = memoryFile{data: append([]byte(nil), data...), obj: obj}
}

func (d *memoryInstallDisk) deleteLocked(role string) {
	delete(d.files, role)
}

func (d *memoryInstallDisk) objectLocked(role string) []byte {
	file, ok := d.files[role]
	if !ok {
		return nil
	}
	return append([]byte(nil), file.data...)
}

func (d *memoryInstallDisk) Object(role string) []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.objectLocked(role)
}

func (d *memoryInstallDisk) SourcePresent() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.objectLocked(roleSource)) > 0 && bytes.Equal(d.objectLocked(roleSource), d.sourceBytes)
}

func (d *memoryInstallDisk) inspect(_ context.Context, name string) (JournalObject, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	file, ok := d.files[name]
	if !ok {
		return JournalObject{}, nil
	}
	return file.obj, nil
}

func (d *memoryInstallDisk) read(_ context.Context, name string, limit int64) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	file, ok := d.files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	if int64(len(file.data)) > limit {
		return nil, invalidInstallJournal()
	}
	return append([]byte(nil), file.data...), nil
}

func (d *memoryInstallDisk) write(_ context.Context, name string, data []byte, _ JournalObject) (JournalDurability, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seq++
	obj := JournalObject{
		Present:  true,
		SHA256:   shaOf(data),
		Dev:      "8",
		Ino:      itoa(2000 + d.seq),
		MountID:  "42",
		BootID:   d.boot,
		Identity: "8:" + itoa(2000+d.seq),
	}
	if name == transactionMarkerPath(testTxnID) || (len(data) == len(testTxnID) && string(data) == testTxnID) {
		obj.Marker = string(data)
	}
	d.files[name] = memoryFile{data: append([]byte(nil), data...), obj: obj}
	if name == installJournalFileName {
		var journal InstallJournal
		if err := json.Unmarshal(data, &journal); err == nil && journal.RecoveryAuthority != "" {
			d.authority = journal.RecoveryAuthority
		}
	}
	return JournalDurability{Published: true, Durable: true, Object: obj}, nil
}

func (d *memoryInstallDisk) Observe(_ context.Context, action JournalAction) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch action.Kind {
	case JournalActionMask, JournalActionUnmask, JournalActionRestoreMask, JournalActionRestoreUnmask:
		if d.svc.masked {
			return "masked", nil
		}
		return "unmasked", nil
	case JournalActionDisable, JournalActionEnable, JournalActionRestoreDisable, JournalActionRestoreEnable:
		if d.svc.enabled {
			return "enabled", nil
		}
		return "disabled", nil
	case JournalActionStop, JournalActionStart, JournalActionRestoreStop, JournalActionRestoreStart:
		if d.svc.running {
			return "running", nil
		}
		return "stopped", nil
	case JournalActionReload, JournalActionRestoreReload:
		return reloadState(d.reloads), nil
	case JournalActionDataPublish, JournalActionDataIsolate, JournalActionRestoreData:
		return fileState(d.objectLocked(roleData)), nil
	case JournalActionManagedBinary, JournalActionRestoreManagedBinary:
		return fileState(d.objectLocked(roleManaged)), nil
	case JournalActionPathBinary, JournalActionRestorePathBinary:
		return fileState(d.objectLocked(rolePath)), nil
	case JournalActionChannel, JournalActionRestoreChannel:
		return fileState(d.objectLocked(roleChannel)), nil
	case JournalActionDefinition, JournalActionRestoreDefinition:
		return fileState(d.objectLocked(roleCandidate)), nil
	case JournalActionValidationStart, JournalActionValidationStop, JournalActionRestoreValidation:
		return d.validating, nil
	case JournalActionActivation:
		return d.authority, nil
	default:
		return "", unknownInstallState()
	}
}

func (d *memoryInstallDisk) Apply(_ context.Context, action JournalAction) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch action.Kind {
	case JournalActionMask, JournalActionRestoreUnmask:
		d.svc.masked = true
	case JournalActionUnmask, JournalActionRestoreMask:
		d.svc.masked = false
		if cand := d.objectLocked(roleCandidate); len(cand) > 0 && action.Kind == JournalActionUnmask {
			d.putLocked(roleLive, cand)
		}
		if backup := d.objectLocked(roleBackup); len(backup) > 0 && action.Kind == JournalActionRestoreMask {
			d.putLocked(roleLive, backup)
		}
	case JournalActionDisable, JournalActionRestoreEnable:
		d.svc.enabled = false
	case JournalActionEnable, JournalActionRestoreDisable:
		d.svc.enabled = true
	case JournalActionStop, JournalActionRestoreStart:
		d.svc.running = false
	case JournalActionStart, JournalActionRestoreStop:
		d.svc.running = true
	case JournalActionReload, JournalActionRestoreReload:
		d.reloads++
	case JournalActionDataPublish:
		d.putLocked(roleData, d.objectLocked(roleData+".candidate"))
	case JournalActionDataIsolate, JournalActionRestoreData:
		if d.dataAction == InstallDataRetain {
			return nil
		}
		if data := d.objectLocked(roleData); len(data) > 0 {
			d.putLocked(roleIsolated, data)
			d.deleteLocked(roleData)
		}
	case JournalActionManagedBinary:
		d.putLocked(roleManaged, d.objectLocked(roleManaged+".candidate"))
	case JournalActionRestoreManagedBinary:
		d.putLocked(roleManaged, d.objectLocked(roleManaged+".backup"))
	case JournalActionPathBinary:
		d.putLocked(rolePath, d.objectLocked(rolePath+".candidate"))
	case JournalActionRestorePathBinary:
		d.putLocked(rolePath, d.objectLocked(rolePath+".backup"))
	case JournalActionChannel:
		d.putLocked(roleChannel, d.objectLocked(roleChannel+".candidate"))
	case JournalActionRestoreChannel:
		d.putLocked(roleChannel, d.objectLocked(roleChannel+".backup"))
	case JournalActionDefinition:
		d.putLocked(roleCandidate, d.objectLocked(roleLive+".candidate"))
	case JournalActionRestoreDefinition:
		d.deleteLocked(roleCandidate)
	case JournalActionValidationStart:
		d.validating = "running"
	case JournalActionValidationStop, JournalActionRestoreValidation:
		d.validating = "idle"
	case JournalActionActivation:
		d.authority = InstallAuthorityTarget
	default:
		return unknownInstallState()
	}
	return nil
}

func fileState(data []byte) string {
	if len(data) == 0 {
		return "absent"
	}
	return shaOf(data)
}

func reloadState(n int) string {
	return "reload:" + itoa(n)
}

func shaOf(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type fakeInstallLease struct {
	mu        sync.Mutex
	held      bool
	acquires  int
	validates int
	closes    int
	global    bool
	private   bool
	order     []string
	loops     int
}

func (l *fakeInstallLease) acquire(_ context.Context) (InstallLease, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.acquires++
	if l.held {
		l.loops++
		return nil, protocol.APIError{Code: protocol.CodeInvalidState, Message: "install lock is held"}
	}
	l.held = true
	if l.global {
		l.order = append(l.order, "B")
	}
	if l.private {
		l.order = append(l.order, "P")
	}
	return l, nil
}

func (l *fakeInstallLease) Validate(_ context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.held {
		return os.ErrClosed
	}
	l.validates++
	return nil
}

func (l *fakeInstallLease) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.held = false
	l.closes++
	return nil
}

type fakeService struct {
	disk             *memoryInstallDisk
	hook             service.ActionHook
	publicEntrypoint int
}

func (s *fakeService) SetHook(hook service.ActionHook) { s.hook = hook }

func (s *fakeService) InspectDefinition(ctx context.Context) (service.Definition, error) {
	if err := ctx.Err(); err != nil {
		return service.Definition{}, err
	}
	s.disk.mu.Lock()
	defer s.disk.mu.Unlock()
	if !s.disk.svc.installed {
		return service.Definition{Status: service.StatusNotInstalled}, nil
	}
	status := service.StatusStopped
	if s.disk.svc.running {
		status = service.StatusRunning
	}
	return service.Definition{
		Status:  status,
		Enabled: s.disk.svc.enabled,
		Running: s.disk.svc.running,
		Masked:  s.disk.svc.masked,
	}, nil
}

func (s *fakeService) DisableAutostartAndStop(ctx context.Context) error {
	s.disk.mu.Lock()
	installed := s.disk.svc.installed
	enabled := s.disk.svc.enabled
	running := s.disk.svc.running
	s.disk.mu.Unlock()
	if !installed {
		return nil
	}
	if enabled {
		if err := s.act(ctx, service.DefinitionAction{Kind: service.DefinitionActionDisable, TargetRole: JournalRoleDefinition, OldState: "enabled", NewState: "disabled"}, JournalAction{Kind: JournalActionDisable, TargetRole: JournalRoleDefinition, OldState: "enabled", NewState: "disabled"}); err != nil {
			return err
		}
	}
	if err := s.act(ctx, service.DefinitionAction{Kind: service.DefinitionActionMask, TargetRole: JournalRoleDefinition, OldState: "unmasked", NewState: "masked"}, JournalAction{Kind: JournalActionMask, TargetRole: JournalRoleDefinition, OldState: "unmasked", NewState: "masked"}); err != nil {
		return err
	}
	if err := s.act(ctx, service.DefinitionAction{Kind: service.DefinitionActionReload, TargetRole: JournalRoleDefinition, OldState: "stale", NewState: "reloaded"}, JournalAction{Kind: JournalActionReload, TargetRole: JournalRoleDefinition}); err != nil {
		return err
	}
	if running {
		if err := s.act(ctx, service.DefinitionAction{Kind: service.DefinitionActionStop, TargetRole: JournalRoleDefinition, OldState: "running", NewState: "stopped"}, JournalAction{Kind: JournalActionStop, TargetRole: JournalRoleDefinition, OldState: "running", NewState: "stopped"}); err != nil {
			return err
		}
	}
	return nil
}

func (s *fakeService) WaitOwnedTreeExit(ctx context.Context) error { return ctx.Err() }

func (s *fakeService) WriteDefinition(ctx context.Context, def service.Definition) error {
	if err := s.act(ctx, service.DefinitionAction{Kind: service.DefinitionActionUnmask, TargetRole: JournalRoleDefinition, OldState: "masked", NewState: "unmasked"}, JournalAction{Kind: JournalActionUnmask, TargetRole: JournalRoleDefinition, OldState: "masked", NewState: "unmasked"}); err != nil {
		return err
	}
	if def.Enabled {
		if err := s.act(ctx, service.DefinitionAction{Kind: service.DefinitionActionEnable, TargetRole: JournalRoleDefinition, OldState: "disabled", NewState: "enabled"}, JournalAction{Kind: JournalActionEnable, TargetRole: JournalRoleDefinition, OldState: "disabled", NewState: "enabled"}); err != nil {
			return err
		}
	}
	if err := s.act(ctx, service.DefinitionAction{Kind: service.DefinitionActionReload, TargetRole: JournalRoleDefinition, OldState: "stale", NewState: "reloaded"}, JournalAction{Kind: JournalActionReload, TargetRole: JournalRoleDefinition}); err != nil {
		return err
	}
	return nil
}

func (s *fakeService) RestoreDefinition(ctx context.Context, _ service.Definition) error {
	s.publicEntrypoint++
	return ctx.Err()
}

func (s *fakeService) Start(ctx context.Context) error {
	return s.act(ctx, service.DefinitionAction{Kind: service.DefinitionActionStart, TargetRole: JournalRoleDefinition, OldState: "stopped", NewState: "running"}, JournalAction{Kind: JournalActionStart, TargetRole: JournalRoleDefinition, OldState: "stopped", NewState: "running"})
}

func (s *fakeService) Probe(ctx context.Context) (service.Definition, error) {
	return s.InspectDefinition(ctx)
}

func (s *fakeService) act(ctx context.Context, def service.DefinitionAction, action JournalAction) error {
	apply := func(ctx context.Context) error {
		if action.Kind == JournalActionReload {
			s.disk.mu.Lock()
			action.OldState = reloadState(s.disk.reloads)
			action.NewState = reloadState(s.disk.reloads + 1)
			s.disk.mu.Unlock()
		}
		return s.disk.Apply(ctx, action)
	}
	return service.DirectActionHook(ctx, def, func(ctx context.Context) error {
		hook := s.hook
		if hook == nil {
			return apply(ctx)
		}
		return hook(ctx, def, apply)
	})
}

type installHarness struct {
	disk    *memoryInstallDisk
	lease   *fakeInstallLease
	service *fakeService
	tx      *InstallTransaction
	req     InstallRequest
	art     InstallArtifacts
}

func newInstallHarness(t *testing.T, dataAction string) *installHarness {
	t.Helper()
	art := InstallArtifacts{
		DataAction:    dataAction,
		Source:        "/home/user/.mihari",
		Target:        "/var/lib/mihari/data",
		Install:       "/usr/local/lib/mihari",
		Endpoint:      "/var/lib/mihari/control.sock",
		Credential:    "/var/lib/mihari/control.token",
		DataRoot:      "/var/lib/mihari/data",
		OldRunning:    true,
		OldEnabled:    true,
		TargetRunning: true,
		TargetEnabled: true,
		SourceBytes:   []byte("source-tree"),
		DataOld:       []byte("old-data"),
		DataNew:       []byte("new-data"),
		ManagedOld:    []byte("old-managed"),
		ManagedNew:    []byte("new-managed"),
		PathOld:       []byte("old-path"),
		PathNew:       []byte("new-path"),
		ChannelOld:    []byte("old-channel"),
		ChannelNew:    []byte("new-channel"),
		DefinitionOld: []byte("old-unit"),
		DefinitionNew: []byte("new-unit"),
		BootID:        testBootID,
	}
	art.CandidateHash = shaOf(art.DefinitionNew)
	art.BackupHash = shaOf(art.DefinitionOld)
	disk := newMemoryInstallDisk()
	disk.dataAction = dataAction
	disk.sourceBytes = append([]byte(nil), art.SourceBytes...)
	disk.svc = memoryService{installed: true, enabled: true, running: true}
	disk.put(roleSource, art.SourceBytes)
	disk.put(roleBackup, art.DefinitionOld)
	disk.put(roleLive, art.DefinitionOld)
	disk.put(roleManaged, art.ManagedOld)
	disk.put(roleManaged+".backup", art.ManagedOld)
	disk.put(roleManaged+".candidate", art.ManagedNew)
	disk.put(rolePath, art.PathOld)
	disk.put(rolePath+".backup", art.PathOld)
	disk.put(rolePath+".candidate", art.PathNew)
	disk.put(roleChannel, art.ChannelOld)
	disk.put(roleChannel+".backup", art.ChannelOld)
	disk.put(roleChannel+".candidate", art.ChannelNew)
	disk.put(roleLive+".candidate", art.DefinitionNew)
	if dataAction == InstallDataCreate {
		disk.put(roleData+".candidate", art.DataNew)
	} else {
		disk.put(roleData, art.DataOld)
	}
	lease := &fakeInstallLease{global: true}
	svc := &fakeService{disk: disk}
	h := &installHarness{
		disk:    disk,
		lease:   lease,
		service: svc,
		art:     art,
		req: InstallRequest{
			Schema:    InstallRequestSchema,
			Operation: InstallOperationUpdate,
			Binary:    "/tmp/mihari-candidate",
			Channel:   InstallChannelMain,
			Layout:    InstallLayoutSystem,
		},
	}
	h.tx = h.makeTxn(disk, lease)
	return h
}

func (h *installHarness) makeTxn(disk *memoryInstallDisk, lease *fakeInstallLease) *InstallTransaction {
	svc := &fakeService{disk: disk}
	if h.service != nil && disk == h.disk {
		svc = h.service
	}
	tx := &InstallTransaction{
		Store:          &InstallJournalStore{files: disk},
		Service:        svc,
		Effects:        disk,
		Acquire:        lease.acquire,
		Now:            func() time.Time { return time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC) },
		NewID:          func() string { return testTxnID },
		Artifacts:      h.art,
		Private:        lease.private,
		Validation:     harnessValidationChild(h.art),
		ParentIdentity: ProcessStartIdentity{PID: 7, BootID: testBootID, StartUnix: 100},
		EUID:           0,
	}
	svc.hook = tx.journaledHook
	if disk == h.disk {
		h.service = svc
	}
	return tx
}

func (h *installHarness) reconstruct(disk *memoryInstallDisk, lease *fakeInstallLease) *InstallTransaction {
	if !lease.held {
		lease.held = true
	}
	svc := &fakeService{disk: disk}
	tx := &InstallTransaction{
		Store:          &InstallJournalStore{files: disk},
		Service:        svc,
		Effects:        disk,
		Acquire:        lease.acquire,
		Now:            func() time.Time { return time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC) },
		NewID:          func() string { return testTxnID },
		Artifacts:      h.art,
		Private:        lease.private,
		Validation:     harnessValidationChild(h.art),
		ParentIdentity: ProcessStartIdentity{PID: 7, BootID: testBootID, StartUnix: 100},
		EUID:           0,
	}
	svc.hook = tx.journaledHook
	return tx
}

func harnessValidationChild(_ InstallArtifacts) *FakeValidationChild {
	return &FakeValidationChild{Result: ValidationOK, SetupRequired: true}
}

func (h *installHarness) loadedJournal(t *testing.T, disk *memoryInstallDisk) InstallJournal {
	t.Helper()
	got, err := (&InstallJournalStore{files: disk}).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func catchInstallCrash(fn func()) (crashed bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if recovered == errInstallCrash {
				crashed = true
				return
			}
			panic(recovered)
		}
	}()
	fn()
	return false
}

func apiCode(err error) protocol.ErrorCode {
	var api protocol.APIError
	if errors.As(err, &api) {
		return api.Code
	}
	return ""
}
