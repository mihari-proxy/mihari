package core

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/subscription"
	"os"
	"testing"
)

type memoryConfigs struct {
	s           *memoryStore
	sequence    int
	writes      int
	failWriteAt int
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
	return f.s.Load(ctx, ProvenanceRole("runtime_config"), "")
}
func (f *memoryConfigs) write(ctx context.Context, b []byte) (*ConfigCapability, error) {
	f.writes++
	if e := f.s.Save(ctx, ProvenanceRole("runtime_config"), "", b); e != nil {
		return nil, e
	}
	if f.writes == f.failWriteAt {
		return nil, errors.New("sync failed after replacement")
	}
	return f.bind(ctx, sha256.Sum256(b))
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
	first, e := trusted.PrepareGenerated(context.Background(), subscription.PolicyOutput{YAML: []byte("first config")})
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if closeErr := first.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	second, e := trusted.PrepareGenerated(context.Background(), subscription.PolicyOutput{YAML: []byte("second config")})
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
func TestTrustedRuntime_GreenBootstrapUsesPolicy(t *testing.T) {
	s := newMemoryStore()
	files := &memoryConfigs{s: s}
	trusted := &TrustedExecution{store: s, files: files}
	settings := config.Defaults()
	settings.ControllerSecret = "test-secret"
	input := subscription.PolicyInput{SubscriptionID: testTransaction, Generation: 1, CoreTag: "v1.19.30", OS: "linux", Arch: "amd64"}
	if e := trusted.InitializeConfig(context.Background(), settings, input); e != nil {
		t.Fatalf("green policy bootstrap failed: %v", e)
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
	candidate, e := trusted.PrepareGenerated(context.Background(), subscription.PolicyOutput{YAML: []byte("new config")})
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
