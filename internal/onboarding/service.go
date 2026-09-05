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

// State is the onboarding-owned persistent state.
type State struct {
	Complete bool
}

type Options struct {
	StatePath            string
	InitialSetupRequired bool
	SaveState            func(string, State) (config.CommitResult, error)
	OnPersistenceWarning func(error)
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
	mu                   sync.RWMutex
	statePath            string
	state                State
	saveState            func(string, State) (config.CommitResult, error)
	onPersistenceWarning func(error)
}

func Open(options Options) (*Service, error) {
	saver := options.SaveState
	if saver == nil {
		saver = saveState
	}
	state, err := loadState(options.StatePath)
	if errors.Is(err, os.ErrNotExist) {
		state = State{Complete: !options.InitialSetupRequired}
		result, saveErr := saver(options.StatePath, state)
		if saveErr != nil {
			return nil, saveErr
		}
		if !result.Committed {
			return nil, dataError("persist onboarding state")
		}
		reportPersistenceWarning(options.OnPersistenceWarning, result.Warning)
	} else if err != nil {
		return nil, err
	}
	return &Service{
		statePath:            options.StatePath,
		state:                state,
		saveState:            saver,
		onPersistenceWarning: options.OnPersistenceWarning,
	}, nil
}

// State returns a snapshot of the onboarding-owned state.
func (s *Service) State() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// Update persists and publishes a completion-state update.
func (s *Service) Update(complete *bool) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if complete == nil || *complete == s.state.Complete {
		return s.state, nil
	}
	next := State{Complete: *complete}
	result, err := s.saveState(s.statePath, next)
	if err != nil {
		return State{}, err
	}
	if !result.Committed {
		return State{}, dataError("persist onboarding state")
	}
	s.state = next
	reportPersistenceWarning(s.onPersistenceWarning, result.Warning)
	return s.state, nil
}

func reportPersistenceWarning(report func(error), warning error) {
	if warning != nil && report != nil {
		report(errors.New("onboarding parent directory sync failed after commit"))
	}
}

func loadState(path string) (State, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	if len(raw) > maxStateSize {
		return State{}, dataError("onboarding state is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var persisted persistedState
	if err := decoder.Decode(&persisted); err != nil {
		return State{}, dataError("invalid onboarding state")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return State{}, dataError("onboarding state must contain one document")
	}
	if persisted.Schema != stateSchema {
		return State{}, dataError("unsupported onboarding state schema")
	}
	return State{Complete: persisted.Complete}, nil
}

func saveState(path string, state State) (config.CommitResult, error) {
	raw, err := json.Marshal(persistedState{Schema: stateSchema, Complete: state.Complete})
	if err != nil {
		return config.CommitResult{}, fmt.Errorf("encode onboarding state: %w", err)
	}
	return config.AtomicWriteWithCommit(path, append(raw, '\n'), 0o600)
}

func dataError(message string) error {
	return protocol.APIError{Code: protocol.CodeDataFailure, Message: message}
}
