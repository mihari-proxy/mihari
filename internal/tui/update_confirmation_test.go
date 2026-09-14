package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"github.com/mihari-proxy/mihari/internal/update"
)

func updateConfirmationModel(t *testing.T, width, height int, label string) Model {
	t.Helper()
	preview, err := update.NewReplacementPreview(update.ReplacementCandidate{Version: "v0.9.4-dev.2", Channel: "main", SHA256: strings.Repeat("a", 64)}, update.ReplacementSnapshot{Targets: []update.ReplacementTarget{
		{Path: "/fixture/binary", FileID: "binary", SHA256: strings.Repeat("b", 64), Roles: []string{"binary"}, Exists: true, UnrecognizedVersion: label},
		{Path: "/fixture/service", FileID: "service", SHA256: strings.Repeat("c", 64), Roles: []string{"service"}, Exists: true, UnrecognizedVersion: label},
	}})
	if err != nil {
		t.Fatal(err)
	}
	m, _, prepare := rootReplacementFixture(t, preview)
	m.width, m.height = width, height
	next, cmd := m.Update(prepare())
	m = next.(Model)
	if cmd == nil {
		t.Fatal("missing confirmation intent")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.modal == nil {
		t.Fatal("missing confirmation modal")
	}
	return m
}

func forUpdateConfirmationSizes(t *testing.T, check func(*testing.T, *Modal, int, int)) {
	t.Helper()
	for _, size := range []struct{ width, height int }{{72, 22}, {100, 28}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			m := updateConfirmationModel(t, size.width, size.height, strings.Repeat("abcdefgh", 16))
			check(t, m.modal, size.width, size.height)
		})
	}
}

func TestUpdateConfirmation_LayoutBounds(t *testing.T) {
	forUpdateConfirmationSizes(t, func(t *testing.T, modal *Modal, width, height int) {
		for n := 0; n < 60; n++ {
			view := modal.View(width, height)
			if lipgloss.Width(view) > width || lipgloss.Height(view) > height {
				t.Fatalf("dialog exceeds %dx%d:\n%s", width, height, ansi.Strip(view))
			}
			modal.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		}
	})
}

func TestUpdateConfirmation_FixedControls(t *testing.T) {
	forUpdateConfirmationSizes(t, func(t *testing.T, modal *Modal, width, height int) {
		for n := 0; n < 60; n++ {
			plain := ansi.Strip(modal.View(width, height))
			for _, fixed := range []string{ui.UpdateMihariTitle, ui.ConfirmLabel, ui.CancelLabel} {
				if !strings.Contains(plain, fixed) {
					t.Fatalf("missing fixed element %q", fixed)
				}
			}
			modal.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		}
	})
}

func TestUpdateConfirmation_LineScrollingRevealsContent(t *testing.T) {
	forUpdateConfirmationSizes(t, func(t *testing.T, modal *Modal, width, height int) {
		if !strings.Contains(ansi.Strip(modal.View(width, height)), ui.UpdateInstalledHeading) {
			t.Fatal("missing Installed section")
		}
		seenLabel, seenNote := false, false
		for n := 0; n < 60; n++ {
			compact := strings.Map(func(r rune) rune {
				if r == ' ' || r == '\n' || r == '│' || r == '\r' {
					return -1
				}
				return r
			}, ansi.Strip(modal.View(width, height)))
			seenLabel = seenLabel || strings.Contains(compact, strings.Repeat("abcdefgh", 16))
			seenNote = seenNote || strings.Contains(compact, "Ifinstallationfails,reopenMiharitoretry.")
			if modal.Update(tea.KeyPressMsg{Code: tea.KeyDown}) != ModalNone || modal.selected != 1 {
				t.Fatal("scroll changed confirmation")
			}
		}
		if !seenLabel || !seenNote {
			t.Fatal("scroll cannot reveal all content")
		}
		modal.View(width, height)
		previous := modal.scroll
		modal.Update(tea.KeyPressMsg{Code: tea.KeyUp})
		if modal.scroll != previous-1 {
			t.Fatal("up did not scroll one line")
		}
	})
}

func TestUpdateConfirmation_PageScrolling(t *testing.T) {
	forUpdateConfirmationSizes(t, func(t *testing.T, modal *Modal, width, height int) {
		initial := modal.View(width, height)
		modal.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
		if modal.View(width, height) == initial || modal.scroll == 0 {
			t.Fatal("page down did not reveal more content")
		}
		previous := modal.scroll
		modal.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
		if modal.scroll >= previous {
			t.Fatal("page up did not scroll towards the beginning")
		}
	})
}

func TestUpdateConfirmation_ResizeClampsScroll(t *testing.T) {
	m := updateConfirmationModel(t, 72, 22, strings.Repeat("abcdefgh", 16))
	m.modal.scroll = 10000
	m.modal.View(72, 22)
	if m.modal.scroll <= 0 || m.modal.scroll >= 10000 {
		t.Fatal("scroll did not clamp to the compact body")
	}
	m.modal.View(100, 60)
	if m.modal.scroll != 0 {
		t.Fatal("enlarged viewport did not reset the fully visible body")
	}
	m.modal.View(72, 22)
	if m.modal.scroll < 0 {
		t.Fatal("negative scroll after resize")
	}
}

