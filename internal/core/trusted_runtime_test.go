package core

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"os"
	"sync"
	"testing"
)

type memoryConfigs struct {
	s             *memoryStore
	sequence      int
	writes        int
	failWriteAt   int
	failWriteFrom int
	writeErrors   map[int]error
	pathErrors    map[int]error
	readErrors    map[int]error
}

func (f *memoryConfigs) prepare(ctx context.Context, b []byte) (*ConfigCapability, error) {
	f.sequence++
	tx := fmt.Sprintf("%032x", f.sequence)
	if e := f.s.Save(ctx, CandidateReceipt, tx, b); e != nil {
		return nil, e
	}
	v, e := f.s.open(ctx, CandidateReceipt, tx)
	return &ConfigCapability{file: v, root: f.s.location(), hash: sha256.Sum256(b)}, e
}
func (f *memoryConfigs) read(ctx context.Context) ([]byte, error) {
	if e := f.readErrors[f.writes]; e != nil {
		delete(f.readErrors, f.writes)
		return nil, e
	}
	return f.s.Load(ctx, ProvenanceRole("runtime_config"), "")
}
func (f *memoryConfigs) write(ctx context.Context, b []byte) (*ConfigCapability, error) {
	f.writes++
	if e := f.s.Save(ctx, ProvenanceRole("runtime_config"), "", b); e != nil {
		return nil, e
	}
	if f.writes == f.failWriteAt || f.failWriteFrom > 0 && f.writes >= f.failWriteFrom {
		return nil, errors.New("sync failed after replacement")
	}
	if e := f.writeErrors[f.writes]; e != nil {
		return nil, e
	}
	cap, e := f.bind(ctx, sha256.Sum256(b))
	if e == nil && f.pathErrors[f.writes] != nil {
		cap.file = &configFaultFile{verifiedFile: cap.file, err: f.pathErrors[f.writes]}
	}
	return cap, e
}
func (f *memoryConfigs) bind(ctx context.Context, hash [32]byte) (*ConfigCapability, error) {
	b, e := f.read(ctx)
	if e != nil {
		return nil, e
	}
	if sha256.Sum256(b) != hash {
		return nil, errors.New("config hash mismatch")
	}
	v, e := f.s.open(ctx, ProvenanceRole("runtime_config"), "")
	return &ConfigCapability{file: v, root: f.s.location(), hash: hash, committed: true}, e
}
func (f *memoryConfigs) remove(ctx context.Context, c *ConfigCapability) error {
	v := c.file.(*memoryVerifiedFile)
	if _, e := v.verify(ctx); e != nil {
		if errors.Is(e, os.ErrNotExist) {
			return nil
		}
		return e
	}
	return f.s.Apply(ctx, ProvenanceMutation{Role: v.role, Transaction: v.tx, Expected: v.observed})
}
func TestConfigCapability_ValidatesSelectedCandidate(t *testing.T) {
	s := newMemoryStore()
	seedInstalledReceipt(t, s, "trusted")
	files := &memoryConfigs{s: s}
	x := &recordedExecutor{store: s}
	trusted := &TrustedExecution{store: s, files: files, executor: x}
	first, e := trusted.PrepareGenerated(context.Background(), []byte("first config"))
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if closeErr := first.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	second, e := trusted.PrepareGenerated(context.Background(), []byte("second config"))
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if closeErr := second.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	if x.configHashes[0] != digest([]byte("first config")) || x.configHashes[1] != digest([]byte("second config")) {
		t.Fatal("executor did not validate the selected config bytes")
	}
	if len(x.commands) != 2 || x.commands[0].Config == x.commands[1].Config {
		t.Fatal("two config candidates were interchanged")
	}
	if _, e = trusted.Publish(context.Background(), first, second.Hash()); e == nil || files.writes != 0 {
		t.Fatal("mismatched candidate hash published")
	}
	cap, e := trusted.Publish(context.Background(), first, first.Hash())
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if closeErr := cap.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	published, e := files.read(context.Background())
	if e != nil || sha256.Sum256(published) != first.Hash() {
		t.Fatal("published different bytes than validated")
	}
}
func TestTrustedRuntime_GreenBootstrapUsesGeneratedBytes(t *testing.T) {
	s := newMemoryStore()
	files := &memoryConfigs{s: s}
	trusted := &TrustedExecution{store: s, files: files}
	settings := config.Defaults()
	settings.ControllerSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	content, err := subscription.Generate(subscription.Document{"proxies": []any{}}, nil, settings)
	if err != nil {
		t.Fatal(err)
	}
	if e := trusted.InitializeConfig(context.Background(), content); e != nil {
		t.Fatalf("green generated bootstrap failed: %v", e)
	}
	cap, e := trusted.CommittedConfig(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if closeErr := cap.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	if files.writes != 1 {
		t.Fatal("missing committed bootstrap")
	}
}

func TestTrustedRuntime_PublicationFailureRestoresPreviousHash(t *testing.T) {
	s := newMemoryStore()
	seedInstalledReceipt(t, s, "trusted")
	files := &memoryConfigs{s: s}
	trusted := &TrustedExecution{store: s, files: files, executor: &recordedExecutor{}}
	old, e := trusted.publishContent(context.Background(), []byte("old config"))
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = old.Close() }()
	candidate, e := trusted.PrepareGenerated(context.Background(), []byte("new config"))
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = candidate.Close() }()
	files.failWriteAt = 2
	if _, e = trusted.Publish(context.Background(), candidate, candidate.Hash()); e == nil {
		t.Fatal("publication error ignored")
	}
	actual, e := files.read(context.Background())
	if e != nil || string(actual) != "old config" {
		t.Fatal("failed publication retained new config")
	}
	cap, e := trusted.CommittedConfig(context.Background())
	if e != nil {
		t.Fatal("old config hash was not rebound")
	}
	if e = cap.Close(); e != nil {
		t.Fatal(e)
	}
}

