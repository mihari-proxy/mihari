package update

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
)

const versionProbeLimit = 4096

var errVersionProbeLimit = errors.New("version query output exceeded limit")

// VersionRunner owns the bounded version child process and its cancellation.
type VersionRunner interface {
	RunVersion(context.Context, string, string) ([]byte, error)
}

// ExecVersionRunner queries an already trusted executable without a shell.
type ExecVersionRunner struct{}

// RunVersion runs self version in an isolated temporary environment.
func (ExecVersionRunner) RunVersion(ctx context.Context, executable, dir string) ([]byte, error) {
	if !filepath.IsAbs(executable) || !filepath.IsAbs(dir) {
		return nil, os.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	stdout := &versionProbeWriter{cancel: cancel}
	stderr := &versionProbeWriter{cancel: cancel}
	cmd := exec.CommandContext(ctx, executable, "self", "version", "--json")
	cmd.Dir = dir
	cmd.Env = []string{
		"MIHARI_DATA=" + filepath.Join(dir, "data"),
		"MIHARI_CONTROL_ENDPOINT=" + filepath.Join(dir, "control.sock"),
		"MIHARI_CONTROL_CREDENTIAL=" + filepath.Join(dir, "control.token"),
		"MIHARI_INSTALL_ROOT=" + filepath.Join(dir, "install"),
		"HOME=" + dir, "USERPROFILE=" + dir, "LOCALAPPDATA=" + dir, "APPDATA=" + dir,
		"XDG_STATE_HOME=" + dir, "XDG_CONFIG_HOME=" + dir, "XDG_CACHE_HOME=" + dir,
		"TMPDIR=" + dir, "TMP=" + dir, "TEMP=" + dir,
	}
	// Windows needs its OS location to initialize native libraries; no user secrets.
	for _, key := range []string{"SystemRoot", "WINDIR"} {
		if v := os.Getenv(key); v != "" {
			cmd.Env = append(cmd.Env, key+"="+v)
		}
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// Bound inherited pipes too, then Wait reaps the child before returning.
	cmd.WaitDelay = 100 * time.Millisecond
	err := cmd.Run()
	if stdout.overflow || stderr.overflow {
		return nil, errors.Join(errVersionProbeLimit, err)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	return stdout.buf.Bytes(), nil
}

type versionProbeWriter struct {
	buf      bytes.Buffer
	cancel   context.CancelFunc
	overflow bool
}

func (w *versionProbeWriter) Write(p []byte) (int, error) {
	if len(p) > versionProbeLimit-w.buf.Len() {
		w.overflow = true
		w.cancel()
		return 0, errVersionProbeLimit
	}
	return w.buf.Write(p)
}

// ObserveReplacementTarget binds a trusted version query to file observations.
func ObserveReplacementTarget(ctx context.Context, role, path string, runner VersionRunner) (ReplacementTarget, error) {
	return observeReplacementTarget(ctx, role, path, runner, platform.ObserveReplacementFile)
}
func observeReplacementTarget(ctx context.Context, role, path string, runner VersionRunner, observe func(context.Context, string) (platform.ReplacementFile, error)) (out ReplacementTarget, err error) {
	if err = ctx.Err(); err != nil {
		return out, err
	}
	before, err := observe(ctx, path)
	if err != nil {
		return out, err
	}
	out = ReplacementTarget{Roles: []string{role}, Path: before.Path, FileID: before.FileID, SHA256: before.SHA256, Exists: before.Exists}
	if !before.Exists || !before.MayExecute {
		return out, nil
	}
	dir, err := os.MkdirTemp("", "mihari-version-")
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(dir)) }()
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if runner == nil {
		runner = ExecVersionRunner{}
	}
	raw, queryErr := runner.RunVersion(probeCtx, before.Path, dir)
	if ctx.Err() != nil {
		return ReplacementTarget{}, ctx.Err()
	}
	after, err := observe(ctx, path)
	if err != nil {
		return ReplacementTarget{}, err
	}
	if before.Path != after.Path || before.FileID != after.FileID || before.SHA256 != after.SHA256 || !after.Exists || !after.MayExecute {
		return ReplacementTarget{}, replacementChanged()
	}
	if queryErr == nil {
		out.Version = decodeProbedVersion(raw)
	}
	return out, nil
}

func decodeProbedVersion(raw []byte) string {
	if len(raw) > versionProbeLimit {
		return ""
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return ""
	}
	seen := map[string]bool{}
	var schema, version string
	for d.More() {
		tok, err := d.Token()
		if err != nil {
			return ""
		}
		key, ok := tok.(string)
		if !ok || seen[key] {
			return ""
		}
		seen[key] = true
		var value string
		if err := d.Decode(&value); err != nil {
			return ""
		}
		switch key {
		case "schema":
			schema = value
		case "version":
			version = value
		default:
			return ""
		}
	}
	end, err := d.Token()
	if err != nil || end != json.Delim('}') {
		return ""
	}
	if _, err = d.Token(); err != io.EOF {
		return ""
	}
	if schema != "mihari/v1" || !seen["version"] {
		return ""
	}
	return normalizedReplacementVersion(version)
}
