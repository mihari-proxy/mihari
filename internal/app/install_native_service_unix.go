//go:build linux || darwin

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
)

type nativeServiceObject struct {
	Path, ParentIdentity, Stage, StageIdentity string
	Versions                                   []serviceObjectVersion
}
type nativeServiceFiles struct {
	objects                   map[string]nativeServiceObject
	recordedBoot, currentBoot string
}

func serviceEntryState(entry platform.ServiceEntry) string {
	if !entry.Present {
		return "absent"
	}
	return service.DefinitionFileState(service.DefinitionFile{Bytes: entry.Bytes, Owner: entry.Owner, Mode: entry.Mode}, entry.Link)
}
func (s *nativeInstallSession) prepareServiceFiles(ctx context.Context) error {
	if s.state.Foreground {
		return nil
	}
	s.state.ServiceFiles = map[string]nativeServiceObject{}
	values := map[string][]service.DefinitionAction{}
	identities := map[string]string{}
	expectedStates := map[string]string{}
	for index, def := range []service.Definition{s.state.OldDefinition, s.state.TargetDefinition} {
		for _, f := range def.Files {
			f := f
			values[f.Path] = append(values[f.Path], service.DefinitionAction{Path: f.Path, File: &f})
			if index == 0 {
				identities[f.Path] = f.Identity
				expectedStates[f.Path] = service.DefinitionFileState(f, "")
			}
		}
		for _, l := range def.Links {
			values[l.Path] = append(values[l.Path], service.DefinitionAction{Path: l.Path, Link: l.Target})
			if index == 0 {
				identities[l.Path] = l.Identity
				expectedStates[l.Path] = "link:" + l.Target
			}
		}
	}
	for path := range values {
		if strings.HasSuffix(path, "/mihari.service") {
			values[path] = append(values[path], service.DefinitionAction{Path: path, Link: "/dev/null"})
		}
	}
	paths := make([]string, 0, len(values))
	for path := range values {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		expected, known := identities[path]
		if !known {
			expected = "absent"
		}
		object, err := prepareNativeServiceObject(ctx, s.state.TransactionID, path, expected, expectedStates[path], values[path])
		s.state.ServiceFiles[path] = object
		if err != nil {
			return err
		}
	}
	return nil
}
func prepareNativeServiceObject(ctx context.Context, id, path, expected, expectedState string, values []service.DefinitionAction) (object nativeServiceObject, err error) {
	object.Path = path
	parent, err := platform.OpenTrustedParent(ctx, filepath.Dir(path), 0)
	if err != nil {
		return object, err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	_, object.ParentIdentity, _, _, err = parent.Snapshot(ctx)
	if err != nil {
		return object, err
	}
	old, err := parent.ReadServiceEntry(ctx, filepath.Base(path))
	if err != nil {
		return object, err
	}
	if (expected == "absent" && old.Present) || (expected != "absent" && (!old.Present || old.Key() != expected || serviceEntryState(old) != expectedState)) {
		return object, unknownInstallState()
	}
	if old.Present {
		object.Versions = append(object.Versions, serviceObjectVersion{Name: "original", State: serviceEntryState(old), Identity: old.Key()})
	}
	object.Stage = ".mihari-service-" + id + "-" + sha256Hex(path)[:16]
	stage, err := parent.OpenDir(ctx, object.Stage, platform.RootPolicy{Owner: 0, Mode: 0700, AllowCreate: true})
	if err != nil {
		return object, err
	}
	defer func() { err = errors.Join(err, stage.Close()) }()
	_, object.StageIdentity, _, _, err = stage.Snapshot(ctx)
	if err != nil {
		return object, err
	}
	seen := map[string]bool{}
	for _, value := range values {
		body, link, mode := []byte(nil), value.Link, uint32(0644)
		if value.File != nil {
			body, mode = value.File.Bytes, value.File.Mode
		}
		state := service.DefinitionFileState(service.DefinitionFile{Bytes: body, Mode: mode}, link)
		if seen[state] {
			continue
		}
		seen[state] = true
		name := "object-" + strconv.Itoa(len(object.Versions))
		if err := stage.WriteServiceEntry(ctx, name, body, link, mode, platform.ServiceEntry{}); err != nil {
			return object, err
		}
		entry, err := stage.ReadServiceEntry(ctx, name)
		if err != nil {
			return object, err
		}
		object.Versions = append(object.Versions, serviceObjectVersion{Name: name, State: state, Identity: entry.Key()})
	}
	return object, nil
}
func (f *nativeServiceFiles) openParent(ctx context.Context, object nativeServiceObject) (root *platform.TrustedRoot, err error) {
	root, err = platform.OpenTrustedParent(ctx, filepath.Dir(object.Path), 0)
	if err != nil {
		return nil, err
	}
	_, id, _, _, err := root.Snapshot(ctx)
	if err != nil || (f.currentBoot == f.recordedBoot && id != object.ParentIdentity) {
		return nil, errors.Join(err, unknownInstallState(), root.Close())
	}
	return root, nil
}
func (f *nativeServiceFiles) Observe(ctx context.Context, path string) (state string, err error) {
	object, ok := f.objects[path]
	if !ok {
		return "", unknownInstallState()
	}
	parent, err := f.openParent(ctx, object)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	entry, err := parent.ReadServiceEntry(ctx, filepath.Base(path))
	if err != nil {
		return "", err
	}
	state = serviceEntryState(entry)
	if !entry.Present {
		return state, nil
	}
	if !serviceObjectMatches(object.Versions, state, entry.Key(), f.recordedBoot, f.currentBoot) {
		return "", unknownInstallState()
	}
	return state, nil
}
func (f *nativeServiceFiles) Apply(ctx context.Context, action service.DefinitionAction) (err error) {
	object, ok := f.objects[action.Path]
	if !ok {
		return unknownInstallState()
	}
	parent, err := f.openParent(ctx, object)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	old, err := parent.ReadServiceEntry(ctx, filepath.Base(action.Path))
	if err != nil {
		return err
	}
	current := serviceEntryState(old)
	if old.Present && !serviceObjectMatches(object.Versions, current, old.Key(), f.recordedBoot, f.currentBoot) {
		return unknownInstallState()
	}
	if current == action.NewState {
		return nil
	}
	if current != action.OldState {
		return unknownInstallState()
	}
	stage, err := parent.OpenDir(ctx, object.Stage, platform.RootPolicy{Owner: 0, Mode: 0700})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, stage.Close()) }()
	_, stageID, _, _, err := stage.Snapshot(ctx)
	if err != nil {
		return err
	}
	if f.currentBoot == f.recordedBoot && stageID != object.StageIdentity {
		return unknownInstallState()
	}
	return publishServiceObject(ctx, &nativeServiceSlots{stage: stage, parent: parent, path: filepath.Base(action.Path), live: old}, object.Versions, serviceSlotEntry{Present: old.Present, State: current, Identity: old.Key()}, action.NewState, f.recordedBoot, f.currentBoot)
}

