//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

type modelTrustedBackend struct {
	trustedBackend
	nodes         map[int]trustedNode
	closed        []int
	aclErr, fsErr error
	replaced      int
	missing       bool
	created       int
	strictACL     bool
	alias         string
	prepared      int
}

func newTrustedModel() *modelTrustedBackend {
	b := &modelTrustedBackend{nodes: map[int]trustedNode{}}
	for i := 0; i < 4; i++ {
		b.nodes[i] = trustedNode{id: fileIdentity{dev: 1, ino: uint64(i + 1)}, mode: unix.S_IFDIR | 0755, links: 2}
	}
	n := b.nodes[3]
	n.mode = unix.S_IFDIR | 0700
	b.nodes[3] = n
	return b
}
func (b *modelTrustedBackend) openRoot() (int, error) { return 0, nil }
func (b *modelTrustedBackend) openDir(fd int, name string, below bool) (int, error) {
	if b.alias != "" && fd == 0 && name != "private" {
		return -1, ErrUnsafeComponent
	}
	if b.missing && fd == 2 {
		return -1, unix.ENOENT
	}
	return fd + 1, nil
}
func (b *modelTrustedBackend) stat(fd int) (trustedNode, error) { return b.nodes[fd], nil }
func (b *modelTrustedBackend) statAt(fd int, name string) (trustedNode, error) {
	if b.alias != "" && fd == 0 && name != "private" {
		return trustedNode{id: fileIdentity{dev: 1, ino: 99}, mode: unix.S_IFLNK | 0777, links: 1}, nil
	}
	n := b.nodes[fd+1]
	if b.replaced == fd+1 {
		n.id.ino++
	}
	return n, nil
}
func (b *modelTrustedBackend) checkFS(int) error { return b.fsErr }
func (b *modelTrustedBackend) checkACL(fd int, strict bool, owner uint32) error {
	if strict && b.strictACL && fd == 2 {
		return os.ErrPermission
	}
	return b.aclErr
}
func (b *modelTrustedBackend) close(fd int) error { b.closed = append(b.closed, fd); return nil }
func (b *modelTrustedBackend) mkdir(int, string, uint32) error {
	b.created++
	b.missing = false
	return nil
}
func (b *modelTrustedBackend) prepareDir(int, uint32, uint32) error { b.prepared++; return nil }
func (b *modelTrustedBackend) sync(int) error                       { return nil }
func (b *modelTrustedBackend) dup(fd int) (int, error)              { return fd, nil }
func (b *modelTrustedBackend) osAlias(fd int, name string) (string, error) {
	if trustedDarwinAlias(name, b.alias) {
		return b.alias, nil
	}
	return "", ErrUnsafeComponent
}

