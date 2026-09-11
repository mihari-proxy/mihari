//go:build unix_security && (linux || darwin)

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/platform"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 4 && os.Args[1] == "daemon" && os.Args[2] == "--install-validation" {
		err := RunInheritedValidation(context.Background(), os.Args[3], func(ctx context.Context, layout platform.ResolvedLayout, _ *platform.OwnedDaemonLease, ready func(bool) error) error {
			if err := os.WriteFile(filepath.Join(filepath.Dir(platform.SystemLayoutDefaults().BaseDir), "validation-proof-"+os.Args[3]), []byte("authenticated"), 0600); err != nil {
				return err
			}
			if err := ready(false); err != nil {
				return err
			}
			<-ctx.Done()
			if platform.SecurityValidationFixture() == "hold-after-eof" {
				// Explicit non-cooperating fault: authentication and ready were real.
				// Ordinary EOF behavior remains the immediately-returning branch below.
				proof := filepath.Join(filepath.Dir(platform.SystemLayoutDefaults().BaseDir), "validation-eof-"+os.Args[3])
				if err := os.WriteFile(proof, []byte(fmt.Sprint(os.Getpid())), 0600); err != nil {
					return err
				}
				timer := time.NewTimer(45 * time.Second)
				defer timer.Stop()
				<-timer.C
				return errors.New("bounded held validation fixture expired")
			}
			return nil
		})
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type nativeValidationObserver struct {
	child    ValidationChild
	mode     string
	identity ProcessStartIdentity
	session  ValidationSession
	parent   ValidationLease
}

func (o *nativeValidationObserver) Start(ctx context.Context, r ValidationStart) (ValidationSession, error) {
	switch o.mode {
	case "wrong-parent":
		r.Handshake.ParentIdentity.StartUnix++
	case "wrong-binary":
		r.Journal.CandidateHash = strings.Repeat("e", 64)
	case "no-lease":
		r.Child = nil
	}
	session, err := o.child.Start(ctx, r)
	if session != nil {
		o.identity = session.Identity()
		o.session = session
		o.parent = r.Lease
	}
	if err == nil && o.mode == "parent-eof" {
		return &nativeValidationEOFSession{ValidationSession: session, parent: r.Lease}, nil
	}
	return session, err
}

type nativeValidationEOFSession struct {
	ValidationSession
	parent ValidationLease
}

func (s *nativeValidationEOFSession) WaitReady(ctx context.Context) error {
	if err := s.ValidationSession.WaitReady(ctx); err != nil {
		return err
	}
	return errors.Join(s.parent.Close(), s.WaitLockRelease(ctx), errValidationCanceled)
}

type corruptNativeHandshake struct{ ValidationLease }

func (p corruptNativeHandshake) Write(raw []byte) (int, error) {
	var message validationPipeMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		return 0, err
	}
	if message.Nonce != "" {
		message.Nonce = strings.Repeat("00", 32)
	}
	encoded, err := json.Marshal(message)
	if err != nil {
		return 0, err
	}
	encoded = append(encoded, '\n')
	if _, err := p.ValidationLease.Write(encoded); err != nil {
		return 0, err
	}
	return len(raw), nil
}

