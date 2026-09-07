//go:build linux || darwin

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/mihari-proxy/mihari/internal/platform"
)

// Only root-private stages with their own captured identity are recoverable.
// Unknown or interrupted pre-marker stages are retained, never guessed by name.
func cleanupUnixBinaryStages(ctx context.Context, parent *platform.TrustedRoot) error {
	names, err := parent.ReadNames(ctx)
	if err != nil {
		return err
	}
	for _, name := range names {
		id, ok := strings.CutPrefix(name, ".mihari-update-")
		if !ok || !validTransactionID(id) {
			continue
		}
		if err := cleanupUnixBinaryStage(ctx, parent, name); err != nil {
			return err
		}
	}
	return nil
}

func cleanupUnixBinaryStage(ctx context.Context, parent *platform.TrustedRoot, name string) (err error) {
	stage, err := parent.OpenDir(ctx, name, platform.RootPolicy{Owner: 0, Mode: 0700})
	if err != nil {
		return nil
	} // Unproven directory: retain it.
	defer func() { err = errors.Join(err, stage.Close()) }()
	names, err := stage.ReadNames(ctx)
	if err != nil {
		return err
	}
	for _, entry := range names {
		if entry != "candidate" && entry != "identity.json" {
			return nil
		}
	}
	markerFile, markerID, err := stage.OpenFile(ctx, "identity.json", 0600)
	if err != nil {
		return nil
	} // No trusted marker, no cleanup authority.
	raw, readErr := io.ReadAll(io.LimitReader(markerFile, 4097))
	if err := errors.Join(readErr, markerFile.Close()); err != nil {
		return err
	}
	if len(raw) > 4096 {
		return nil
	}
	var marker binaryStageMarker
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&marker) != nil || decoder.Decode(new(any)) != io.EOF {
		return nil
	}
	_, stageID, _, _, err := stage.Snapshot(ctx)
	if err != nil {
		return err
	}
	candidate, candidateID, openErr := stage.OpenFile(ctx, "candidate", 0755)
	candidateHash, candidateKey := "absent", ""
	if openErr == nil {
		candidateKey = candidateID.Key()
		hash, readErr := hashReader(io.LimitReader(candidate, migrationBinaryMax+1), migrationBinaryMax+1)
		if err := errors.Join(readErr, candidate.Close()); err != nil {
			return err
		}
		candidateHash = hash
	} else if !errors.Is(openErr, os.ErrNotExist) {
		return nil
	}
	if !binaryStageMatches(marker, stageID, candidateKey, candidateHash) {
		return nil
	}
	if candidateKey != "" {
		if err := stage.RemoveFile(ctx, "candidate", 0755, candidateID); err != nil {
			return err
		}
	}
	if err := stage.RemoveFile(ctx, "identity.json", 0600, markerID); err != nil {
		return err
	}
	return parent.RemoveEmptyDir(ctx, name)
}
