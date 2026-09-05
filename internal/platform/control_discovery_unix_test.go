//go:build linux || darwin

package platform

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

type discoveryModel struct {
	nodes   map[string]discoveryMetadata
	closed  int
	denied  string
	visited []string
}

func (b *discoveryModel) directory(p discoveryRef, name string) (discoveryRef, error) {
	return b.child(p, name)
}

func newDiscoveryModel() *discoveryModel {
	b := &discoveryModel{nodes: map[string]discoveryMetadata{}}
	for i, p := range []string{"/", "/var", "/var/lib", "/var/lib/mihari"} {
		mode := uint32(0755)
		if i == 3 {
			mode = 0711
		}
		b.nodes[p] = discoveryMetadata{node: trustedNode{id: fileIdentity{dev: 1, ino: uint64(i + 1)}, mode: unix.S_IFDIR | mode, links: 2}, mount: discoveryMount{id: 1}}
	}
	b.nodes["/var/lib/mihari/control.sock"] = discoveryMetadata{node: trustedNode{id: fileIdentity{dev: 1, ino: 5}, mode: unix.S_IFSOCK | 0666, links: 1}, mount: discoveryMount{id: 1}}
	return b
}
func (b *discoveryModel) root() (discoveryRef, error) {
	return discoveryRef{tail: "/", owned: true}, nil
}
func (b *discoveryModel) child(p discoveryRef, n string) (discoveryRef, error) {
	return discoveryRef{tail: strings.TrimSuffix(p.tail, "/") + "/" + n, owned: true}, nil
}
func (b *discoveryModel) inspect(r discoveryRef, strict bool, owner uint32) (discoveryMetadata, error) {
	b.visited = append(b.visited, r.tail)
	if r.tail == b.denied {
		return discoveryMetadata{}, os.ErrPermission
	}
	m, ok := b.nodes[r.tail]
	if !ok {
		return m, os.ErrNotExist
	}
	return m, nil
}
func (b *discoveryModel) name(p discoveryRef, n string) (trustedNode, error) {
	m, ok := b.nodes[strings.TrimSuffix(p.tail, "/")+"/"+n]
	if !ok {
		return trustedNode{}, os.ErrNotExist
	}
	return m.node, nil
}
func (b *discoveryModel) alias(discoveryRef, string) (string, error) { return "", ErrUnsafeComponent }
func (b *discoveryModel) close(discoveryRef) error                   { b.closed++; return nil }
func (b *discoveryModel) read(discoveryRef, string, discoveryMetadata) ([]byte, error) {
	return []byte(strings.Repeat("a", 64)), nil
}
func discoverySystemLocator() ControlLocator {
	return ControlLocator{Mode: SystemMode, BaseDir: "/var/lib/mihari", Endpoint: "/var/lib/mihari/control.sock", Credential: "/var/lib/mihari/control.token"}
}
func TestControlDiscovery_ProtectedChain(t *testing.T) {
	b := newDiscoveryModel()
	l := discoverySystemLocator()
	d, err := openControlDiscovery(context.Background(), l, l.Endpoint, 1000, linuxTestDefaults(""), b)
	if err != nil {
		t.Fatalf("valid protected discovery: %v", err)
	}
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	if b.closed == 0 {
		t.Fatal("reader handles leaked")
	}
}
func TestControlDiscovery_DoesNotDescendUnsafePrefix(t *testing.T) {
	b := newDiscoveryModel()
	b.denied = "/var"
	l := discoverySystemLocator()
	_, err := openControlDiscovery(context.Background(), l, l.Endpoint, 1000, linuxTestDefaults(""), b)
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("unsafe ancestor accepted: %v", err)
	}
	for _, p := range b.visited {
		if p == "/var/lib" {
			t.Fatal("descended unsafe prefix")
		}
	}
}

