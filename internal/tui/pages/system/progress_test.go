package system

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type coreProgressClient struct {
	*fakeClient
	progress *protocol.OperationProgress
}

func (c *coreProgressClient) OperationStatus(context.Context, string) (protocol.OperationStatus, error) {
	return protocol.OperationStatus{State: "running", Progress: c.progress}, nil
}

func TestSystemCoreProgress_UsesInstallSentenceOnThePendingChip(t *testing.T) {
	received := int64(1536)
	total := int64(2048)
	client := &coreProgressClient{fakeClient: &fakeClient{}, progress: &protocol.OperationProgress{
		Phase: protocol.ProgressPhaseDownloading, ReceivedBytes: &received, TotalBytes: &total, ElapsedMilliseconds: 1000,
	}}
	model := New(client, func() string { return "core-op" })
	model.SetSize(160, 50)
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityCore}}, protocol.CoreStatus{Version: "v1.19.0", Status: "running", Channel: "stable"})
	model.SetMutationsEnabled(true)
	if model.confirmAction(actionUpdate) == nil || model.coreProgressID != "core-op" {
		t.Fatalf("id=%q", model.coreProgressID)
	}

	updated, command := model.Update(ui.ActionPendingMsg{Action: ui.ActionUpdateCore})
	model = updated.(*Model)
	if model.coreProgressLine != "Installing mihomo core  00:00" || command == nil {
		t.Fatalf("line=%q command=%v", model.coreProgressLine, command != nil)
	}
	batch, ok := command().(tea.BatchMsg)
	if !ok {
		t.Fatal("core install pending did not poll")
	}
	want := "Downloading mihomo core  1.5 KiB / 2.0 KiB  00:01"
	var progress coreProgressMsg
	found := false
	for _, item := range batch {
		message := item()
		page, ok := message.(ui.PageResultMsg)
		if !ok {
			continue
		}
		typed, ok := page.Result.(coreProgressMsg)
		if !ok {
			continue
		}
		progress = typed
		found = true
	}
	if !found || progress.id != "core-op" || progress.line != want {
		t.Fatalf("progress=%#v", progress)
	}

	updated, tick := model.Update(progress)
	model = updated.(*Model)
	if tick == nil || model.coreProgressLine != want {
		t.Fatalf("line=%q tick=%v", model.coreProgressLine, tick != nil)
	}
	model.rowSpinClock = time.Unix(0, 0)
	view := model.View()
	if !strings.Contains(view, want) || strings.Contains(view, ui.CoreProgressUpdating) || strings.Contains(view, "⠋") {
		t.Fatalf("view=%s", view)
	}

	updated, _ = model.Update(coreProgressMsg{id: "other", line: "Extracting mihomo core  00:01"})
	model = updated.(*Model)
	if model.coreProgressLine != want {
		t.Fatalf("stale line=%q", model.coreProgressLine)
	}
	updated, _ = model.Update(actionResultMsg{kind: actionUpdate, install: protocol.CoreInstallResult{Revision: 3, Version: "v1.20.0"}})
	model = updated.(*Model)
	if model.pending || model.coreProgressLine != "" {
		t.Fatalf("pending=%v line=%q", model.pending, model.coreProgressLine)
	}
}

func TestSystemCoreRestart_DoesNotTrackDownloadProgress(t *testing.T) {
	client := &coreProgressClient{fakeClient: &fakeClient{}}
	model := New(client, func() string { return "core-op" })
	model.SetSnapshot(protocol.Status{Capabilities: []string{protocol.CapabilityCore}}, protocol.CoreStatus{Version: "v1.19.0", Status: "running"})
	model.SetMutationsEnabled(true)
	model.coreProgressID = "stale"
	model.coreProgressLine = "Downloading mihomo core  1.0 KiB  00:01"
	if model.confirmAction(actionRestart) == nil || model.coreProgressID != "" {
		t.Fatalf("restart kept id=%q", model.coreProgressID)
	}
	updated, _ := model.Update(ui.ActionPendingMsg{Action: ui.ActionRestartCore})
	model = updated.(*Model)
	if model.coreProgressLine != "" || model.pendingNote != ui.CoreProgressRestarting {
		t.Fatalf("line=%q note=%q", model.coreProgressLine, model.pendingNote)
	}
}