func TestSecurityValidationProcess(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	var plan []string
	for _, mode := range []string{"success", "wrong-parent", "wrong-binary", "wrong-nonce", "no-lease", "parent-eof"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newSecurityNativeInstall(t)
			session := fixture.open(t)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			request := InstallRequest{Schema: InstallRequestSchema, Operation: InstallOperationInstall, Channel: InstallChannelMain, Layout: InstallLayoutPrivate, Data: fixture.layout.Data.Root}
			old, err := session.tx.Service.InspectDefinition(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := session.prepare(ctx, request, old, &nativeReleaseInputs{binary: bytes, resources: map[string][]byte{}}, false); err != nil {
				t.Fatal(err)
			}
			observer := &nativeValidationObserver{child: session.tx.Validation, mode: mode}
			session.tx.Validation = observer
			if mode == "wrong-nonce" {
				create := session.tx.NewPipe
				session.tx.NewPipe = func() (ValidationLease, ValidationLease) { p, c := create(); return corruptNativeHandshake{p}, c }
			}
			_, applyErr := session.tx.ApplyLocked(ctx, session, request)
			if (applyErr == nil) != (mode == "success") {
				t.Fatalf("native validation %s result %v", mode, applyErr)
			}
			if mode == "success" {
				plan = actionKinds(session.tx.journal)
			}
			proof := filepath.Join(filepath.Dir(platform.SystemLayoutDefaults().BaseDir), "validation-proof-"+session.tx.journal.TransactionID)
			_, proofErr := os.Lstat(proof)
			authorized := mode == "success" || mode == "parent-eof"
			if authorized && proofErr != nil {
				t.Fatal("real authenticated validation callback absent", proofErr)
			}
			if !authorized && !errors.Is(proofErr, os.ErrNotExist) {
				t.Fatal("unauthorized validation entered data callback")
			}
			if observer.identity.PID > 0 {
				if live, err := identifyValidationProcess(context.Background(), observer.identity.PID); err != nil || SameProcessStart(observer.identity, live) {
					t.Fatal("validation child still exists after joined return")
				}
			} else if authorized || mode == "wrong-nonce" {
				t.Fatal("test did not launch actual validation process")
			}
			if err := session.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("actual-missing-descriptor", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		child := exec.CommandContext(ctx, binary, "daemon", "--install-validation", testTxnID)
		child.Env = []string{"PATH=/usr/bin:/bin"}
		if err := child.Run(); err == nil {
			t.Fatal("real child accepted missing inherited lease")
		}
		if ctx.Err() != nil || child.ProcessState == nil || child.ProcessState.ExitCode() != 1 {
			t.Fatal("missing-lease refusal did not exit and join")
		}
	})
	t.Run("installer-loss-live-recovery", securityLostInstallerRecovery)

	for index, kind := range plan {
		if kind != JournalActionValidationStart && kind != JournalActionValidationStop {
			continue
		}
		for _, point := range []string{"before-intent", "after-intent", "after-effect", "after-done"} {
			t.Run(fmt.Sprintf("crash-%s-%s", kind, point), func(t *testing.T) {
				f := newSecurityNativeInstall(t)
				s := f.open(t)
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				req := InstallRequest{Schema: InstallRequestSchema, Operation: InstallOperationInstall, Channel: InstallChannelMain, Layout: InstallLayoutPrivate, Data: f.layout.Data.Root}
				old, err := s.tx.Service.InspectDefinition(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.prepare(ctx, req, old, &nativeReleaseInputs{binary: bytes, resources: map[string][]byte{}}, false); err != nil {
					t.Fatal(err)
				}
				observer := &nativeValidationObserver{child: s.tx.Validation}
				s.tx.Validation = observer
				s.tx.crash = &installCrashSpec{index: index + 1, point: point}
				if !catchInstallCrash(func() {
					if _, err := s.tx.ApplyLocked(ctx, s, req); err != nil {
						t.Fatal("failed before process action fault", err)
					}
				}) {
					t.Fatal("process action fault not reached")
				}
				mustLaunch := kind == JournalActionValidationStop || point == "after-effect" || point == "after-done"
				if mustLaunch && observer.identity.PID <= 0 {
					t.Fatal("process fault row lacked actual child")
				}
				if observer.identity.PID > 0 {
					if live, err := identifyValidationProcess(ctx, observer.identity.PID); err != nil || SameProcessStart(observer.identity, live) {
						t.Fatal("process fault leaked child")
					}
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				r := f.open(t)
				if present, err := r.loadState(ctx); err != nil || !present {
					t.Fatal(err)
				}
				// ConfigureUnixValidation installed the real identity-aware recovery adapter.
				if err := r.tx.RecoverLocked(ctx, r); err != nil {
					t.Fatal("native process recovery", err)
				}
				if err := r.tx.RecoverLocked(ctx, r); err != nil {
					t.Fatal("native repeated process recovery", err)
				}
				if err := r.Close(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}

}
