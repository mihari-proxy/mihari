package tui

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mihari-proxy/mihari/internal/app"
)

type uninstallRunFake struct {
	order    *[]string
	runCalls int
}

func (f *uninstallRunFake) Preview(context.Context) ([]app.UninstallTarget, error) { return nil, nil }

func (f *uninstallRunFake) Run(_ context.Context, progress func(string)) error {
	f.runCalls++
	*f.order = append(*f.order, "run")
	progress("Uninstalling Mihari service")
	return nil
}

func TestFinishCompleteUninstallRun_CleansUpBeforeRunningAndPrintsProgress(t *testing.T) {
	var order []string
	fake := &uninstallRunFake{order: &order}
	var output bytes.Buffer
	err := finishCompleteUninstallRun(context.Background(), NewModel(), nil, &output, func(tea.Model) error {
		order = append(order, "cleanup")
		return nil
	}, fake)
	if err != nil || fake.runCalls != 1 || !reflect.DeepEqual(order, []string{"cleanup", "run"}) || output.String() != "Uninstalling Mihari service\n" {
		t.Fatalf("err=%v calls=%d order=%v output=%q", err, fake.runCalls, order, output.String())
	}
}

func TestFinishCompleteUninstallRun_CleanupFailurePreventsMutation(t *testing.T) {
	var order []string
	fake := &uninstallRunFake{order: &order}
	cleanupErr := errors.New("close failed")
	err := finishCompleteUninstallRun(context.Background(), NewModel(), nil, nil, func(tea.Model) error {
		order = append(order, "cleanup")
		return cleanupErr
	}, fake)
	if !errors.Is(err, cleanupErr) || fake.runCalls != 0 || !reflect.DeepEqual(order, []string{"cleanup"}) {
		t.Fatalf("err=%v calls=%d order=%v", err, fake.runCalls, order)
	}
}
