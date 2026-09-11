//go:build unix_security && (linux || darwin)

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/securitytest"
)

type abruptValidationChild struct{ ValidationChild }
type abruptValidationSession struct {
	ValidationSession
	request ValidationStart
}

func (c abruptValidationChild) Start(ctx context.Context, r ValidationStart) (ValidationSession, error) {
	s, err := c.ValidationChild.Start(ctx, r)
	if err != nil {
		return nil, err
	}
	// Retain actual startup identity in the owning test's bounded pipe before
	// any readiness assertion or abrupt exit can strand this child.
	if err := json.NewEncoder(os.Stdout).Encode(s.Identity()); err != nil {
		return nil, errors.Join(err, s.Stop(context.WithoutCancel(ctx)), s.Close(), s.WaitLockRelease(context.WithoutCancel(ctx)))
	}
	return &abruptValidationSession{s, r}, nil
}
func (s *abruptValidationSession) WaitReady(ctx context.Context) error {
	if err := s.ValidationSession.WaitReady(ctx); err != nil {
		return err
	}
	raw, err := s.request.Store.files.read(ctx, validationReadyPath(s.request.Journal.TransactionID), maxValidationReadyBytes)
	if err != nil {
		return err
	}
	ready, err := DecodeValidationReady(raw)
	if err != nil {
		return err
	}
	if err := MatchValidationReady(s.request.Journal, ready); err != nil {
		return err
	}
	if !SameProcessStart(ready.DaemonIdentity, s.Identity()) {
		return errValidationReady
	}
	if err := json.NewEncoder(os.Stdout).Encode(ready); err != nil {
		return err
	}
	// Actual process loss: deliberately no Go unwinding/deferred stop or Close.
	os.Exit(73)
	return nil
}
func TestSecurityLostInstallerChild(t *testing.T) {
	if os.Getenv("MIHARI_SECURITY_LOST_INSTALLER") != "1" {
		t.Fatal("isolated installer child only")
	}
	securitytest.Parent(t)
	var layout platform.ResolvedLayout
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 65537))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&layout); err != nil {
		t.Fatal(err)
	}
	f := securityFixtureForLayout(t, layout)
	s := f.open(t)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	req := InstallRequest{Schema: InstallRequestSchema, Operation: InstallOperationInstall, Channel: InstallChannelMain, Layout: InstallLayoutPrivate, Data: layout.Data.Root}
	old, err := s.tx.Service.InspectDefinition(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.prepare(context.Background(), req, old, &nativeReleaseInputs{binary: raw, resources: map[string][]byte{}}, false); err != nil {
		t.Fatal(err)
	}
	s.tx.Validation = abruptValidationChild{s.tx.Validation}
	if _, err := s.tx.ApplyLocked(context.Background(), s, req); err != nil {
		t.Fatal(err)
	}
	t.Fatal("installer loss boundary not reached")
}
func securityLostInstallerRecovery(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		t.Run(fmt.Sprintf("mismatched-record-%t", mismatch), func(t *testing.T) {
			f := newSecurityNativeInstall(t)
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(f.layout)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, "-test.run=^TestSecurityLostInstallerChild$", "-test.timeout=20s")
			command.Env = append(os.Environ(), "MIHARI_SECURITY_LOST_INSTALLER=1", "MIHARI_SECURITY_VALIDATION_FIXTURE=hold-after-eof")
			command.Stdin = bytes.NewReader(raw)
			var output, diagnostics securitytest.BoundedBuffer
			command.Stdout = &output
			command.Stderr = &diagnostics
			// Register independent process cleanup before launch. Its identity comes
			// from the owned installer's Start result, not mutable recovery bytes.
			t.Cleanup(func() {
				var owned ProcessStartIdentity
				if err := json.NewDecoder(bytes.NewReader(output.Bytes())).Decode(&owned); err != nil {
					if len(output.Bytes()) != 0 {
						t.Error("owned child identity proof", err)
					}
					return
				}
				if !validProcessStart(owned) {
					t.Error("invalid owned child startup identity")
					return
				}
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 12*time.Second)
				defer cleanupCancel()
				if err := (unixValidationRecovery{}).StopAndWait(cleanupCtx, owned); err != nil {
					t.Error("independent child cleanup", err)
				}
			})
			err = command.Run() // owns and joins the abruptly exiting installer process
			installerErr := err
			recovered := f.open(t)
			if present, err := recovered.loadState(ctx); err != nil || !present {
				t.Fatal("lost installer state", err)
			}
			journal, err := recovered.tx.Store.Load(ctx)
			if err != nil {
				t.Fatal(err)
			}
			recovered.tx.journal = journal
			launch, present, err := recovered.tx.Store.loadValidationLaunch(ctx, journal)
			if err != nil || !present || !validProcessStart(launch.Child) {
				t.Fatal("durable actual child launch absent", err)
			}
			t.Cleanup(func() {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 12*time.Second)
				defer cleanupCancel()
				if err := (unixValidationRecovery{}).StopAndWait(cleanupCtx, launch.Child); err != nil {
					t.Error("owned child cleanup", err)
				}
				if err := recovered.Close(); err != nil {
					t.Error(err)
				}
				if err := waitValidationLocks(cleanupCtx, journal); err != nil {
					t.Error("cleanup locks", err)
				}
			})
			var exited *exec.ExitError
			if !errors.As(installerErr, &exited) || exited.ExitCode() != 73 {
				t.Fatalf("installer did not abruptly exit at readiness: %v %s", installerErr, diagnostics.String())
			}
			var owned ProcessStartIdentity
			var ready ValidationReady
			proofs := json.NewDecoder(bytes.NewReader(output.Bytes()))
			if err := proofs.Decode(&owned); err != nil {
				t.Fatal("owned startup proof", err)
			}
			if err := proofs.Decode(&ready); err != nil {
				t.Fatal("authenticated ready proof", err)
			}
			if err := proofs.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
				t.Fatal("trailing installer proof")
			}
			if !SameProcessStart(owned, launch.Child) {
				t.Fatal("launch record differs from owned startup identity")
			}
			if !SameProcessStart(launch.Child, ready.DaemonIdentity) || launch.Parent.PID != command.Process.Pid {
				t.Fatal("ready/installer/kernel journal identity mismatch")
			}
			eof := filepath.Join(filepath.Dir(platform.SystemLayoutDefaults().BaseDir), "validation-eof-"+launch.TransactionID)
			for {
				proof, err := os.ReadFile(eof)
				if err == nil {
					if string(proof) != fmt.Sprint(launch.Child.PID) {
						t.Fatal("EOF child proof mismatch")
					}
					break
				}
				if !errors.Is(err, os.ErrNotExist) || ctx.Err() != nil {
					t.Fatal("actual parent EOF proof missing", err)
				}
				timer := time.NewTimer(10 * time.Millisecond)
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
					t.Fatal(ctx.Err())
				}
			}
			live, err := identifyValidationProcess(ctx, launch.Child.PID)
			if err != nil || !SameProcessStart(live, launch.Child) {
				t.Fatal("recorded child was not live at recovery entry", err)
			}
			lockCtx, lockCancel := context.WithTimeout(ctx, 100*time.Millisecond)
			held, lockErr := platform.AcquireDaemonLease(lockCtx, f.layout)
			lockCancel()
			if held != nil {
				if err := held.Close(); err != nil {
					t.Error(err)
				}
			}
			if lockErr == nil {
				t.Fatal("live child did not retain real data/endpoint leases")
			}
			if mismatch {
				wrong := launch
				wrong.Child.StartUnix++
				if err := recovered.tx.Store.saveValidationLaunch(ctx, wrong); err != nil {
					t.Fatal(err)
				}
				mismatchCtx, mismatchCancel := context.WithTimeout(ctx, 200*time.Millisecond)
				err := recovered.tx.RecoverLocked(mismatchCtx, recovered)
				mismatchCancel()
				if err == nil {
					t.Fatal("mismatched live child record accepted recovery")
				}
				live, err := identifyValidationProcess(ctx, launch.Child.PID)
				if err != nil || !SameProcessStart(live, launch.Child) {
					t.Fatal("mismatched identity killed actual child", err)
				}
				if err := recovered.tx.Store.saveValidationLaunch(ctx, launch); err != nil {
					t.Fatal(err)
				}
			}
			if err := recovered.tx.RecoverLocked(ctx, recovered); err != nil {
				t.Fatal("matching live-child recovery", err)
			}
			live, err = identifyValidationProcess(ctx, launch.Child.PID)
			if err != nil || SameProcessStart(live, launch.Child) {
				t.Fatal("matching recovery failed to reap child", err)
			}
			// Source rollback may retain its own installer data lease. Release
			// that owned lease before proving that the child's kernel locks are free.
			if recovered.dataLease != nil {
				if err := recovered.dataLease.Release(ctx); err != nil {
					t.Fatal(err)
				}
			}
			reacquired, err := platform.AcquireDaemonLease(ctx, f.layout)
			if err != nil {
				t.Fatal("recovery failed to release actual locks", err)
			}
			if err := reacquired.Close(); err != nil {
				t.Fatal(err)
			}
			if err := recovered.tx.RecoverLocked(ctx, recovered); err != nil {
				t.Fatal("live-child recovery not idempotent", err)
			}
		})
	}
}
