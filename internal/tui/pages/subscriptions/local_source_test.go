package subscriptions

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestAddForm_SourceToggleClearsDraft(t *testing.T) {
	f := newAddForm()
	f.inputs[0].SetValue("local")
	f.inputs[1].SetValue("https://example.test")
	// Source follows the existing three fields in keyboard order.
	f.index = 3
	f.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if f.inputs[1].Value() != "" {
		t.Fatal("source toggle retained old URL")
	}
	path := filepath.Join(t.TempDir(), "local.yaml")
	f.inputs[1].SetValue(path)
	if !f.valid() {
		t.Fatal(f.errorText)
	}
	want, _ := platform.FileURI(path)
	if got := f.addRequest("op", 1); got.URL != want || got.ProxyMode != "" {
		t.Fatalf("request=%+v", got)
	}
}

func TestAddForm_FileConfirmationDefaultsCancelAndRetriesWithNewID(t *testing.T) {
	c := &detailClient{fakeClient: &fakeClient{}}
	next := 0
	m := New(c, func() string { next++; return fmt.Sprintf("operation-%d", next) }, nil)
	m.SetSize(80, 24)
	f := newAddForm()
	f.inputs[0].SetValue("refs")
	f.inputs[1].SetValue("https://fixture.test/sub")
	m.openForm(f, "")
	first := m.submitForm(f, "", 0)
	result := mutationResultFromCmd(t, first)
	result.err = protocol.APIError{Code: protocol.CodeInvalidArgument, Message: strings.Repeat("File references require acknowledgement.\n", 40), Details: map[string]any{"confirmation_required": "file_references"}}
	m.finishSave(result)
	if m.saveState != saveFileReferences || m.confirmYes {
		t.Fatal("confirmation not default Cancel")
	}
	view := m.View()
	if !strings.Contains(view, "[Cancel]") || len(strings.Split(view, "\n")) > 24 {
		t.Fatal("confirmation choices not visible within viewport")
	}
	m.updateSaveKeys(tea.KeyPressMsg{Code: tea.KeyRight})
	retry := m.updateSaveKeys(tea.KeyPressMsg{Code: tea.KeyEnter})
	second := mutationResultFromCmd(t, retry)
	if !f.allowReferences || second.operation.ID == result.operation.ID {
		t.Fatal("retry reused rejected operation or lost acknowledgement")
	}
}

func TestEditForm_LocalSourceLockedAndPathRevealed(t *testing.T) {
	f := newEditForm(protocol.Subscription{Name: "local", SourceType: "file"})
	path := filepath.Join(t.TempDir(), "main.yaml")
	uri, _ := platform.FileURI(path)
	f.reveal(uri)
	if f.inputs[1].Value() != path || !f.localSource {
		t.Fatal("file source not shown as native path")
	}
	f.urlTouched = true
	if request := f.updateRequest("op", 1); request.URL != nil {
		t.Fatal("unchanged file path submitted as changed")
	}
	if !f.valid() {
		t.Fatal(f.errorText)
	}
}
