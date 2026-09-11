package app

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestLaunchdRuntime_RecordRejectsMissingAndAmbiguousGeneration(t *testing.T) {
	for _, raw := range []string{
		`{}`, `{"schema":"mihari.launchd-runtime/v1"}`,
		`{"schema":"mihari.launchd-runtime/v1","generation":{}}`,
		`{"schema":"mihari.launchd-runtime/v1","generation":{"boot_id":"boot-a","pid":1,"start":"10.000001"}}`,
		`{"schema":"mihari.launchd-runtime/v1","generation":{"boot_id":"boot-a","pid":123,"start":"10.1"}}`,
		`{"schema":"mihari.launchd-runtime/v1","generation":null,"generation":null}`,
		`{"schema":"mihari.launchd-runtime/v1","generation":null,"path":"/other"}`,
		`{"schema":"mihari.launchd-runtime/v1","generation":null} {}`,
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := decodeLaunchdRuntime(bytes.NewBufferString(raw)); err == nil {
				t.Fatal("ambiguous runtime generation accepted")
			}
		})
	}
}

func TestLaunchdRuntime_RecordReadsNullAndCanonicalGeneration(t *testing.T) {
	empty, err := decodeLaunchdRuntime(bytes.NewBufferString(`{"schema":"mihari.launchd-runtime/v1","generation":null}`))
	if err != nil || empty.Generation != nil {
		t.Fatalf("empty generation: %v", err)
	}
	got, err := decodeLaunchdRuntime(bytes.NewBufferString(`{"schema":"mihari.launchd-runtime/v1","generation":{"boot_id":"boot-a","pid":123,"start":"10.000001"}}`))
	if err != nil || got.Generation == nil || got.Generation.PID != 123 || got.Generation.Start != "10.000001" {
		t.Fatalf("valid generation: %v", err)
	}
}

type memoryLaunchdRuntime struct {
	raw      []byte
	writeErr error
}

func (s *memoryLaunchdRuntime) Read(context.Context) ([]byte, string, error) {
	return append([]byte(nil), s.raw...), "captured-version", nil
}
func (s *memoryLaunchdRuntime) Publish(_ context.Context, version string, raw []byte) error {
	if version != "captured-version" {
		return errors.New("version changed")
	}
	if s.writeErr != nil {
		return s.writeErr
	}
	s.raw = append([]byte(nil), raw...)
	return nil
}

func TestLaunchdRuntime_RegistrationDoesNotOverwriteUnclearedTree(t *testing.T) {
	old := []byte(`{"schema":"mihari.launchd-runtime/v1","generation":{"boot_id":"boot-a","pid":123,"start":"10.000001"}}`)
	for _, observation := range []struct {
		name  string
		empty bool
		err   error
	}{
		{"remaining child", false, nil}, {"observation denied", false, errors.New("denied")},
	} {
		t.Run(observation.name, func(t *testing.T) {
			store := &memoryLaunchdRuntime{raw: append([]byte(nil), old...)}
			err := registerLaunchdRuntime(context.Background(), store, launchdRuntimeGeneration{BootID: "boot-a", PID: 456, Start: "20.000002"}, func(context.Context, launchdRuntimeGeneration) (bool, error) {
				return observation.empty, observation.err
			})
			if err == nil || !bytes.Equal(store.raw, old) {
				t.Fatal("uncleared or unknown tree was replaced")
			}
		})
	}
}

func TestLaunchdRuntime_RegistrationPublishesAfterOldTreeExit(t *testing.T) {
	store := &memoryLaunchdRuntime{raw: []byte(`{"schema":"mihari.launchd-runtime/v1","generation":{"boot_id":"boot-a","pid":123,"start":"10.000001"}}`)}
	err := registerLaunchdRuntime(context.Background(), store, launchdRuntimeGeneration{BootID: "boot-b", PID: 456, Start: "20.000002"}, func(_ context.Context, got launchdRuntimeGeneration) (bool, error) {
		if got.PID != 123 || got.BootID != "boot-a" {
			t.Fatal("old generation identity was lost")
		}
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema":"mihari.launchd-runtime/v1","generation":{"boot_id":"boot-b","pid":456,"start":"20.000002"}}`
	if string(store.raw) != want {
		t.Fatal("new generation was not durably registered")
	}
}

func TestLaunchdRuntime_RegistrationPropagatesDurabilityFailure(t *testing.T) {
	want := errors.New("sync failed")
	store := &memoryLaunchdRuntime{raw: []byte(`{"schema":"mihari.launchd-runtime/v1","generation":null}`), writeErr: want}
	err := registerLaunchdRuntime(context.Background(), store, launchdRuntimeGeneration{BootID: "boot-a", PID: 456, Start: "20.000002"}, nil)
	if !errors.Is(err, want) {
		t.Fatalf("durability error lost: %v", err)
	}
}

func TestLaunchdRuntime_CanceledRegistrationLeavesRecordUntouched(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	old := []byte(`{"schema":"mihari.launchd-runtime/v1","generation":null}`)
	store := &memoryLaunchdRuntime{raw: append([]byte(nil), old...)}
	if err := registerLaunchdRuntime(ctx, store, launchdRuntimeGeneration{BootID: "boot-a", PID: 456, Start: "20.000002"}, nil); !errors.Is(err, context.Canceled) || !bytes.Equal(old, store.raw) {
		t.Fatal("canceled registration changed runtime state")
	}
}
