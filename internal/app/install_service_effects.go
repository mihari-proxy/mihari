package app

import (
	"context"
	"strings"

	"github.com/mihari-proxy/mihari/internal/service"
)

// installServiceEffects resolves journal refs only through the root-private
// old/target definition backup. Journal strings never become arbitrary paths.
type installServiceEffects struct {
	files       installServiceFiles
	adapter     service.RecoveryAdapter
	old, target service.Definition
}

type installServiceFiles interface {
	Observe(context.Context, string) (string, error)
	Apply(context.Context, service.DefinitionAction) error
}

func (s *installServiceEffects) observeAction(ctx context.Context, def service.DefinitionAction) (string, error) {
	if s.files != nil && def.Path != "" {
		return s.files.Observe(ctx, def.Path)
	}
	return s.adapter.ObserveAction(ctx, def)
}
func (s *installServiceEffects) replayAction(ctx context.Context, def service.DefinitionAction) error {
	if s.files != nil && def.Path != "" {
		return s.files.Apply(ctx, def)
	}
	return s.adapter.ReplayAction(ctx, def)
}
func (s *installServiceEffects) paths() map[string]string {
	out := map[string]string{}
	for _, def := range []service.Definition{s.old, s.target} {
		for _, file := range def.Files {
			out["service:"+sha256Hex(file.Path)] = file.Path
		}
		for _, link := range def.Links {
			out["service:"+sha256Hex(link.Path)] = link.Path
		}
	}
	return out
}
func (s *installServiceEffects) prepare(ctx context.Context, def service.DefinitionAction) (JournalAction, error) {
	if s.adapter == nil {
		return JournalAction{}, unknownInstallState()
	}
	action := JournalAction{Kind: def.Kind, TargetRole: def.TargetRole, NewState: def.NewState}
	if def.Path != "" {
		ref := "service:" + sha256Hex(def.Path)
		if s.paths()[ref] != def.Path {
			return JournalAction{}, unknownInstallState()
		}
		action.BackupRef, action.CandidateRef = ref, ref
		switch {
		case def.File != nil:
			action.NewState = service.DefinitionFileState(*def.File, "")
		case def.Link != "":
			action.NewState = service.DefinitionFileState(service.DefinitionFile{}, def.Link)
		default:
			action.NewState = "absent"
		}
	}
	actual, err := s.observeAction(ctx, def)
	if err != nil {
		return JournalAction{}, err
	}
	action.OldState = actual
	if _, err := s.resolve(action); err != nil {
		return JournalAction{}, err
	}
	return action, nil
}
func (s *installServiceEffects) resolve(action JournalAction) (service.DefinitionAction, error) {
	def := service.DefinitionAction{Kind: strings.TrimPrefix(action.Kind, "restore_"), TargetRole: action.TargetRole, OldState: action.OldState, NewState: action.NewState}
	if action.CandidateRef != "" || action.BackupRef != "" {
		if action.CandidateRef != action.BackupRef {
			return def, unknownInstallState()
		}
		def.Path = s.paths()[action.CandidateRef]
		if def.Path == "" {
			return def, unknownInstallState()
		}
		if action.NewState == "absent" {
			return def, nil
		}
		for _, snapshot := range []service.Definition{s.old, s.target} {
			for _, file := range snapshot.Files {
				if file.Path == def.Path && service.DefinitionFileState(file, "") == action.NewState {
					copy := file
					def.File = &copy
					return def, nil
				}
			}
			for _, link := range snapshot.Links {
				if link.Path == def.Path && "link:"+link.Target == action.NewState {
					def.Link = link.Target
					return def, nil
				}
			}
		}
		// The sole temporary link not present in either definition is a unit mask.
		if action.NewState == "link:/dev/null" && strings.HasSuffix(def.Path, "/mihari.service") {
			def.Link = "/dev/null"
			return def, nil
		}
		return def, unknownInstallState()
	}
	switch def.Kind {
	case service.DefinitionActionReload:
	case service.DefinitionActionStart, service.DefinitionActionStop:
		switch action.NewState {
		case "running", "loaded":
			def.Kind = service.DefinitionActionStart
		case "stopped", "unloaded":
			def.Kind = service.DefinitionActionStop
		default:
			return def, unknownInstallState()
		}
	case service.DefinitionActionEnable, service.DefinitionActionDisable, service.DefinitionActionDisabled:
		if action.NewState != "enabled" && action.NewState != "disabled" {
			return def, unknownInstallState()
		}
	default:
		return def, unknownInstallState()
	}
	return def, nil
}
func (s *installServiceEffects) observe(ctx context.Context, action JournalAction) (string, error) {
	def, err := s.resolve(action)
	if err != nil {
		return "", err
	}
	return s.observeAction(ctx, def)
}
func (s *installServiceEffects) apply(ctx context.Context, action JournalAction) error {
	def, err := s.resolve(action)
	if err != nil {
		return err
	}
	if def.Kind == service.DefinitionActionReload {
		return s.replayAction(ctx, def)
	}
	actual, err := s.observeAction(ctx, def)
	if err != nil {
		return err
	}
	if actual == action.NewState {
		return nil
	}
	if actual != action.OldState {
		return unknownInstallState()
	}
	if err := s.replayAction(ctx, def); err != nil {
		return err
	}
	actual, err = s.observeAction(ctx, def)
	if err != nil {
		return err
	}
	if actual != action.NewState {
		return unknownInstallState()
	}
	return nil
}

func isConcreteServiceAction(action JournalAction) bool {
	if strings.HasPrefix(action.CandidateRef, "service:") || strings.HasPrefix(action.BackupRef, "service:") {
		return true
	}
	switch strings.TrimPrefix(action.Kind, "restore_") {
	case JournalActionEnable, JournalActionDisable, JournalActionDisabled, JournalActionStop, JournalActionStart, JournalActionReload:
		return true
	}
	return false
}
