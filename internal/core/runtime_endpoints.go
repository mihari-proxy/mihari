package core

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/diagnostics"
	"go.yaml.in/yaml/v3"
)

// ReconcileRuntimeEndpoints applies saved endpoints before the stopped core is
// started. Authentication invariants remain strict; unrelated YAML is retained.
// The caller must be the sole business-file writer for this data root. This is
// startup assembly, before Manager.Run can accept or execute config mutations;
// the source recheck detects stale preparation, not arbitrary external writers.
// On Windows all daemons sharing a data root must use the same control endpoint;
// endpoint reservation does not serialize different endpoints on the same root.
func ReconcileRuntimeEndpoints(ctx context.Context, runner CommandRunner, binaryPath, dataDir, path string, settings config.Settings, reporter diagnostics.Reporter) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := EnsureRuntimeConfig(path, settings)
	if err == nil {
		return ctx.Err()
	}
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeDataFailure {
		return err
	}
	switch api.Details["field"] {
	case "mixed-port", "bind-address", "external-controller":
	default:
		return err
	}
	previous, readErr := os.ReadFile(path)
	if readErr != nil {
		return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "read runtime configuration"}, readErr)
	}
	if len(previous) > 32<<20 {
		return protocol.APIError{Code: protocol.CodeDataFailure, Message: "runtime configuration is too large"}
	}
	var document, managed map[string]any
	if decodeErr := yaml.Unmarshal(previous, &document); decodeErr != nil {
		return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "invalid runtime configuration"}, decodeErr)
	}
	bootstrap, buildErr := BootstrapConfig(settings)
	if buildErr != nil {
		return buildErr
	}
	if decodeErr := yaml.Unmarshal(bootstrap, &managed); decodeErr != nil {
		return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeInternal, Message: "decode managed runtime invariants"}, decodeErr)
	}
	for _, key := range []string{"mixed-port", "allow-lan", "bind-address", "external-controller", "secret"} {
		// Do not convert an unmanaged or malformed document into a managed one,
		// or hide a credential / LAN-policy mismatch behind an endpoint change.
		if reflect.TypeOf(document[key]) != reflect.TypeOf(managed[key]) ||
			(key == "allow-lan" || key == "secret") && !reflect.DeepEqual(document[key], managed[key]) {
			return protocol.APIError{Code: protocol.CodeDataFailure, Message: "runtime configuration violates a mihari-managed field", Details: map[string]any{"field": key}}
		}
	}
	for _, key := range []string{"mixed-port", "bind-address", "external-controller"} {
		document[key] = managed[key]
	}
	content, err := yaml.Marshal(document)
	if err != nil {
		return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "encode runtime endpoints"}, err)
	}
	candidate, err := os.CreateTemp(filepath.Dir(path), ".endpoints-*.yaml")
	if err != nil {
		return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "create runtime endpoint candidate"}, err)
	}
	defer func() {
		if cleanupErr := os.Remove(candidate.Name()); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
			if resultErr != nil {
				resultErr = errors.Join(resultErr, diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "remove runtime endpoint candidate"}, cleanupErr))
			} else if reporter != nil {
				// The runtime file has already committed. Candidate cleanup is a
				// warning, matching trusted startup, and must not degrade startup.
				reporter(ctx, diagnostics.Record{Component: "core", Event: "configuration.cleanup.failed", Level: slog.LevelWarn, Err: cleanupErr})
			}
		}
	}()
	_, writeErr := candidate.Write(content)
	if err := errors.Join(writeErr, candidate.Close()); err != nil {
		return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "write runtime endpoint candidate"}, err)
	}
	if info, err := os.Stat(binaryPath); err == nil {
		if !info.Mode().IsRegular() {
			return protocol.APIError{Code: protocol.CodeDataFailure, Message: "mihomo binary is not a regular file"}
		}
		if err := ValidateConfig(ctx, runner, binaryPath, dataDir, candidate.Name()); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "inspect mihomo binary"}, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "recheck runtime configuration"}, err)
	}
	if !bytes.Equal(current, previous) {
		return protocol.APIError{Code: protocol.CodeRevisionConflict, Message: "runtime configuration changed during endpoint preparation"}
	}
	// The core has not started, so publication needs no live controller reload.
	return commitRuntimeEndpoints(ctx, reporter, func() (config.CommitResult, error) {
		return config.AtomicWriteWithCommit(path, content, 0o600)
	})
}

func commitRuntimeEndpoints(ctx context.Context, reporter diagnostics.Reporter, commit func() (config.CommitResult, error)) error {
	result, err := commit()
	if err != nil {
		return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "persist runtime endpoints"}, err)
	}
	if !result.Committed {
		return diagnostics.Wrap(protocol.APIError{Code: protocol.CodeDataFailure, Message: "persist runtime endpoints"}, errors.New("runtime endpoints were not committed"))
	}
	if result.Warning != nil && reporter != nil {
		reporter(ctx, diagnostics.Record{Component: "core", Event: "configuration.commit.warning", Level: slog.LevelWarn, Err: result.Warning})
	}
	return nil
}
