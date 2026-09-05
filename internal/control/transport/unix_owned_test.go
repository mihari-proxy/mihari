//go:build linux || darwin

package transport

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
)

// A fixture scope substitutes only the separately tested trusted lease, never
// socket IO. /tmp is deliberately not represented as a trusted absolute root.
type fixtureEndpointScope struct {
	endpoint string
	err      error
	postErr  error
	calls    int
}

func (s *fixtureEndpointScope) WithEndpoint(ctx context.Context, _ platform.ResolvedLayout, fn func(string) error) error {
	s.calls++
	if s.err != nil {
		return s.err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.Join(fn(s.endpoint), s.postErr)
}
func TestListenOwned_LegacyProcess(t *testing.T) {
	if endpoint := os.Getenv("MIHARI_T04_LEGACY_SOCKET"); endpoint != "" {
		l, err := net.Listen("unix", endpoint)
		if err != nil {
			t.Fatal(err)
		}
		defer assertTransportClose(t, l.Close)
		fmt.Println("READY")
		var b [1]byte
		if _, err := os.Stdin.Read(b[:]); err != nil {
			t.Fatal(err)
		}
		return
	}
	layout, scope := ownedFixture(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestListenOwned_LegacyProcess$")
	cmd.Env = append(os.Environ(), "MIHARI_T04_LEGACY_SOCKET="+scope.endpoint)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := in.Write([]byte("x")); err != nil {
			t.Error(err)
		}
		assertTransportClose(t, in.Close)
		if err := cmd.Wait(); err != nil {
			t.Error(err)
		}
	}()
	scanner := bufio.NewScanner(out)
	if !scanner.Scan() || scanner.Text() != "READY" {
		t.Fatal("legacy listener did not become ready")
	}
	before, err := os.Lstat(scope.endpoint)
	if err != nil {
		t.Fatal(err)
	}
	_, err = listenOwned(ctx, layout, scope, probeSocket)
	if !errors.Is(err, ErrEndpointOccupied) {
		t.Fatalf("live legacy process not preserved: %v", err)
	}
	after, err := os.Lstat(scope.endpoint)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("legacy socket removed: %v", err)
	}
}
func ownedFixture(t *testing.T) (platform.ResolvedLayout, *fixtureEndpointScope) {
	t.Helper()
	dir, err := os.MkdirTemp("", "mi-t04-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	p := filepath.Join(dir, "e.sock")
	l := platform.ResolvedLayout{Mode: platform.PrivateMode, ControlEndpoint: p}
	return l, &fixtureEndpointScope{endpoint: p}
}
func staleFixture(t *testing.T, endpoint string) {
	t.Helper()
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: endpoint, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	l.SetUnlinkOnClose(false)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestListenOwned_BindsPrivateSocket(t *testing.T) {
	layout, scope := ownedFixture(t)
	l, err := listenOwned(context.Background(), layout, scope, probeSocket)
	if err != nil {
		t.Fatalf("bind valid owned endpoint: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(scope.endpoint); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("own socket was not cleaned: %v", err)
	}
}
func TestListenOwned_PrivateMode(t *testing.T) {
	layout, scope := ownedFixture(t)
	l, err := listenOwned(context.Background(), layout, scope, probeSocket)
	if err != nil {
		t.Fatal(err)
	}
	defer assertTransportClose(t, l.Close)
	st, err := os.Lstat(scope.endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0600 {
		t.Fatalf("socket mode=%04o, want0600", st.Mode().Perm())
	}
}
func TestListenOwned_ReplacementPreserved(t *testing.T) {
	layout, scope := ownedFixture(t)
	l, err := listenOwned(context.Background(), layout, scope, probeSocket)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(scope.endpoint); err != nil {
		t.Fatal(err)
	}
	staleFixture(t, scope.endpoint)
	before, _ := os.Lstat(scope.endpoint)
	if err := l.Close(); !errors.Is(err, platform.ErrIdentityMismatch) {
		t.Fatalf("replacement close=%v", err)
	}
	after, err := os.Lstat(scope.endpoint)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("replacement removed: %v", err)
	}
}
func TestListenOwned_ProbeReplacementPreserved(t *testing.T) {
	layout, scope := ownedFixture(t)
	staleFixture(t, scope.endpoint)
	var replacement os.FileInfo
	_, err := listenOwned(context.Background(), layout, scope, func(context.Context, string) error {
		if err := os.Rename(scope.endpoint, scope.endpoint+".old"); err != nil {
			t.Fatal(err)
		}
		staleFixture(t, scope.endpoint)
		replacement, _ = os.Lstat(scope.endpoint)
		return syscall.ECONNREFUSED
	})
	if !errors.Is(err, platform.ErrIdentityMismatch) {
		t.Fatalf("replacement probe=%v", err)
	}
	after, err := os.Lstat(scope.endpoint)
	if err != nil || !os.SameFile(replacement, after) {
		t.Fatalf("replacement removed: %v", err)
	}
}
func TestListenOwned_ProbeDecision(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		removable bool
	}{
		{"live", nil, false}, {"refused", syscall.ECONNREFUSED, true}, {"denied", syscall.EACCES, false}, {"timeout", context.DeadlineExceeded, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			layout, scope := ownedFixture(t)
			staleFixture(t, scope.endpoint)
			before, _ := os.Lstat(scope.endpoint)
			l, err := listenOwned(context.Background(), layout, scope, func(ctx context.Context, _ string) error {
				d, ok := ctx.Deadline()
				if !ok || time.Until(d) > 500*time.Millisecond {
					t.Error("probe has no 500ms bound")
				}
				return tc.err
			})
			if tc.removable {
				if err != nil {
					t.Fatalf("refused socket not replaced: %v", err)
				}
				defer assertTransportClose(t, l.Close)
			} else {
				if !errors.Is(err, ErrEndpointOccupied) {
					t.Fatalf("unsafe probe classification: %v", err)
				}
				after, statErr := os.Lstat(scope.endpoint)
				if statErr != nil || !os.SameFile(before, after) {
					t.Fatalf("occupied socket removed: %v", statErr)
				}
			}
		})
	}
}

func TestDialVerified_PeerBeforeIO(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(map[bool]string{false: "expected owner", true: "forged peer"}[reject], func(t *testing.T) {
			layout, scope := ownedFixture(t)
			l, err := net.Listen("unix", scope.endpoint)
			if err != nil {
				t.Fatal(err)
			}
			defer assertTransportClose(t, l.Close)
			done := make(chan int, 1)
			go func() {
				c, e := l.Accept()
				if e != nil {
					done <- -1
					return
				}
				defer assertTransportClose(t, c.Close)
				if err := c.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
					t.Error(err)
					done <- -1
					return
				}
				var b [1]byte
				n, _ := c.Read(b[:])
				done <- n
			}()
			expected := uint32(os.Geteuid())
			if reject {
				expected++
			}
			locator := platform.ControlLocator{Mode: platform.PrivateMode, Endpoint: layout.ControlEndpoint, ExpectedOwner: expected}
			discover := func(ctx context.Context, _ platform.ControlLocator, fn func(string) error) error {
				return fn(scope.endpoint)
			}
			c, err := dialVerified(context.Background(), locator, discover, peerOwner)
			if reject {
				if !errors.Is(err, os.ErrPermission) {
					t.Fatalf("forged peer error=%v", err)
				}
				if n := <-done; n != 0 {
					t.Fatalf("unverified peer received %d bytes", n)
				}
			} else {
				if err != nil {
					t.Fatalf("verified peer rejected: %v", err)
				}
				if _, err := c.Write([]byte("x")); err != nil {
					t.Fatal(err)
				}
				assertTransportClose(t, c.Close)
				if n := <-done; n != 1 {
					t.Fatalf("verified IO=%d", n)
				}
			}
		})
	}
}