func TestTrustedRoot_PositiveRetainsDescriptors(t *testing.T) {
	b := newTrustedModel()
	r, err := openTrustedRoot(context.Background(), "/var/lib/mihari", RootPolicy{Mode: 0700}, b)
	if err != nil {
		t.Fatalf("positive control failed: %v", err)
	}
	if len(b.closed) != 0 {
		t.Fatal("capability discarded validated descriptors")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if len(b.closed) != 4 {
		t.Fatalf("closed %d descriptors, want 4", len(b.closed))
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if len(b.closed) != 4 {
		t.Fatal("duplicate close")
	}
}

func TestTrustedRoot_ModelDarwinAliasTraversal(t *testing.T) {
	for _, name := range []string{"var", "tmp", "etc"} {
		t.Run(name, func(t *testing.T) {
			b := newTrustedModel()
			b.alias = "private/" + name
			r, err := openTrustedRoot(context.Background(), "/"+name+"/mihari", RootPolicy{Mode: 0700}, b)
			if err != nil {
				t.Fatalf("positive alias traversal: %v", err)
			}
			defer assertTestClose(t, r.Close)
			b.alias = "/private/" + name
			if err := r.verify(); !errors.Is(err, ErrIdentityMismatch) {
				t.Fatalf("changed alias target not detected: %v", err)
			}
		})
	}
}

func TestCreationParent_TrustedRootCreateFinalOnly(t *testing.T) {
	b := newTrustedModel()
	b.missing = true
	r, err := openTrustedRoot(context.Background(), "/var/lib/mihari", RootPolicy{Mode: 0700, AllowCreate: true}, b)
	if err != nil {
		t.Fatalf("positive create: %v", err)
	}
	defer assertTestClose(t, r.Close)
	if b.created != 1 {
		t.Fatal("did not exclusively create final component")
	}
}

func TestCreationParent_ChangedOwnerNeverRepaired(t *testing.T) {
	b := newTrustedModel()
	b.missing = true
	n := b.nodes[3]
	n.uid = 4242
	b.nodes[3] = n
	r, err := openTrustedRoot(context.Background(), "/var/lib/mihari", RootPolicy{Mode: 0700, AllowCreate: true}, b)
	if r != nil {
		assertTestClose(t, r.Close)
		t.Fatal("accepted substituted owner")
	}
	if err == nil || b.prepared != 0 {
		t.Fatalf("foreign inode was repaired: %d %v", b.prepared, err)
	}
}

func TestCreationParent_TraversalACLDoesNotAuthorizeCreate(t *testing.T) {
	b := newTrustedModel()
	b.strictACL = true
	r, err := openTrustedRoot(context.Background(), "/var/lib/mihari", RootPolicy{Mode: 0700}, b)
	if err != nil {
		t.Fatalf("positive existing-root traversal: %v", err)
	}
	assertTestClose(t, r.Close)
	b = newTrustedModel()
	b.strictACL = true
	b.missing = true
	r, err = openTrustedRoot(context.Background(), "/var/lib/mihari", RootPolicy{Mode: 0700, AllowCreate: true}, b)
	if r != nil {
		assertTestClose(t, r.Close)
		t.Fatal("created below ACL parent")
	}
	if err == nil || b.created != 0 {
		t.Fatal("creation occurred before ACL rejection")
	}
}

func TestTrustedRoot_RejectsUntrustedAuthority(t *testing.T) {
	for _, change := range []string{"foreign ancestor", "writable ancestor", "foreign application", "wide application", "ACL", "filesystem", "replaced ancestor", "replaced final"} {
		t.Run(change, func(t *testing.T) {
			b := newTrustedModel()
			switch change {
			case "foreign ancestor":
				n := b.nodes[1]
				n.uid = 4242
				b.nodes[1] = n
			case "writable ancestor":
				n := b.nodes[2]
				n.mode |= 0020
				b.nodes[2] = n
			case "foreign application":
				n := b.nodes[3]
				n.uid = 4242
				b.nodes[3] = n
			case "wide application":
				n := b.nodes[3]
				n.mode |= 0040
				b.nodes[3] = n
			case "ACL":
				b.aclErr = os.ErrPermission
			case "filesystem":
				b.fsErr = os.ErrPermission
			case "replaced ancestor":
				b.replaced = 1
			case "replaced final":
				b.replaced = 3
			}
			r, err := openTrustedRoot(context.Background(), "/var/lib/mihari", RootPolicy{Mode: 0700}, b)
			if r != nil {
				assertTestClose(t, r.Close)
				t.Fatal("accepted untrusted authority")
			}
			if !errors.Is(err, os.ErrPermission) || errors.Is(err, ErrUnsafeComponent) {
				t.Fatalf("wrong rejection classification: %v", err)
			}
		})
	}
}

func TestTrustedRoot_RejectsInvalidPathBeforeIO(t *testing.T) {
	for _, path := range []string{"", "/", "relative", "/var/../lib", "/var/./lib", "/var//lib", "/var/lib/", "/var/\x00"} {
		t.Run(fmt.Sprintf("%q", path), func(t *testing.T) {
			b := newTrustedModel()
			r, err := openTrustedRoot(context.Background(), path, RootPolicy{Mode: 0700}, b)
			if r != nil {
				assertTestClose(t, r.Close)
			}
			if err == nil || len(b.closed) != 0 {
				t.Fatalf("invalid path reached IO: %v", err)
			}
		})
	}
}

func TestTrustedRoot_CanceledAcquisitionPerformsNoIO(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := newTrustedModel()
	r, err := openTrustedRoot(ctx, "/var/lib/mihari", RootPolicy{Mode: 0700, AllowCreate: true}, b)
	if r != nil || !errors.Is(err, context.Canceled) || len(b.closed) != 0 || b.created != 0 {
		t.Fatalf("canceled acquisition reached IO: %v", err)
	}
}

func TestTrustedRoot_RejectsChangedComponent(t *testing.T) {
	for _, index := range []int{1, 2, 3} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			b := newTrustedModel()
			n := b.nodes[index]
			n.mode = unix.S_IFLNK | 0777
			b.nodes[index] = n
			r, err := openTrustedRoot(context.Background(), "/var/lib/mihari", RootPolicy{Mode: 0700}, b)
			if r != nil {
				assertTestClose(t, r.Close)
				t.Fatal("accepted changed symlink component")
			}
			if !errors.Is(err, ErrUnsafeComponent) {
				t.Fatalf("wrong rejection stage: %v", err)
			}
		})
	}
}
