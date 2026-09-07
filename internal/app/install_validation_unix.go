//go:build linux || darwin

package app

import (
	"errors"
	"net"
	"os"
	"sync"
	"syscall"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

const validationLeaseFD = 3

type unixPipeLease struct {
	conn     *net.UnixConn
	file     *os.File
	once     sync.Once
	closeErr error
}

func (l *unixPipeLease) Read(b []byte) (int, error) {
	if l == nil || l.conn == nil {
		return 0, errMissingValidationPipe
	}
	return l.conn.Read(b)
}

func (l *unixPipeLease) Write(b []byte) (int, error) {
	if l == nil || l.conn == nil {
		return 0, errMissingValidationPipe
	}
	return l.conn.Write(b)
}

func (l *unixPipeLease) Close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		var errs []error
		if l.conn != nil {
			errs = append(errs, l.conn.Close())
		}
		if l.file != nil {
			errs = append(errs, l.file.Close())
		}
		l.closeErr = errors.Join(errs...)
	})
	return l.closeErr
}

func newUnixValidationPipes() (parent, child ValidationLease, err error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, nil, protocol.APIError{Code: protocol.CodeDataFailure, Message: "create validation pipe"}
	}
	syscall.CloseOnExec(fds[0])
	syscall.CloseOnExec(fds[1])
	parentFile := os.NewFile(uintptr(fds[0]), "validation-parent")
	childFile := os.NewFile(uintptr(fds[1]), "validation-child")
	parentConn, err := net.FileConn(parentFile)
	if err != nil {
		_ = parentFile.Close()
		_ = childFile.Close()
		return nil, nil, err
	}
	childConn, err := net.FileConn(childFile)
	if err != nil {
		_ = parentConn.Close()
		_ = parentFile.Close()
		_ = childFile.Close()
		return nil, nil, err
	}
	parentUnix, _ := parentConn.(*net.UnixConn)
	childUnix, _ := childConn.(*net.UnixConn)
	return &unixPipeLease{conn: parentUnix, file: parentFile}, &unixPipeLease{conn: childUnix, file: childFile}, nil
}

// InheritedValidationLease returns the anonymous pipe inherited as extra file 3.
func InheritedValidationLease() (ValidationLease, error) {
	file := os.NewFile(validationLeaseFD, "validation-lease")
	if file == nil {
		return nil, errMissingValidationPipe
	}
	if _, err := file.Stat(); err != nil {
		_ = file.Close()
		return nil, errMissingValidationPipe
	}
	conn, err := net.FileConn(file)
	if err != nil {
		_ = file.Close()
		return nil, errMissingValidationPipe
	}
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		_ = conn.Close()
		_ = file.Close()
		return nil, errMissingValidationPipe
	}
	return &unixPipeLease{conn: unixConn, file: file}, nil
}