func TestListenOwned_FailedPostcheckClosesOnce(t *testing.T) {
	layout, scope := ownedFixture(t)
	scope.postErr = platform.ErrIdentityMismatch
	l, err := listenOwned(context.Background(), layout, scope, probeSocket)
	if l != nil || !errors.Is(err, platform.ErrIdentityMismatch) {
		t.Fatalf("unproved listener returned: %v", err)
	}
	if _, err := os.Lstat(scope.endpoint); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed listener socket leaked: %v", err)
	}
	if scope.calls != 2 {
		t.Fatalf("cleanup scope count=%d, want bind+cleanup", scope.calls)
	}
}
func TestListenOwned_RejectsNonSocketAndAbsentLease(t *testing.T) {
	layout, scope := ownedFixture(t)
	if err := os.WriteFile(scope.endpoint, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := listenOwned(context.Background(), layout, scope, probeSocket); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("non-socket error=%v", err)
	}
	bytes, err := os.ReadFile(scope.endpoint)
	if err != nil || string(bytes) != "preserve" {
		t.Fatal("non-socket removed")
	}
	if _, err := ListenOwned(context.Background(), layout, nil); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("absent lease error=%v", err)
	}
}
func TestDialVerified_PostcheckClosesWithoutIO(t *testing.T) {
	_, scope := ownedFixture(t)
	l, err := net.Listen("unix", scope.endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer assertTransportClose(t, l.Close)
	done := make(chan int, 1)
	go func() {
		c, e := l.Accept()
		if e != nil {
			done <- -1
			return
		}
		defer assertTransportClose(t, c.Close)
		if err := c.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Error(err)
			done <- -1
			return
		}
		var b [1]byte
		n, _ := c.Read(b[:])
		done <- n
	}()
	locator := platform.ControlLocator{Mode: platform.PrivateMode, ExpectedOwner: uint32(os.Geteuid())}
	discover := func(ctx context.Context, _ platform.ControlLocator, fn func(string) error) error {
		return errors.Join(fn(scope.endpoint), platform.ErrIdentityMismatch)
	}
	c, err := dialVerified(context.Background(), locator, discover, peerOwner)
	if c != nil || !errors.Is(err, platform.ErrIdentityMismatch) {
		t.Fatalf("postcheck error=%v", err)
	}
	if n := <-done; n != 0 {
		t.Fatalf("unproven endpoint received %d bytes", n)
	}
}

func assertTransportClose(t *testing.T, close func() error) {
	t.Helper()
	if err := close(); err != nil {
		t.Error(err)
	}
}
