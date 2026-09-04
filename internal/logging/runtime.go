package logging

import (
	"context"
	"log/slog"
	"sync"

	"github.com/mihari-proxy/mihari/internal/platform"
)

// RuntimeOptions opens a component-scoped JSONL logging runtime.
type RuntimeOptions struct {
	BasePath  string
	Component string
	Config    Config
	PrivateFS *platform.PrivateFS
	OpenLock  func(*platform.PrivateFS, string) (platform.AdvisoryLock, error)
	Redactor  *Redactor
	Reporter  FailureReporter
}

// Runtime owns a slog logger, LevelVar, and rotating writer for one log file.
type Runtime struct {
	rotator *RotatingWriter
	level   *slog.LevelVar
	logger  *slog.Logger
}

// Open creates the base log file and returns a runtime. ctx cancel aborts the
// initial lock wait. The shared PrivateFS is not owned by the runtime.
func Open(ctx context.Context, opts RuntimeOptions) (*Runtime, error) {
	rotator, err := OpenRotatingWriter(ctx, RotatorOptions{
		BasePath:  opts.BasePath,
		Config:    opts.Config,
		PrivateFS: opts.PrivateFS,
		OpenLock:  opts.OpenLock,
		Reporter:  opts.Reporter,
	})
	if err != nil {
		return nil, err
	}
	level := &slog.LevelVar{}
	level.Set(opts.Config.Level)
	handler := NewJSONHandler(rotator, level, opts.Component, opts.Redactor)
	return &Runtime{
		rotator: rotator,
		level:   level,
		logger:  slog.New(handler),
	}, nil
}

// Logger returns the runtime slog logger.
func (r *Runtime) Logger() *slog.Logger {
	if r == nil {
		return nil
	}
	return r.logger
}

// Apply swaps level and rotator limits, then converges archives. A canceled
// context still completes the in-memory swap.
func (r *Runtime) Apply(ctx context.Context, cfg Config) {
	r.swapConfig(cfg)
	r.convergeArchives(ctx)
}

// Config returns the current logging config.
func (r *Runtime) Config() Config {
	if r == nil || r.rotator == nil {
		return DefaultConfig()
	}
	return r.rotator.config()
}

// Close closes the rotator, lock, and current write handle. It does not close
// the shared PrivateFS.
func (r *Runtime) Close() error {
	if r == nil || r.rotator == nil {
		return nil
	}
	return r.rotator.Close()
}

// EnterRecordMutex locks the process-local record mutex and returns unlock.
func (r *Runtime) EnterRecordMutex() func() {
	r.rotator.mu.Lock()
	return func() { r.rotator.mu.Unlock() }
}

func (r *Runtime) swapConfig(cfg Config) {
	copied := cfg
	if r.level != nil {
		r.level.Set(copied.Level)
	}
	if r.rotator != nil {
		r.rotator.swapConfig(copied)
	}
}

func (r *Runtime) convergeArchives(ctx context.Context) {
	if r.rotator != nil {
		r.rotator.convergeArchives(ctx)
	}
}

// Group applies one config to multiple runtimes (daemon and mihomo).
type Group struct {
	applyMu sync.Mutex
	mu      sync.Mutex
	dir     string
	cfg     Config
	targets []*Runtime
}

// NewGroup stores dir, the initial config, and the given targets.
func NewGroup(dir string, config Config, targets ...*Runtime) *Group {
	copied := make([]*Runtime, len(targets))
	copy(copied, targets)
	return &Group{dir: dir, cfg: config, targets: copied}
}

// Apply swaps config on every target, then converges archives one by one.
// It must not call Runtime.Apply per target.
func (g *Group) Apply(ctx context.Context, cfg Config) {
	g.apply(ctx, cfg, nil)
}

func (g *Group) apply(ctx context.Context, cfg Config, beforeLock func()) {
	if beforeLock != nil {
		beforeLock()
	}
	g.applyMu.Lock()
	defer g.applyMu.Unlock()

	g.mu.Lock()
	g.cfg = cfg
	targets := g.targets
	g.mu.Unlock()
	for _, t := range targets {
		if t != nil {
			t.swapConfig(cfg)
		}
	}
	for _, t := range targets {
		if t != nil {
			t.convergeArchives(ctx)
		}
	}
}

// Config returns the last applied group config.
func (g *Group) Config() Config {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.cfg
}

// Dir returns the log directory associated with the group.
func (g *Group) Dir() string {
	if g == nil {
		return ""
	}
	return g.dir
}
