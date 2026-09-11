package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// CoreCommand is the complete command produced by verified capabilities.
type CoreCommand struct {
	Binary, Home, Config string
	Args, Env            []string
}

// CorePurpose selects exactly one supported command shape.
type CorePurpose uint8

const (
	CoreVersion CorePurpose = iota
	CoreValidate
	CoreRun
)

type verifiedFile interface {
	verify(context.Context) (string, error)
	Close() error
}

// VerifiedCore holds the installed pair or a private, authenticated candidate.
// Its zero value is unusable; callers cannot fill in trust identities.
type VerifiedCore struct {
	mu              sync.Mutex
	store           ProvenanceStore
	binary, receipt verifiedFile
	candidate       *Candidate
	installed       bool
	closed          bool
}

// ConfigCapability binds a selected generated candidate or committed config.
type ConfigCapability struct {
	mu                sync.Mutex
	file              verifiedFile
	root              string
	hash              [32]byte
	committed, closed bool
}

func (c *VerifiedCore) Command(ctx context.Context, purpose CorePurpose, configuration *ConfigCapability) (CoreCommand, error) {
	if c == nil {
		return CoreCommand{}, os.ErrClosed
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed || c.store == nil || c.binary == nil || c.receipt == nil {
		return CoreCommand{}, os.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return CoreCommand{}, err
	}
	if c.candidate != nil {
		v := c.candidate.trusted
		v.mu.Lock()
		closed = v.closed
		v.mu.Unlock()
		if closed {
			return CoreCommand{}, os.ErrClosed
		}
	}
	if purpose > CoreRun || purpose == CoreVersion && configuration != nil || purpose != CoreVersion && configuration == nil || purpose == CoreRun && !c.installed {
		return CoreCommand{}, os.ErrPermission
	}
	if c.installed {
		if _, e := c.store.Load(ctx, PairJournal, ""); !errors.Is(e, os.ErrNotExist) {
			if e != nil {
				return CoreCommand{}, e
			}
			return CoreCommand{}, dataFailure("provenance recovery required")
		}
	}
	binary, e := c.binary.verify(ctx)
	if e != nil {
		return CoreCommand{}, e
	}
	if _, e = c.receipt.verify(ctx); e != nil {
		return CoreCommand{}, e
	}
	root := c.store.coreStore().location()
	home := filepath.Join(root, "runtime", "core-home")
	command := CoreCommand{Binary: binary, Home: home, Env: fixedEnvironment(home)}
	if purpose == CoreVersion {
		command.Args = []string{"-v"}
		return command, nil
	}
	command.Config, e = configuration.verify(ctx, root, purpose == CoreRun)
	if e != nil {
		return CoreCommand{}, e
	}
	command.Args = []string{"-d", home, "-f", command.Config}
	if purpose == CoreValidate {
		command.Args = append([]string{"-t"}, command.Args...)
	}
	return command, nil
}
func (c *VerifiedCore) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	var a, b error
	if c.binary != nil {
		a = c.binary.Close()
	}
	if c.receipt != nil {
		b = c.receipt.Close()
	}
	return errors.Join(a, b)
}
func (c *ConfigCapability) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.file != nil {
		return c.file.Close()
	}
	return nil
}
func (c *ConfigCapability) verify(ctx context.Context, root string, committed bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.file == nil || c.root != root || committed && !c.committed {
		return "", os.ErrPermission
	}
	return c.file.verify(ctx)
}
func fixedEnvironment(home string) []string {
	return []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "HOME=" + home, "TMPDIR=" + filepath.Join(home, "tmp")}
}

type trustedCandidate struct {
	store                   ProvenanceStore
	transaction             string
	binary, receipt, marker ProvenanceObject
	retired                 map[ProvenanceRole]ProvenanceObject
	closed                  bool
	mu                      sync.Mutex
}

