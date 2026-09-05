package platform

import (
	"golang.org/x/sys/unix"
)

type nativeDiscoveryBackend struct{}

func (nativeDiscoveryBackend) directory(p discoveryRef, name string) (discoveryRef, error) {
	fd, err := unix.Openat(p.fd, name, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	return discoveryRef{fd: fd, owned: true}, componentOpenError(err)
}

func (nativeDiscoveryBackend) root() (discoveryRef, error) {
	fd, err := unix.Open("/", unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	return discoveryRef{fd: fd, owned: true}, err
}
func (nativeDiscoveryBackend) child(p discoveryRef, name string) (discoveryRef, error) {
	fd, err := unix.Openat(p.fd, name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	return discoveryRef{fd: fd, owned: true}, componentOpenError(err)
}
func (nativeDiscoveryBackend) name(p discoveryRef, name string) (trustedNode, error) {
	return (nativeTrustedBackend{}).statAt(p.fd, name)
}
func (nativeDiscoveryBackend) alias(discoveryRef, string) (string, error) {
	return "", ErrUnsafeComponent
}
func (nativeDiscoveryBackend) close(r discoveryRef) error { return unix.Close(r.fd) }
func (nativeDiscoveryBackend) inspect(r discoveryRef, strict bool, owner uint32) (discoveryMetadata, error) {
	var st unix.Stat_t
	if err := unix.Fstat(r.fd, &st); err != nil {
		return discoveryMetadata{}, err
	}
	if err := (nativeTrustedBackend{}).checkFS(r.fd); err != nil {
		return discoveryMetadata{}, denied("discovery filesystem", err)
	}
	if err := discoveryFDACL(r.fd, strict, owner); err != nil {
		return discoveryMetadata{}, denied("discovery ACL", err)
	}
	id, err := linuxMountID(r.fd)
	if err != nil || id == 0 {
		return discoveryMetadata{}, denied("discovery mount identity", err)
	}
	n, err := (nativeTrustedBackend{}).stat(r.fd)
	if err != nil {
		return discoveryMetadata{}, err
	}
	return discoveryMetadata{node: n, mount: discoveryMount{id: id}, size: st.Size}, nil
}
func (b nativeDiscoveryBackend) read(p discoveryRef, name string, m discoveryMetadata) ([]byte, error) {
	fd, err := unix.Openat(p.fd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, componentOpenError(err)
	}
	return readDiscoveryFD(fd, m, func(fd int) (discoveryMetadata, error) { return b.inspect(discoveryRef{fd: fd}, true, m.node.uid) })
}