type nativeServiceSlots struct {
	held          map[string]platform.ServiceEntry
	stage, parent *platform.TrustedRoot
	path          string
	live          platform.ServiceEntry
}

func (s *nativeServiceSlots) Read(ctx context.Context, name string) (serviceSlotEntry, error) {
	entry, err := s.stage.ReadServiceEntry(ctx, name)
	if err == nil {
		if s.held == nil {
			s.held = map[string]platform.ServiceEntry{}
		}
		s.held[name] = entry
	}
	return serviceSlotEntry{Present: entry.Present, State: serviceEntryState(entry), Identity: entry.Key()}, err
}
func (s *nativeServiceSlots) Publish(ctx context.Context, name string) error {
	candidate, ok := s.held[name]
	if !ok || !candidate.Present {
		return unknownInstallState()
	}
	return s.stage.MoveServiceEntryTo(ctx, name, candidate, s.parent, s.path, s.live)
}
func (s *nativeServiceSlots) Exchange(ctx context.Context, name string) error {
	candidate, ok := s.held[name]
	if !ok || !candidate.Present {
		return unknownInstallState()
	}
	// The journal retains its intent on error. Observe resolves the actual live
	// inode even when exchange committed and a subsequent parent sync failed.
	_, err := s.stage.ExchangeServiceEntryWith(ctx, name, candidate, s.parent, s.path, s.live)
	return err
}
func (s *nativeServiceSlots) Retain(ctx context.Context, name string) error {
	return s.parent.MoveServiceEntryTo(ctx, s.path, s.live, s.stage, name, platform.ServiceEntry{})
}

func (f *nativeServiceFiles) Cleanup(ctx context.Context) (result error) {
	for _, object := range f.objects {
		result = errors.Join(result, f.cleanupObject(ctx, object))
	}
	return result
}
func (f *nativeServiceFiles) cleanupObject(ctx context.Context, object nativeServiceObject) (err error) {
	parent, err := f.openParent(ctx, object)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, parent.Close()) }()
	stage, err := parent.OpenDir(ctx, object.Stage, platform.RootPolicy{Owner: 0, Mode: 0700})
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, stage.Close()) }()
	_, id, _, _, err := stage.Snapshot(ctx)
	if err != nil {
		return err
	}
	if f.currentBoot == f.recordedBoot && id != object.StageIdentity {
		return unknownInstallState()
	}
	for _, v := range object.Versions {
		entry, err := stage.ReadServiceEntry(ctx, v.Name)
		if err != nil {
			return err
		}
		if !entry.Present {
			continue
		}
		if !serviceObjectMatches(object.Versions, serviceEntryState(entry), entry.Key(), f.recordedBoot, f.currentBoot) {
			return unknownInstallState()
		}
		if err := stage.RemoveServiceEntry(ctx, v.Name, entry); err != nil {
			return err
		}
	}
	return parent.RemoveEmptyDir(ctx, object.Stage)
}
