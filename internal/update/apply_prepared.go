package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func (u SelfUpdater) observeReplacement(ctx context.Context, path string) (ReplacementSnapshot, error) {
	if u.ObserveTargets != nil {
		return u.ObserveTargets(ctx, path)
	}
	target, err := ObserveReplacementTarget(ctx, "binary", path, nil)
	return ReplacementSnapshot{Targets: []ReplacementTarget{target}}, err
}

// ApplyPrepared validates and installs the fixed candidate without querying releases.
func (u SelfUpdater) ApplyPrepared(ctx context.Context, p PreparedUpdate) (result Result, err error) {
	result = Result{Version: p.Version, Ahead: p.Ahead, Channel: p.Channel}
	if !p.Available {
		return result, nil
	}
	if err = verifyPreparedCandidate(ctx, p, io.Discard); err != nil {
		return result, err
	}
	snapshot, err := u.observeReplacement(ctx, p.TargetPath)
	if err != nil {
		return result, err
	}
	candidate := ReplacementCandidate{Version: p.Version, SHA256: p.SHA256, Channel: p.Channel}
	if err = RecheckReplacement(p.Preview, candidate, snapshot); err != nil {
		return result, err
	}
	// Derive risk from the bound observations rather than trusting a mutable field.
	p.Preview, err = NewReplacementPreview(candidate, snapshot)
	if err != nil {
		return result, err
	}
	if err = ValidateReplacementConsent(p.Preview, p.Consent); err != nil {
		return result, err
	}
	// Stage beside the target so platform rename never crosses filesystems.
	stage, err := os.MkdirTemp(filepath.Dir(p.TargetPath), ".mihari-update-")
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(stage)) }()
	path := filepath.Join(stage, "candidate")
	if u.openCandidate == nil {
		u.openCandidate = func(path string) (io.WriteCloser, error) {
			return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		}
	}
	file, err := u.openCandidateFile(path)
	if err != nil {
		return result, err
	}
	copyErr := verifyPreparedCandidate(ctx, p, file)
	if err = errors.Join(copyErr, file.Close()); err != nil {
		return result, err
	}
	if err = os.Chmod(path, 0755); err != nil {
		return result, err
	}
	staged := p
	staged.CandidatePath = path
	if err = verifyPreparedCandidate(ctx, staged, io.Discard); err != nil {
		return result, err
	}
	snapshot, err = u.observeReplacement(ctx, p.TargetPath)
	if err != nil {
		return result, err
	}
	if err = RecheckReplacement(p.Preview, candidate, snapshot); err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if err = replaceBinary(path, p.TargetPath); err != nil {
		return result, protocol.APIError{Code: protocol.CodeDataFailure, Message: "replace mihari binary"}
	}
	result.Updated = true
	if u.AfterReplacePrepared != nil {
		return result, u.AfterReplacePrepared(ctx, p)
	}
	if u.AfterReplace != nil {
		return result, u.AfterReplace(ctx, p.Version)
	}
	return result, nil
}

func verifyPreparedCandidate(ctx context.Context, p PreparedUpdate, out io.Writer) (err error) {
	if err = ctx.Err(); err != nil {
		return err
	}
	if _, ok := parseCanonicalTag(p.Version); !ok {
		return replacementChanged()
	}
	file, err := os.Open(p.CandidatePath)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, hash), io.LimitReader(file, maxSelfBinarySize+1))
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if n > maxSelfBinarySize || hex.EncodeToString(hash.Sum(nil)) != p.SHA256 {
		return replacementChanged()
	}
	return nil
}
