package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

type ownershipChild struct{ session *ownershipSession }

func (c ownershipChild) Start(ctx context.Context, r ValidationStart) (ValidationSession, error) {
	s, err := (&FakeValidationChild{}).Start(ctx, r)
	if err != nil {
		return nil, err
	}
	c.session.ValidationSession = s
	return c.session, nil
}

type ownershipSession struct {
	ValidationSession
	stopErr              error
	waitErr              error
	stops, closes, waits int
}

func (s *ownershipSession) Stop(ctx context.Context) error {
	s.stops++
	_ = s.ValidationSession.Stop(ctx)
	return s.stopErr
}
func (s *ownershipSession) Close() error { s.closes++; return s.ValidationSession.Close() }
func (s *ownershipSession) WaitLockRelease(ctx context.Context) error {
	s.waits++
	err := s.ValidationSession.WaitLockRelease(ctx)
	return errors.Join(err, s.waitErr)
}

func TestInstallValidation_StopFailureStillJoins(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	s := &ownershipSession{stopErr: errors.New("stop failed")}
	h.tx.Validation = ownershipChild{s}
	_, err := h.tx.Apply(context.Background(), h.req)
	if err == nil {
		t.Fatal("stop error lost")
	}
	if s.closes != 1 || s.waits != 1 {
		t.Fatalf("failed stop lost ownership: close=%d wait=%d", s.closes, s.waits)
	}
}

func TestInstallValidation_ReadyMustNameExactLaunchedChild(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	h.tx.Validation = &FakeValidationChild{WrongReadyChild: true}
	_, err := h.tx.Apply(context.Background(), h.req)
	if err == nil {
		t.Fatal("same PID with different start time authorized activation")
	}
}

func TestInstallValidation_PersistsChildBeforeHandshake(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	h.tx.ParentIdentity = ProcessStartIdentity{PID: 7, BootID: testBootID, StartUnix: 100}
	if _, err := h.tx.Apply(context.Background(), h.req); err != nil {
		t.Fatal(err)
	}
	j := h.loadedJournal(t, h.disk)
	launch, present, err := h.tx.Store.loadValidationLaunch(context.Background(), j)
	if err != nil || !present || !validProcessStart(launch.Child) {
		t.Fatalf("launched identity never recorded: %+v present=%v err=%v", launch, present, err)
	}
}

type failHandshakeWriter struct {
	ValidationLease
	failure error
}

func (w failHandshakeWriter) Write([]byte) (int, error) { return 0, w.failure }
func TestInstallValidation_HandshakeWriterFailureCancelsAndJoins(t *testing.T) {
	h := newInstallHarness(t, InstallDataCreate)
	failure := errors.New("handshake write failed")
	h.tx.NewPipe = func() (ValidationLease, ValidationLease) {
		p, c := newMemoryValidationPipes()
		return failHandshakeWriter{p, failure}, c
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := h.tx.Apply(ctx, h.req); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, failure) {
			t.Fatalf("writer error lost: %v", err)
		}
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("handshake writer failure left child waiting")
	}
}
