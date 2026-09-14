package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mihari-proxy/mihari/internal/platform"
	systempage "github.com/mihari-proxy/mihari/internal/tui/pages/system"
	"github.com/mihari-proxy/mihari/internal/tui/ui"
	"github.com/mihari-proxy/mihari/internal/update"
)

type installedLabelRunner struct{ calls int }

func (r *installedLabelRunner) RunVersion(context.Context, string, string) ([]byte, error) {
	r.calls++
	return []byte(`{"schema":"mihari/v1","version":"dev-setup-local"}`), nil
}

type installedLabelUpdater struct{ preview update.ReplacementPreview }

func (u installedLabelUpdater) Check(context.Context, string, string) (update.CheckResult, error) {
	return update.CheckResult{Available: true, Latest: u.preview.Candidate.Version, Channel: "dev"}, nil
}
func (u installedLabelUpdater) Prepare(context.Context, string, string, string) (update.PreparedUpdate, error) {
	return update.PreparedUpdate{Available: true, Version: u.preview.Candidate.Version, Channel: "dev", Preview: u.preview}, nil
}
func (installedLabelUpdater) ApplyPrepared(context.Context, update.PreparedUpdate) (update.Result, error) {
	return update.Result{}, errors.New("confirmation must not apply the update")
}

func TestUpdateConfirmation_ObservedLabelReachesTUI(t *testing.T) {
	ctx := context.Background()
	binary := filepath.Join(t.TempDir(), platform.InstalledBinaryName())
	if err := os.WriteFile(binary, []byte("inert installed fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	file, err := platform.ObserveReplacementFile(ctx, binary)
	if err != nil {
		t.Fatal(err)
	}
	runner := &installedLabelRunner{}
	target, err := update.ObserveReplacementTarget(ctx, "binary", binary, runner)
	if err != nil {
		t.Fatal(err)
	}
	want := "Unknown"
	if file.MayExecute {
		want = "Unknown[dev-setup-local]"
		if runner.calls != 1 {
			t.Fatal("trusted file was not queried exactly once")
		}
	} else if runner.calls != 0 {
		t.Fatal("untrusted file was queried")
	}
	preview, err := update.NewReplacementPreview(update.ReplacementCandidate{Version: "v0.9.4-dev.2", Channel: "dev", SHA256: strings.Repeat("a", 64)}, update.ReplacementSnapshot{Targets: []update.ReplacementTarget{target}})
	if err != nil {
		t.Fatal(err)
	}
	p := systempage.New(nil, nil)
	p.SetSelfUpdater(installedLabelUpdater{preview}, "unrelated-process", binary, func() bool { return true })
	p.SetSelfUpdateChannel(func(context.Context) (string, error) { return "dev", nil })
	p.SetSize(180, 100)
	var route func(tea.Cmd)
	route = func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		switch msg := cmd().(type) {
		case tea.BatchMsg:
			for _, child := range msg {
				route(child)
			}
		case ui.PageResultMsg:
			_, next := p.Update(msg.Result)
			route(next)
		}
	}
	route(p.Load())
	for n := 0; n < 64; n++ {
		if strings.Contains(ansi.Strip(p.View()), ui.FocusMarker+ui.UpdateMihariLabel) {
			_, prepare := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if prepare == nil {
				t.Fatal("no preparation command")
			}
			result := prepare().(ui.PageResultMsg)
			_, confirm := p.Update(result.Result)
			if confirm == nil {
				t.Fatal("no confirmation command")
			}
			intent := confirm().(ui.ActionIntentMsg)
			if intent.MihariUpdate == nil || len(intent.MihariUpdate.Installed) != 1 || intent.MihariUpdate.Installed[0].Version != want || intent.MihariUpdate.Risk != update.ReplacementUnknown {
				t.Fatalf("wrong installed display: %+v", intent.MihariUpdate)
			}
			if intent.Execute == nil || intent.Cancel == nil || strings.Contains(intent.Object+intent.Impact, "dev-setup-local") {
				t.Fatal("display evidence changed the generic confirmation contract")
			}
			return
		}
		p.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	t.Fatal("update row not available")
}
