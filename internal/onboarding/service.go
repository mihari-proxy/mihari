package onboarding

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

const (
	stateSchema  = "mihari.onboarding/v1"
	maxStateSize = 64 << 10
)

type Options struct {
	StatePath            string
	SettingsPath         string
	Settings             config.Settings
	InitialSetupRequired bool
}

type Status struct {
	Complete        bool
	MixedAddr       string
	ControllerAddr  string
	WebAddr         string
	RestartRequired bool
}

// Snapshot couples onboarding values with the global revision at which they were observed.
type Snapshot struct {
	Status   Status
	Revision uint64
}

type Update struct {
	Complete       *bool
	MixedAddr      *string
	ControllerAddr *string
	WebAddr        *string
}

type persistedState struct {
	Schema   string `json:"schema"`
	Complete bool   `json:"complete"`
}

type Service struct {
	mu              sync.RWMutex
	statePath       string
	settingsPath    string
	state           persistedState
	settings        config.Settings
	restartRequired bool
}

func Open(options Options) (*Service, error) {
	if err := options.Settings.Validate(); err != nil {
		return nil, err
	}
	state, err := loadState(options.StatePath)
	if errors.Is(err, os.ErrNotExist) {
		state = persistedState{Schema: stateSchema, Complete: !options.InitialSetupRequired}
		if err := saveState(options.StatePath, state); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return &Service{statePath: options.StatePath, settingsPath: options.SettingsPath, state: state, settings: options.Settings}, nil
}

func (s *Service) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.statusLocked()
}

func (s *Service) Update(update Update) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	nextSettings := s.settings
	if update.MixedAddr != nil {
		nextSettings.MixedAddr = *update.MixedAddr
	}
	if update.ControllerAddr != nil {
		nextSettings.ControllerAddr = *update.ControllerAddr
	}
	if update.WebAddr != nil {
		nextSettings.WebAddr = *update.WebAddr
	}
	if err := nextSettings.Validate(); err != nil {
		return Status{}, err
	}
	nextState := s.state
	if update.Complete != nil {
		nextState.Complete = *update.Complete
	}
	endpointChanged := nextSettings.MixedAddr != s.settings.MixedAddr || nextSettings.ControllerAddr != s.settings.ControllerAddr || nextSettings.WebAddr != s.settings.WebAddr
	if endpointChanged {
		if err := config.Save(s.settingsPath, nextSettings); err != nil {
			return Status{}, err
		}
	}
	if nextState != s.state {
		if err := saveState(s.statePath, nextState); err != nil {
			if endpointChanged {
				if rollbackErr := config.Save(s.settingsPath, s.settings); rollbackErr != nil {
					return Status{}, dataError("persist onboarding state failed and settings rollback failed")
				}
			}
			return Status{}, err
		}
	}
	s.settings, s.state = nextSettings, nextState
	s.restartRequired = s.restartRequired || endpointChanged
	return s.statusLocked(), nil
}

func (s *Service) statusLocked() Status {
	return Status{Complete: s.state.Complete, MixedAddr: s.settings.MixedAddr, ControllerAddr: s.settings.ControllerAddr, WebAddr: s.settings.WebAddr, RestartRequired: s.restartRequired}
}

func loadState(path string) (persistedState, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return persistedState{}, err
	}
	if len(raw) > maxStateSize {
		return persistedState{}, dataError("onboarding state is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var state persistedState
	if err := decoder.Decode(&state); err != nil {
		return persistedState{}, dataError("invalid onboarding state")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return persistedState{}, dataError("onboarding state must contain one document")
	}
	if state.Schema != stateSchema {
		return persistedState{}, dataError("unsupported onboarding state schema")
	}
	return state, nil
}

func saveState(path string, state persistedState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode onboarding state: %w", err)
	}
	return config.AtomicWrite(path, append(raw, '\n'), 0o600)
}

func dataError(message string) error {
	return protocol.APIError{Code: protocol.CodeDataFailure, Message: message}
}
