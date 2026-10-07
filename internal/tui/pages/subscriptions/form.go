package subscriptions

import (
	"errors"
	"fmt"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"net/url"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type formKind uint8

const (
	formAdd formKind = iota
	formEdit
)

type formModel struct {
	validationErr   error
	kind            formKind
	inputs          []textinput.Model
	labels          []string
	index           int
	baseline        protocol.Subscription
	urlBaseline     string
	urlTouched      bool
	errorText       string
	actionOperation string
	actionUncertain bool
}

func newAddForm() *formModel {
	return newForm(formAdd, []string{"Name", "URL", "Mode"}, []string{"", "", ""}, []string{"Subscription name", "https://example.test/subscription", ""})
}

func newEditForm(subscription protocol.Subscription) *formModel {
	f := newForm(formEdit,
		[]string{"Name", "URL", "Interval", "Auto refresh", "Mode", "Enabled", "InUse"},
		[]string{subscription.Name, "", subscription.Interval, strconv.FormatBool(subscription.AutoRefresh), subscription.ProxyMode, "", ""},
		[]string{"Subscription name", "Loading URL...", "Use global interval when blank", "", "", "", ""},
	)
	f.baseline = subscription
	return f
}

// newForm initializes the shared draft and text-input styling for add and edit.
func newForm(kind formKind, labels, values, placeholders []string) *formModel {
	form := &formModel{kind: kind, labels: append([]string(nil), labels...), inputs: make([]textinput.Model, len(labels))}
	for index := range labels {
		input := textinput.New()
		input.Prompt = ""
		styles := textinput.DefaultDarkStyles()
		styles.Focused.Text = lipgloss.NewStyle().Foreground(lipgloss.Color("7")).Background(lipgloss.Color("236"))
		styles.Focused.Placeholder = styles.Focused.Text.Foreground(lipgloss.Color("245"))
		styles.Blurred.Text = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
		styles.Blurred.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
		styles.Cursor.Color = lipgloss.Color("15")
		input.SetStyles(styles)
		input.Placeholder = placeholders[index]
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
		case "tab", "down", "enter":
			return false, f.move(1)
		case "shift+tab", "up":
			return false, f.move(-1)
		}
		if f.isCycle() {
			switch key.String() {
			case "left", "right", "space":
				value := f.inputs[f.index].Value()
				if f.kind == formEdit && f.index == 3 {
					value = strconv.FormatBool(value != "true")
				} else {
					value = nextProxyMode(value)
					if key.String() == "left" {
						value = nextProxyMode(value)
					}
				}
				f.inputs[f.index].SetValue(value)
			}
			return false, nil
		}
	}
	if f.index >= len(f.inputs) || f.isCycle() || f.isAction() {
		return false, nil
	}
	// Forward keys, bracketed paste, and clipboard paste results into the focused textinput.
	before := f.inputs[f.index].Value()
	updated, command := f.inputs[f.index].Update(message)
	f.inputs[f.index] = updated
	if f.index == 1 && before != updated.Value() {
		f.urlTouched = true
	}
	return false, command
}

func (f *formModel) isCycle() bool {
	return f.index < len(f.inputs) && (f.kind == formAdd && f.index == 2 || f.kind == formEdit && (f.index == 3 || f.index == 4))
}

func (f *formModel) isAction() bool {
	return f.kind == formEdit && f.index >= 5 && f.index < len(f.inputs)
}

func (f *formModel) reveal(raw string) {
	if f.urlTouched {
		return
	}
	f.urlBaseline = raw
	f.inputs[1].SetValue(raw)
}

func (f *formModel) move(delta int) tea.Cmd {
	if f.index < len(f.inputs) {
		f.inputs[f.index].Blur()
	}
	f.index = (f.index + delta + len(f.inputs) + 1) % (len(f.inputs) + 1)
	if f.index < len(f.inputs) && !f.isCycle() && !f.isAction() {
		return f.inputs[f.index].Focus()
	}
	return nil
}

func (f *formModel) valid() bool {
	f.errorText = ""
	f.validationErr = nil
	if strings.TrimSpace(f.inputs[0].Value()) == "" {
		return f.validationFailure("Name is required.", nil)
	}
	raw := strings.TrimSpace(f.inputs[1].Value())
	if f.kind == formAdd || f.urlTouched || raw != "" {
		if raw == "" {
			return f.validationFailure("URL is required.", nil)
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			if err == nil {
				err = fmt.Errorf("subscription URL %q must use HTTP or HTTPS and have a host", raw)
			}
			return f.validationFailure("Enter a valid HTTP or HTTPS URL.", err)
		}
	}
	if f.kind == formEdit {
		interval := strings.TrimSpace(f.inputs[2].Value())
		if interval != "" {
			d, err := time.ParseDuration(interval)
			if err != nil || d <= 0 {
				if err == nil {
					err = fmt.Errorf("subscription interval %q must be positive", interval)
				}
				return f.validationFailure("Enter a positive interval or leave it blank.", err)
			}
		}
	}
	return true
}

func (f *formModel) addRequest(operationID string, revision uint64) protocol.SubscriptionAddRequest {
	request := protocol.SubscriptionAddRequest{OperationID: operationID, Name: strings.TrimSpace(f.inputs[0].Value()), URL: strings.TrimSpace(f.inputs[1].Value()), ProxyMode: f.inputs[2].Value()}
	request.IfRevision = &revision
	return request
}

func (f *formModel) updateRequest(operationID string, revision uint64) protocol.SubscriptionUpdateRequest {
	name := strings.TrimSpace(f.inputs[0].Value())
	interval := strings.TrimSpace(f.inputs[2].Value())
	autoRefresh, _ := strconv.ParseBool(strings.TrimSpace(f.inputs[3].Value()))
	request := protocol.SubscriptionUpdateRequest{OperationID: operationID, IfRevision: &revision}
	if name != f.baseline.Name {
		request.Name = &name
	}
	if interval != f.baseline.Interval {
		request.Interval = &interval
	}
	if autoRefresh != f.baseline.AutoRefresh {
		request.AutoRefresh = &autoRefresh
	}
	mode := f.inputs[4].Value()
	if mode != f.baseline.ProxyMode {
		request.ProxyMode = &mode
	}
	if rawURL := strings.TrimSpace(f.inputs[1].Value()); f.urlTouched && rawURL != "" && rawURL != f.urlBaseline {
		request.URL = &rawURL
	}
	return request
}

func (f *formModel) validationFailure(summary string, cause error) bool {
	f.errorText = summary
	if cause == nil {
		cause = errors.New(summary)
	}
	f.validationErr = diagnostics.Wrap(protocol.APIError{Code: protocol.CodeInvalidArgument, Message: summary}, cause)
	return false
}
