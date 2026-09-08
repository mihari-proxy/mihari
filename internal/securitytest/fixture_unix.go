//go:build unix_security && (linux || darwin)

// Package securitytest contains only the explicitly tagged native CI harness.
package securitytest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/mihari-proxy/mihari/internal/platform"
)

// Input is an immutable parent-created fixture description.
type Input struct {
	Defaults   platform.LayoutDefaults
	Layout     platform.ResolvedLayout
	Own, Other string
}

// Result is observed inside the child, then asserted by its root parent.
type Result struct {
	EUID          uint32 `json:"euid"`
	GID           uint32 `json:"gid"`
	Authenticated bool   `json:"authenticated"`
	PrivateDenied bool   `json:"private_denied"`
	OtherDenied   bool   `json:"other_denied"`
	OwnLog        bool   `json:"own_log"`
}

// Parent verifies the tagged default supplier before fixture IO.
func Parent(t *testing.T) (platform.ResolvedLayout, [2]uint32, [2]uint32) {
	t.Helper()
	defaults := platform.SystemLayoutDefaults()
	if os.Geteuid() != 0 || defaults.BaseDir != filepath.Join(os.Getenv("MIHARI_SECURITY_ROOT"), "system") {
		t.Fatal("validated root harness required")
	}
	layout, err := platform.ResolveLayout(platform.LayoutInput{}, defaults)
	if err != nil {
		t.Fatal(err)
	}
	var uids, gids [2]uint32
	for n, key := range []string{"MIHARI_SECURITY_UID_A", "MIHARI_SECURITY_UID_B"} {
		value, err := strconv.ParseUint(os.Getenv(key), 10, 32)
		if err != nil || value == 0 {
			t.Fatal("invalid actual UID")
		}
		account, err := user.LookupId(strconv.FormatUint(value, 10))
		if err != nil {
			t.Fatal(err)
		}
		gid, err := strconv.ParseUint(account.Gid, 10, 32)
		if err != nil {
			t.Fatal(err)
		}
		uids[n], gids[n] = uint32(value), uint32(gid)
	}
	if uids[0] == uids[1] {
		t.Fatal("UIDs must differ")
	}
	return layout, uids, gids
}

// UserRoots returns fixture U paths under the independently marked, readable
// result tree. The machine anchor remains search-only for ordinary users.
func UserRoots(t *testing.T) [2]string {
	t.Helper()
	// Loading the tagged defaults verifies both independently recorded roots.
	_ = platform.SystemLayoutDefaults()
	results := os.Getenv("MIHARI_SECURITY_RESULTS")
	if os.Geteuid() != 0 || !filepath.IsAbs(results) || filepath.Clean(results) != results {
		t.Fatal("validated results root required")
	}
	root, err := platform.OpenTrustedRoot(context.Background(), results, platform.RootPolicy{Owner: 0, Mode: 0755})
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	return [2]string{filepath.Join(results, "users", "a"), filepath.Join(results, "users", "b")}
}

// Run executes a joined child with empty supplementary groups and bounded pipes.
func Run(t *testing.T, ctx context.Context, name string, uid, gid uint32, input Input) Result {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(binary)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0755 || st.Uid != 0 || st.Nlink != 1 {
		t.Fatal("untrusted helper executable")
	}
	parent, err := platform.OpenTrustedParent(ctx, filepath.Dir(binary), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Close(); _ = write.Close() }()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, binary, "-test.run=^"+name+"$")
	cmd.Env = []string{"MIHARI_UNIX_SECURITY_CHILD=1", "PATH=/usr/bin:/bin"}
	cmd.ExtraFiles = []*os.File{read}
	cmd.Stdin = bytes.NewReader(raw)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: gid, Groups: []uint32{}}}
	var output, stderr BoundedBuffer
	cmd.Stdout = &output
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := read.Close(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	envelope := struct {
		Schema   string                  `json:"schema"`
		Defaults platform.LayoutDefaults `json:"defaults"`
	}{"mihari.unix-security-defaults/v1", input.Defaults}
	writeErr := json.NewEncoder(write).Encode(envelope)
	if err := errors.Join(writeErr, write.Close(), cmd.Wait()); err != nil {
		t.Fatalf("native helper: %v\n%s", err, ChildFailureSites(output.Bytes()))
	}
	var result Result
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.EUID != uid || result.GID != gid {
		t.Fatal("child observed wrong actual identity")
	}
	return result
}

// BoundedBuffer caps each child output stream at the fixture protocol limit.
type BoundedBuffer struct{ bytes.Buffer }

func (b *BoundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 65536 {
		return 0, errors.New("native child output limit")
	}
	return b.Buffer.Write(p)
}

// ReadInput consumes only parent-created stdin and FD3, never ownership markers.
func ReadInput(t *testing.T) Input {
	t.Helper()
	if os.Getenv("MIHARI_UNIX_SECURITY_CHILD") != "1" || os.Geteuid() == 0 {
		t.Fatal("explicit dropped-UID child required")
	}
	var input Input
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 65537))
	if err != nil || len(raw) > 65536 {
		t.Fatal("bounded fixture input required")
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	if platform.SystemLayoutDefaults() != input.Defaults {
		t.Fatal("FD3 defaults disagree")
	}
	return input
}

// Emit writes the observed result without testing-harness stdout contamination.
func Emit(t *testing.T, result Result) {
	t.Helper()
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}
