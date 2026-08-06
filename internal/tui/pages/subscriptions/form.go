package subscriptions

import (
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
)

type formKind uint8

const (
	formAdd formKind = iota
	formEdit
)

type formModel struct {
	kind   formKind
	inputs []textinput.Model
	labels []string
	index  int
}

func newAddForm() *formModel {
	return newForm(formAdd, []string{"Name", "URL"}, []string{"", ""}, []string{"Subscription name", "https://example.test/subscription"})
}

func newEditForm(subscription protocol.Subscription) *formModel {
	return newForm(formEdit,
		[]string{"Name", "URL", "Interval", "Auto refresh"},
		[]string{subscription.Name, "", subscription.Interval, strconv.FormatBool(subscription.AutoRefresh)},
		[]string{"Subscription name", "Leave blank to keep the stored URL", "Use global interval when blank", "true or false"},
	)
}

func newForm(kind formKind, labels, values, placeholders []string) *formModel {
	form := &formModel{kind: kind, labels: append([]string(nil), labels...), inputs: make([]textinput.Model, len(labels))}
	for index := range labels {
		input := textinput.New()
		input.Prompt = ""
		input.Placeholder = placeholders[index]
		input.CharLimit = 2048
		input.SetWidth(52)
		input.SetValue(values[index])
		form.inputs[index] = input
	}
	if len(form.inputs) > 0 {
		_ = form.inputs[0].Focus()
	}
	return form
}

func (f *formModel) Update(message tea.Msg) (bool, tea.Cmd) {
	if key, ok := message.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc":
			return true, nil
		case "tab":
			return false, f.move(1)
		case "shift+tab":
			return false, f.move(-1)
		}
	}
	// Forward keys, bracketed paste, and clipboard paste results into the focused textinput.
	updated, command := f.inputs[f.index].Update(message)
	f.inputs[f.index] = updated
	return false, command
}

func (f *formModel) move(delta int) tea.Cmd {
	f.inputs[f.index].Blur()
	f.index = (f.index + delta + len(f.inputs)) % len(f.inputs)
	return f.inputs[f.index].Focus()
}

func (f *formModel) View() string {
	lines := make([]string, 0, len(f.inputs)*2)
	for index := range f.inputs {
		marker := "  "
		if index == f.index {
			marker = ui.FocusMarker
		}
		lines = append(lines, marker+f.labels[index], "  "+f.inputs[index].View())
	}
	return strings.Join(lines, "\n")
}

func (f *formModel) valid() bool {
	if strings.TrimSpace(f.inputs[0].Value()) == "" {
		return false
	}
	if f.kind == formAdd {
		return strings.TrimSpace(f.inputs[1].Value()) != ""
	}
	_, err := strconv.ParseBool(strings.TrimSpace(f.inputs[3].Value()))
	return err == nil
}

func (f *formModel) addRequest(operationID string, revision uint64) protocol.SubscriptionAddRequest {
	request := protocol.SubscriptionAddRequest{OperationID: operationID, Name: strings.TrimSpace(f.inputs[0].Value()), URL: strings.TrimSpace(f.inputs[1].Value())}
	request.IfRevision = &revision
	return request
}

func (f *formModel) updateRequest(operationID string, revision uint64) protocol.SubscriptionUpdateRequest {
	name := strings.TrimSpace(f.inputs[0].Value())
	interval := strings.TrimSpace(f.inputs[2].Value())
	autoRefresh, _ := strconv.ParseBool(strings.TrimSpace(f.inputs[3].Value()))
	request := protocol.SubscriptionUpdateRequest{OperationID: operationID, IfRevision: &revision, Name: &name, Interval: &interval, AutoRefresh: &autoRefresh}
	if rawURL := strings.TrimSpace(f.inputs[1].Value()); rawURL != "" {
		request.URL = &rawURL
	}
	return request
}
