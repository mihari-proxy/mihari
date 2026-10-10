package setup

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestSetupCoreProgress_ReplacesSpinnerUntilSettlement(t *testing.T) {
	client := &observingClient{fakeClient: &fakeClient{status: defaultStatus(false)}, state: "running"}
	model := loadedModel(client.fakeClient)
	model.client = client
	model.step = stepCore
	model.coreLocalLoaded = true

	updated, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(*Model)
	if command == nil || model.progressLine != "Installing mihomo core  00:00" {
		t.Fatalf("line=%q command=%v", model.progressLine, command != nil)
	}
	batch, ok := command().(tea.BatchMsg)
	if !ok {
		t.Fatal("observer install did not poll beside the request")
	}
	var polled coreProgressMsg
	found := false
	for _, item := range batch {
		message := item()
		typed, ok := message.(coreProgressMsg)
		if !ok {
			continue
		}
		polled = typed
		found = true
	}
	if !found || !strings.HasPrefix(polled.line, "Installing mihomo core  ") || polled.gen != model.executionGen {
		t.Fatalf("polled=%#v gen=%d", polled, model.executionGen)
	}

	line := "Downloading mihomo core  1.5 KiB / 2.0 KiB  00:01"
	updated, tick := model.Update(coreProgressMsg{gen: model.executionGen, line: line})
	model = updated.(*Model)
	if tick == nil || model.progressLine != line {
		t.Fatalf("line=%q tick=%v", model.progressLine, tick != nil)
	}
	view := model.View()
	if !strings.Contains(view, line) || strings.Contains(view, "⠋") {
		t.Fatalf("view=%s", view)
	}

	model.settling = true
	if strings.Contains(model.View(), "Downloading mihomo core") {
		t.Fatal("settlement kept the download line")
	}
	updated, next := model.Update(coreProgressMsg{gen: model.executionGen + 1, line: "Extracting mihomo core  00:01"})
	model = updated.(*Model)
	if next != nil || strings.Contains(model.progressLine, "Extracting") {
		t.Fatalf("stale line=%q next=%v", model.progressLine, next != nil)
	}
}

func TestSetupCoreProgress_OldDaemonKeepsInstallingLine(t *testing.T) {
	client := &observingClient{fakeClient: &fakeClient{status: defaultStatus(false)}, state: "running"}
	model := loadedModel(client.fakeClient)
	model.client = client
	model.step = stepCore
	model.coreLocalLoaded = true
	model.installCore()
	updated, command := model.Update(model.pollCoreProgress()())
	model = updated.(*Model)
	if command == nil || !strings.HasPrefix(model.progressLine, "Installing mihomo core  ") || strings.Contains(model.progressLine, "0 B") {
		t.Fatalf("line=%q command=%v", model.progressLine, command != nil)
	}
}
