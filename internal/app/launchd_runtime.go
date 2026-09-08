package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/mihari-proxy/mihari/internal/service"
)

const launchdRuntimeSchema = "mihari.launchd-runtime/v1"

type launchdRuntimeGeneration struct {
	BootID string `json:"boot_id"`
	PID    int    `json:"pid"`
	Start  string `json:"start"`
}
type launchdRuntimeRecord struct {
	Schema     string                    `json:"schema"`
	Generation *launchdRuntimeGeneration `json:"generation"`
}
type launchdRuntimeStore interface {
	Read(context.Context) ([]byte, string, error)
	Publish(context.Context, string, []byte) error
}

func decodeLaunchdRuntime(reader io.Reader) (launchdRuntimeRecord, error) {
	var record launchdRuntimeRecord
	keys, err := decodeInstallationJSON(reader, 4096, &record)
	if err != nil || !keys["schema"] || !keys["generation"] || record.Schema != launchdRuntimeSchema {
		return launchdRuntimeRecord{}, unknownInstallState()
	}
	if record.Generation != nil && !validLaunchdRuntimeGeneration(*record.Generation) {
		return launchdRuntimeRecord{}, unknownInstallState()
	}
	return record, nil
}

func validLaunchdRuntimeGeneration(g launchdRuntimeGeneration) bool {
	_, err := service.RecordedLaunchdIdentity(g.BootID, g.PID, g.Start)
	return err == nil
}

// registerLaunchdRuntime runs only within the caller's retained startup gate
// and data/endpoint leases, after both startup-authority checks. A null record
// is not proof that an unregistered legacy service has exited.
func registerLaunchdRuntime(ctx context.Context, store launchdRuntimeStore, current launchdRuntimeGeneration, empty func(context.Context, launchdRuntimeGeneration) (bool, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if store == nil || !validLaunchdRuntimeGeneration(current) {
		return unknownInstallState()
	}
	raw, version, err := store.Read(ctx)
	if err != nil {
		return err
	}
	record, err := decodeLaunchdRuntime(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	if record.Generation != nil {
		if empty == nil {
			return unknownInstallState()
		}
		vacant, err := empty(ctx, *record.Generation)
		if err != nil {
			return err
		}
		if !vacant {
			return installBusy("previous launchd process group has not exited")
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err = json.Marshal(launchdRuntimeRecord{Schema: launchdRuntimeSchema, Generation: &current})
	if err != nil {
		return fmt.Errorf("encode launchd runtime generation: %w", err)
	}
	return store.Publish(ctx, version, raw)
}