type blockedConfigExecutor struct{ entered, release chan struct{} }

func (x blockedConfigExecutor) Execute(context.Context, CoreCommand) ([]byte, error) {
	close(x.entered)
	<-x.release
	return nil, nil
}
func TestTrustedRuntime_PublicationWaitsForExecution(t *testing.T) {
	for _, purpose := range []string{"start", "candidate-validation"} {
		t.Run(purpose, func(t *testing.T) {
			ctx := context.Background()
			s := newMemoryStore()
			seedInstalledReceipt(t, s, "trusted")
			files := &memoryConfigs{s: s}
			trusted := &TrustedExecution{store: s, files: files, executor: &recordedExecutor{}}
			old, e := trusted.publishContent(ctx, []byte("old"))
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = old.Close() }()
			candidate, e := trusted.PrepareGenerated(ctx, []byte("new"))
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = candidate.Close() }()
			var release func()
			if purpose == "start" {
				_, done, e := trusted.RunCommand(ctx)
				if e != nil {
					t.Fatal(e)
				}
				release = func() {
					if e := done(); e != nil {
						t.Error(e)
					}
				}
			} else {
				prepared := stageFixture(t, trusted.Installer())
				defer prepared.Cleanup()
				v, e := prepared.Verified(ctx)
				if e != nil {
					t.Fatal(e)
				}
				defer func() { _ = v.Close() }()
				x := blockedConfigExecutor{make(chan struct{}), make(chan struct{})}
				done := make(chan error, 1)
				go func() { done <- ValidateVerifiedConfig(ctx, v, old, x) }()
				<-x.entered
				release = func() {
					close(x.release)
					if e := <-done; e != nil {
						t.Error(e)
					}
				}
			}
			for _, operation := range []string{"publish", "restore"} {
				blocked, cancel := context.WithCancel(ctx)
				observed := &publicationContext{Context: blocked, waiting: make(chan struct{})}
				result := make(chan error, 1)
				go func() {
					var cap *ConfigCapability
					var err error
					if operation == "publish" {
						cap, err = trusted.Publish(observed, candidate, candidate.Hash())
					} else {
						cap, err = trusted.RestoreConfig(observed, []byte("rollback"))
					}
					if cap != nil {
						err = errors.Join(err, cap.Close())
					}
					result <- err
				}()
				select {
				case err := <-result:
					cancel()
					release()
					t.Fatalf("%s completed during execution ownership: %v", operation, err)
				case <-observed.waiting:
					cancel()
					if err := <-result; !errors.Is(err, context.Canceled) {
						release()
						t.Fatalf("%s did not wait at execution gate: %v", operation, err)
					}
				}
				if files.writes != 1 {
					release()
					t.Fatal("configuration changed while execution owned path")
				}
			}
			var cap *ConfigCapability
			release()
			cap, e = trusted.Publish(ctx, candidate, candidate.Hash())
			if e != nil {
				t.Fatal(e)
			}
			if e = cap.Close(); e != nil {
				t.Fatal(e)
			}
			if files.writes != 2 {
				t.Fatal("publication did not resume after release")
			}
		})
	}
}

type publicationContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *publicationContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestTrustedRuntime_InitializeRejectsIncompletePair(t *testing.T) {
	for _, missing := range []ProvenanceRole{InstalledBinary, InstalledReceipt} {
		t.Run(string(missing), func(t *testing.T) {
			s := newMemoryStore()
			seedInstalledReceipt(t, s, "trusted")
			observed, e := s.Inspect(context.Background(), missing, "")
			if e != nil {
				t.Fatal(e)
			}
			if e = s.Apply(context.Background(), ProvenanceMutation{Role: missing, Expected: observed}); e != nil {
				t.Fatal(e)
			}
			files := &memoryConfigs{s: s}
			trusted := &TrustedExecution{store: s, files: files, executor: &recordedExecutor{}}
			settings := config.Defaults()
			settings.ControllerSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
			content, err := subscription.Generate(subscription.Document{"proxies": []any{}}, nil, settings)
			if err != nil {
				t.Fatal(err)
			}
			if e = trusted.InitializeConfig(context.Background(), content); e == nil || files.writes != 0 {
				t.Fatal("incomplete installed pair accepted")
			}
		})
	}
}

func TestTrustedRuntime_PrepareClonesBytesAndRejectsForeignOwner(t *testing.T) {
	s := newMemoryStore()
	seedInstalledReceipt(t, s, "trusted")
	files := &memoryConfigs{s: s}
	x := &recordedExecutor{store: s}
	trusted := &TrustedExecution{store: s, files: files, executor: x}
	source := []byte("original generated bytes")
	candidate, e := trusted.PrepareGenerated(context.Background(), source)
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := candidate.Close(); e != nil {
			t.Error(e)
		}
	}()
	source[0] = 'X'
	foreign := &TrustedExecution{store: s, files: files, executor: x}
	if _, e = foreign.Publish(context.Background(), candidate, candidate.Hash()); e == nil || files.writes != 0 {
		t.Fatal("foreign candidate published")
	}
	cap, e := trusted.Publish(context.Background(), candidate, candidate.Hash())
	if e != nil {
		t.Fatal(e)
	}
	if e = cap.Close(); e != nil {
		t.Fatal(e)
	}
	actual, e := files.read(context.Background())
	if e != nil || string(actual) != "original generated bytes" || x.configHashes[0] != digest(actual) {
		t.Fatal("source mutation altered validated bytes")
	}
}
func TestTrustedRuntime_ValidationRejectsPublication(t *testing.T) {
	for _, initialize := range []bool{false, true} {
		t.Run(fmt.Sprint(initialize), func(t *testing.T) {
			s := newMemoryStore()
			seedInstalledReceipt(t, s, "trusted")
			files := &memoryConfigs{s: s}
			x := &recordedExecutor{failValidation: true}
			trusted := &TrustedExecution{store: s, files: files, executor: x}
			var err error
			if initialize {
				err = trusted.InitializeConfig(context.Background(), []byte("invalid"))
			} else {
				var candidate *GeneratedConfig
				candidate, err = trusted.PrepareGenerated(context.Background(), []byte("invalid"))
				if candidate != nil {
					t.Fatal("rejected candidate returned")
				}
			}
			if err == nil || files.writes != 0 || len(x.commands) != 1 {
				t.Fatal("validation rejection bypassed")
			}
			if _, err = trusted.CommittedConfig(context.Background()); err == nil {
				t.Fatal("rejected bytes marked committed")
			}
		})
	}
}
func TestTrustedRuntime_RejectsEmptyGeneratedBytes(t *testing.T) {
	s := newMemoryStore()
	files := &memoryConfigs{s: s}
	trusted := &TrustedExecution{store: s, files: files}
	if err := trusted.InitializeConfig(context.Background(), nil); err == nil {
		t.Fatal("empty bootstrap accepted")
	}
	if candidate, err := trusted.PrepareGenerated(context.Background(), nil); err == nil || candidate != nil {
		t.Fatal("empty candidate accepted")
	}
	if files.sequence != 0 || files.writes != 0 {
		t.Fatal("empty bytes staged or published")
	}
}

// configFaultFile models failure of the identity check after a successful write.
type configFaultFile struct {
	verifiedFile
	err error
}

func (f *configFaultFile) verify(context.Context) (string, error) { return "", f.err }
