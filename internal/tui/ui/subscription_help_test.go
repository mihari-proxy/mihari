package ui

import (
	"github.com/charmbracelet/x/ansi"
	"strings"
	"testing"
)

func TestSubscriptionHelp_FormDoesNotAdvertiseListActions(t *testing.T) {
	help := RenderHelp(PageSubscriptions, ModeForm)
	for _, forbidden := range []string{"refresh all", "activate", "quit outside", "jump to a rail"} {
		if strings.Contains(help, forbidden) {
			t.Fatalf("form help advertises unavailable %q", forbidden)
		}
	}
}

func TestSubscriptionHelp_SaveAndCycleBindings(t *testing.T) {
	for _, tc := range []struct{ mode, want, banned string }{
		{ModeSubscriptionInput, "next field", "refresh all"},
		{ModeSubscriptionSubmit, "save changes", "next or save"},
		{ModeSubscriptionCycle, "cycle draft value", "refresh all"},
		{ModeSubscriptionSaving, "Saving", "Esc cancel"},
		{ModeSubscriptionUnknown, "confirm before submitting again", "activate"},
		{ModeSubscriptionWaiting, "does not cancel the save", "next or save"},
		{ModeSubscriptionConfirm, "Cancel is selected by default", "refresh all"},
	} {
		help := RenderHelp(PageSubscriptions, tc.mode)
		if !strings.Contains(help, tc.want) || strings.Contains(help, tc.banned) {
			t.Fatalf("incorrect help for %s", tc.mode)
		}
	}
	if footer := RenderFooter(PageSubscriptions, ModeSubscriptionSaving, FooterOpt{}); footer != "Saving..." {
		t.Fatal("saving footer offered input")
	}
	for _, mode := range []string{ModeSubscriptionInput, ModeSubscriptionSubmit, ModeSubscriptionUnknown, ModeSubscriptionWaiting, ModeSubscriptionConfirm, ModeSubscriptionCycle} {
		if RenderFooter(PageSubscriptions, mode, FooterOpt{}) == "" {
			t.Fatal("missing overlay footer")
		}
	}
}

func TestPadCell_CenterRespectsDisplayWidth(t *testing.T) {
	for _, value := range []string{"●", "\x1b[32m●\x1b[0m"} {
		got := PadCell(value, 5, AlignCenter)
		if ansi.Strip(got) != "  ●  " || ansi.StringWidth(got) != 5 {
			t.Fatalf("InUse marker was not centered: %q (%d)", ansi.Strip(got), ansi.StringWidth(got))
		}
	}
	if got := PadCell("界", 5, AlignCenter); got != " 界  " {
		t.Fatal("wide cell centering lost columns")
	}
}
