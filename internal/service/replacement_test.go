package service

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestServiceReplacement_ChecksPrecedeMutation(t *testing.T) {
	for _, tc := range []struct {
		name              string
		status            StatusKind
		stopErr, stageErr bool
		want              string
	}{
		{"before stop", StatusRunning, true, false, "status,check-stop"},
		{"before stage running", StatusRunning, false, true, "status,check-stop,stop,check-stage,start"},
		{"before stage stopped", StatusStopped, false, true, "status,check-stop,stop,check-stage"},
		{"success", StatusRunning, false, false, "status,check-stop,stop,check-stage,stage,start"},
		{"missing", StatusNotInstalled, false, false, "status"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := []string{}
			f := &fakeController{status: tc.status, events: &events}
			m := New(Options{Executable: "fixture", NewController: func(RunFunc, string, []string) (Controller, error) { return f, nil }})
			m.stageBinary = func(string) (string, error) { events = append(events, "stage"); return "fixture", nil }
			_, err := m.UpdateInstalledBinaryChecked(context.Background(), ServiceReplacementChecks{
				BeforeStop: func(context.Context) error {
					events = append(events, "check-stop")
					if tc.stopErr {
						return errors.New("changed")
					}
					return nil
				},
				BeforeStage: func(context.Context) error {
					events = append(events, "check-stage")
					if tc.stageErr {
						return errors.New("changed")
					}
					return nil
				},
			})
			if (err != nil) != (tc.stopErr || tc.stageErr) {
				t.Fatalf("err=%v", err)
			}
			if got := strings.Join(events, ","); got != tc.want {
				t.Fatalf("events=%s want=%s", got, tc.want)
			}
		})
	}
}
