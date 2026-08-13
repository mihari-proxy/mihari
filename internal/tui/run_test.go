package tui

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestFinishRunRelaunchesOnlyWhenRequested(t *testing.T) {
	model := NewModel()
	model.relaunchRequested = true
	model.relaunchWarning = "service restart failed"
	var warnings bytes.Buffer
	calls := 0
	err := finishRun(model, nil, &warnings, func() error {
		calls++
		if !strings.Contains(warnings.String(), "service restart failed") {
			t.Fatal("relaunch ran before warning was written")
		}
		return nil
	}, nil)
	if err != nil || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestFinishRunCleansUpSessionBeforeRelaunch(t *testing.T) {
	model := NewModel()
	model.relaunchRequested = true
	cleaned := false

	err := finishRun(model, nil, io.Discard, func() error {
		if !cleaned {
			t.Fatal("relaunch ran before control session cleanup")
		}
		return nil
	}, func() { cleaned = true })
	if err != nil {
		t.Fatal(err)
	}
	if !cleaned {
		t.Fatal("control session cleanup did not run")
	}
}

func TestFinishRunNormalExitDoesNotRelaunch(t *testing.T) {
	calls := 0
	if err := finishRun(NewModel(), nil, io.Discard, func() error { calls++; return nil }, nil); err != nil || calls != 0 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestFinishRunProgramErrorPreventsRelaunch(t *testing.T) {
	model := NewModel()
	model.relaunchRequested = true
	runErr := errors.New("program failed")
	calls := 0
	err := finishRun(model, runErr, io.Discard, func() error { calls++; return nil }, nil)
	if !errors.Is(err, runErr) || calls != 0 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestFinishRunReturnsRelaunchError(t *testing.T) {
	model := NewModel()
	model.relaunchRequested = true
	relaunchErr := errors.New("start replacement failed")
	err := finishRun(model, nil, io.Discard, func() error { return relaunchErr }, nil)
	if !errors.Is(err, relaunchErr) {
		t.Fatalf("err=%v", err)
	}
}

type failingWriter struct{ err error }

func (writer failingWriter) Write([]byte) (int, error) { return 0, writer.err }

func TestFinishRunWarningWriteFailureStillRelaunches(t *testing.T) {
	model := NewModel()
	model.relaunchRequested = true
	model.relaunchWarning = "service restart failed"
	writeErr := errors.New("terminal is closed")
	calls := 0

	err := finishRun(model, nil, failingWriter{err: writeErr}, func() error {
		calls++
		return nil
	}, nil)
	if err != nil || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestFinishRunPreservesWarningAndRelaunchErrors(t *testing.T) {
	model := NewModel()
	model.relaunchRequested = true
	model.relaunchWarning = "service restart failed"
	writeErr := errors.New("terminal is closed")
	relaunchErr := errors.New("start replacement failed")

	err := finishRun(model, nil, failingWriter{err: writeErr}, func() error { return relaunchErr }, nil)
	if !errors.Is(err, writeErr) || !errors.Is(err, relaunchErr) {
		t.Fatalf("err=%v", err)
	}
}

func TestLoadingModel_ViewUsesAlternateScreen(t *testing.T) {
	view := (loadingModel{}).View()
	if !view.AltScreen {
		t.Fatal("loading view must use the alternate screen")
	}
}

func TestLoadingModel_QuitKeysStopProgram(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: 'q', Text: "q"},
		{Code: 'c', Mod: tea.ModCtrl},
	} {
		_, command := (loadingModel{}).Update(key)
		if command == nil {
			t.Fatalf("key=%q did not return a quit command", key.String())
		}
		if message := command(); message != tea.Quit() {
			t.Fatalf("key=%q message=%#v", key.String(), message)
		}
	}
}