// OpenInstalledCore verifies the recovered installed receipt and binary before
// opening an execution capability. It never recovers while a process may run.
func OpenInstalledCore(ctx context.Context, s ProvenanceStore) (*VerifiedCore, error) {
	if s == nil || s.coreStore() == nil {
		return nil, dataFailure("provenance store unavailable")
	}
	if _, e := s.Load(ctx, PairJournal, ""); !errors.Is(e, os.ErrNotExist) {
		if e != nil {
			return nil, e
		}
		return nil, dataFailure("provenance recovery required")
	}
	return openVerifiedPair(ctx, s, InstalledBinary, InstalledReceipt, "", nil)
}
func openVerifiedPair(ctx context.Context, s ProvenanceStore, br, rr ProvenanceRole, tx string, candidate *Candidate) (*VerifiedCore, error) {
	rb, e := s.Load(ctx, rr, tx)
	if e != nil {
		return nil, e
	}
	var receipt ProvenanceReceipt
	if e = decodeStrict(rb, &receipt); e != nil {
		return nil, e
	}
	if e = receipt.validate(ctx); e != nil {
		return nil, e
	}
	goos, arch := s.coreStore().target()
	if receipt.OS != goos || receipt.Arch != arch {
		return nil, dataFailure("core provenance platform mismatch")
	}
	observed, e := s.Inspect(ctx, br, tx)
	if e != nil {
		return nil, e
	}
	if !observed.Present || observed.SHA256 != receipt.BinarySHA256 {
		return nil, dataFailure("mihomo binary provenance mismatch")
	}
	binary, e := s.coreStore().open(ctx, br, tx)
	if e != nil {
		return nil, e
	}
	rf, e := s.coreStore().open(ctx, rr, tx)
	if e != nil {
		return nil, errors.Join(e, binary.Close())
	}
	current, e := s.Inspect(ctx, br, tx)
	if e == nil && !sameObject(current, observed) {
		e = dataFailure("mihomo binary changed while verifying")
	}
	fresh, re := s.Load(ctx, rr, tx)
	if re != nil || digest(fresh) != digest(rb) {
		e = errors.Join(e, re, dataFailure("mihomo receipt changed while verifying"))
	}
	if e != nil {
		return nil, errors.Join(e, binary.Close(), rf.Close())
	}
	return &VerifiedCore{store: s, binary: binary, receipt: rf, candidate: candidate, installed: candidate == nil}, nil
}

// Verified opens this authenticated candidate, including a green installation
// where no installed pair exists. Commit/Cleanup revoke all candidate commands.
func (c *Candidate) Verified(ctx context.Context) (*VerifiedCore, error) {
	if c == nil || c.trusted == nil {
		return nil, dataFailure("untrusted mihomo candidate")
	}
	v := c.trusted
	v.mu.Lock()
	closed := v.closed
	v.mu.Unlock()
	if closed {
		return nil, os.ErrClosed
	}
	for _, item := range []struct {
		role     ProvenanceRole
		expected ProvenanceObject
	}{{CandidateBinary, v.binary}, {CandidateReceipt, v.receipt}} {
		actual, e := v.store.Inspect(ctx, item.role, v.transaction)
		if e != nil {
			return nil, e
		}
		if !sameObject(actual, item.expected) {
			return nil, dataFailure("trusted candidate identity changed")
		}
	}
	return openVerifiedPair(ctx, v.store, CandidateBinary, CandidateReceipt, v.transaction, c)
}

// Path revalidates and returns this exact config role for the reload adapter.
func (c *ConfigCapability) Path(ctx context.Context) (string, error) {
	if c == nil {
		return "", os.ErrClosed
	}
	return c.verify(ctx, c.root, false)
}

// executionGate is a lifecycle lease, separate from state mutexes. It prevents
// a probe/validator/OS Start from racing managed pair publication. No goroutine
// is created; the owner always releases it after execution or recovery joins.
type executionGate struct {
	mu    sync.Mutex
	token chan struct{}
}

func (g *executionGate) acquire(ctx context.Context) (func(), error) {
	g.mu.Lock()
	if g.token == nil {
		g.token = make(chan struct{}, 1)
		g.token <- struct{}{}
	}
	token := g.token
	g.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-token:
	}
	if e := ctx.Err(); e != nil {
		token <- struct{}{}
		return nil, e
	}
	return func() { token <- struct{}{} }, nil
}