func TestControlDiscovery_MountAndAuthority(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*discoveryModel)
		ok   bool
	}{
		{"allowed pre-anchor crossing", func(b *discoveryModel) {
			for _, p := range []string{"/var/lib", "/var/lib/mihari", "/var/lib/mihari/control.sock"} {
				m := b.nodes[p]
				m.mount.id = 2
				m.node.id.dev = 2
				b.nodes[p] = m
			}
		}, true},
		{"final nested mount", func(b *discoveryModel) {
			m := b.nodes["/var/lib/mihari/control.sock"]
			m.mount.id = 2
			b.nodes["/var/lib/mihari/control.sock"] = m
		}, false},
		{"writable prefix", func(b *discoveryModel) { m := b.nodes["/var"]; m.node.mode |= 0020; b.nodes["/var"] = m }, false},
		{"wrong owner", func(b *discoveryModel) {
			m := b.nodes["/var/lib/mihari/control.sock"]
			m.node.uid = 1000
			b.nodes["/var/lib/mihari/control.sock"] = m
		}, false},
		{"symlink endpoint", func(b *discoveryModel) {
			m := b.nodes["/var/lib/mihari/control.sock"]
			m.node.mode = unix.S_IFLNK | 0777
			b.nodes["/var/lib/mihari/control.sock"] = m
		}, false},
		{"wrong base mode", func(b *discoveryModel) {
			m := b.nodes["/var/lib/mihari"]
			m.node.mode = unix.S_IFDIR | 0755
			b.nodes["/var/lib/mihari"] = m
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newDiscoveryModel()
			tc.edit(b)
			l := discoverySystemLocator()
			d, err := openControlDiscovery(context.Background(), l, l.Endpoint, 1000, linuxTestDefaults(""), b)
			if d != nil {
				defer assertDiscoveryClose(t, d.Close)
			}
			if (err == nil) != tc.ok {
				t.Fatalf("discovery err=%v, want success=%v", err, tc.ok)
			}
		})
	}
}
func TestControlDiscovery_PostcheckDetectsChange(t *testing.T) {
	b := newDiscoveryModel()
	l := discoverySystemLocator()
	d, err := openControlDiscovery(context.Background(), l, l.Endpoint, 1000, linuxTestDefaults(""), b)
	if err != nil {
		t.Fatal(err)
	}
	defer assertDiscoveryClose(t, d.Close)
	m := b.nodes["/var/lib/mihari"]
	m.node.id.ino++
	b.nodes["/var/lib/mihari"] = m
	if !errors.Is(d.verify(context.Background()), ErrIdentityMismatch) {
		t.Fatal("changed ancestor passed postcheck")
	}
}
func TestControlDiscovery_PrivateExternalAnchor(t *testing.T) {
	b := newDiscoveryModel()
	b.nodes["/private"] = discoveryMetadata{node: trustedNode{id: fileIdentity{dev: 1, ino: 50}, mode: unix.S_IFDIR | 0700, links: 2}, mount: discoveryMount{id: 1}}
	l := ControlLocator{Mode: PrivateMode, BaseDir: "/private", Endpoint: "/var/lib/mihari/control.sock", Credential: "/private/c"}
	// A root private instance must reject the machine socket independently of P.
	if _, err := openControlDiscovery(context.Background(), l, l.Endpoint, 0, linuxTestDefaults(""), b); err == nil {
		t.Fatal("private instance discovered default endpoint")
	}
	l.Endpoint = "/private/e"
	b.nodes[l.Endpoint] = discoveryMetadata{node: trustedNode{id: fileIdentity{dev: 1, ino: 51}, mode: unix.S_IFSOCK | 0600, links: 1}, mount: discoveryMount{id: 1}}
	d, err := openControlDiscovery(context.Background(), l, l.Endpoint, 0, linuxTestDefaults(""), b)
	if err != nil {
		t.Fatalf("independent root P refused: %v", err)
	}
	defer assertDiscoveryClose(t, d.Close)
}

func TestControlDiscovery_RejectsBeforeIO(t *testing.T) {
	for _, name := range []string{"cancelled", "invalid intermediate"} {
		t.Run(name, func(t *testing.T) {
			b := newDiscoveryModel()
			l := discoverySystemLocator()
			ctx := context.Background()
			if name == "cancelled" {
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			} else {
				l.Credential = "/bad\x00prefix/c"
			}
			if _, err := openControlDiscovery(ctx, l, l.Endpoint, 1000, linuxTestDefaults(""), b); err == nil {
				t.Fatal("invalid request accepted")
			}
			if len(b.visited) != 0 {
				t.Fatalf("performed metadata IO before validating request: %v", b.visited)
			}
		})
	}
}

func assertDiscoveryClose(t *testing.T, close func() error) {
	t.Helper()
	if err := close(); err != nil {
		t.Error(err)
	}
}

func TestControlDiscovery_PrivateAuxiliaryDefaultMode(t *testing.T) {
	for _, name := range []string{"safe non0711", "unsafe mode", "same inode"} {
		t.Run(name, func(t *testing.T) {
			b := newDiscoveryModel()
			m := b.nodes["/var/lib/mihari"]
			m.node.mode = unix.S_IFDIR | 0700
			if name == "unsafe mode" {
				m.node.mode |= 0020
			}
			b.nodes["/var/lib/mihari"] = m
			private := discoveryMetadata{node: trustedNode{id: fileIdentity{dev: 1, ino: 80}, mode: unix.S_IFDIR | 0700, links: 2}, mount: discoveryMount{id: 1}}
			if name == "same inode" {
				private.node.id = m.node.id
			}
			b.nodes["/portable"] = private
			b.nodes["/portable/e"] = discoveryMetadata{node: trustedNode{id: fileIdentity{dev: 1, ino: 81}, mode: unix.S_IFSOCK | 0600, links: 1}, mount: discoveryMount{id: 1}}
			l := ControlLocator{Mode: PrivateMode, BaseDir: "/portable", Endpoint: "/portable/e", Credential: "/portable/c"}
			d, err := openControlDiscovery(context.Background(), l, l.Endpoint, 0, linuxTestDefaults(""), b)
			if d != nil {
				defer assertDiscoveryClose(t, d.Close)
			}
			if name == "safe non0711" {
				if err != nil {
					t.Fatalf("unrelated protected default B blocked root P: %v", err)
				}
			} else if err == nil {
				t.Fatal("unsafe/overlapping auxiliary B accepted")
			}
		})
	}
}