func TestUpdateConfirmation_DefaultCancelAndExplicitConfirm(t *testing.T) {
	for _, confirm := range []bool{false, true} {
		m := updateConfirmationModel(t, 100, 28, "local")
		if m.modal.selected != 1 {
			t.Fatal("cancel is not default")
		}
		if confirm {
			m.modal.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
		}
		next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		m = next.(Model)
		if m.modal != nil || cmd == nil {
			t.Fatal("selection did not close and dispatch")
		}
		msg := cmd()
		_, executing := msg.(actionExecuteMsg)
		if executing != confirm {
			t.Fatal("wrong confirmation action")
		}
	}
}

func TestUpdateConfirmation_TooSmallCannotConfirm(t *testing.T) {
	m := updateConfirmationModel(t, 100, 28, "local")
	m.modal.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 15})
	m = next.(Model)
	if !strings.Contains(ansi.Strip(m.View().Content), ui.ResizeRequired) {
		t.Fatal("small window did not request resize")
	}
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	if cmd != nil || m.modal == nil {
		t.Fatal("invisible confirmation was activated")
	}
	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if next.(Model).modal != nil || cmd == nil {
		t.Fatal("small window cannot cancel")
	}
}

func TestUpdateConfirmation_OtherActionsKeepPlainConfirmation(t *testing.T) {
	m := NewModel()
	next, _ := m.Update(ui.ActionIntentMsg{Action: ui.ActionSwitchMihariChannel, Title: "Channel", Object: "dev", Impact: "Change channel", MihariUpdate: &ui.MihariUpdateConfirmation{TargetVersion: "v9.0.0"}})
	m = next.(Model)
	view := ansi.Strip(m.modal.View(72, 22))
	if strings.Contains(view, "v9.0.0") || !strings.Contains(view, "Change channel") {
		t.Fatal("update layout leaked into another action")
	}
}

func TestUpdateConfirmation_Golden(t *testing.T) {
	for _, scenario := range []string{"custom", "long", "missing", "upgrade", "downgrade"} {
		t.Run(scenario, func(t *testing.T) {
			width, height := 100, 28
			label := "dev-setup-6a47df6-dirty-20260913.2"
			switch scenario {
			case "long":
				width, height, label = 72, 22, strings.Repeat("abcdefgh", 16)
			case "missing":
				label = ""
			}
			m := updateConfirmationModel(t, width, height, label)
			if scenario == "upgrade" || scenario == "downgrade" {
				content := m.modal.updateContent
				content.Installed[0] = ui.MihariInstalledVersion{Role: "Binary", Version: "v0.9.3"}
				content.Risk, content.Compatibility = update.ReplacementNone, ""
				content.Installed[1] = content.Installed[0]
				content.Installed[1].Role = "Service"
				if scenario == "downgrade" {
					content.Installed[0].Version = "v0.9.5"
					content.Installed[1] = ui.MihariInstalledVersion{Role: "Service", Version: "Unknown[local]", Unknown: true}
					content.Risk, content.Compatibility = update.ReplacementDowngrade, ui.UpdateDowngradeCompatibility
				}
			}
			assertGoldenContent(t, "update_confirmation_"+scenario, strings.TrimRight(trimRenderPadding(normalizeRender(m.modal.View(width, height))), "\n")+"\n")
			if scenario == "long" || scenario == "downgrade" {
				m.modal.scroll = 10000
				assertGoldenContent(t, "update_confirmation_"+scenario+"_scrolled", strings.TrimRight(trimRenderPadding(normalizeRender(m.modal.View(width, height))), "\n")+"\n")
			}
		})
	}
}

func TestUpdateConfirmation_UsesSemanticThemeColors(t *testing.T) {
	theme := ui.DefaultTheme()
	content := ui.MihariUpdateConfirmation{
		Installed:     []ui.MihariInstalledVersion{{Role: "Binary", Version: "Unknown[local]", Unknown: true}},
		TargetVersion: "v0.9.4-dev.2", Risk: update.ReplacementUnknown, Compatibility: ui.UpdateUnknownBuild,
		AfterConfirmation: ui.UpdateAfterStandalone,
	}
	view := strings.Join(updateConfirmationLines(theme, content, 58), "\n")
	for _, styled := range []string{theme.Warning.Render("Unknown[local]"), theme.Warning.Bold(true).Render(ui.UpdateCompatibilityUnknownHeading), theme.Title.Render(content.TargetVersion), theme.Muted.Render("  Binary  ")} {
		if !strings.Contains(view, styled) || styled == ansi.Strip(styled) {
			t.Fatal("missing semantic color")
		}
	}
	content.Risk, content.Compatibility = update.ReplacementDowngrade, ui.UpdateDowngradeCompatibility
	view = strings.Join(updateConfirmationLines(theme, content, 58), "\n")
	if !strings.Contains(view, theme.Danger.Bold(true).Render(ui.UpdateDowngradeHeading)) {
		t.Fatal("downgrade does not use danger heading")
	}
}
