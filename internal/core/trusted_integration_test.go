package core_test

import (
	"bytes"
	"context"
	"errors"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/core"

	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"

	"github.com/mihari-proxy/mihari/internal/subscription"

	"github.com/mihari-proxy/mihari/internal/tundetect"
	"go.yaml.in/yaml/v3"

	"path/filepath"

	"github.com/mihari-proxy/mihari/internal/app"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/supervisor"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type seamController struct {
	runtimeapi.Controller
	fixture          *core.TestTrustedFixture
	patches, reloads int
	failReloads      int
	tun              map[string]any
}

func (c *seamController) Configs(context.Context) (map[string]any, error) {
	return map[string]any{"tun": c.tun}, nil
}
func (c *seamController) PatchConfigs(_ context.Context, p map[string]any) error {
	c.patches++
	c.tun = p["tun"].(map[string]any)
	return nil
}
func (c *seamController) Reload(_ context.Context, _ string, _ bool) error {
	c.reloads++
	if c.reloads <= c.failReloads {
		return errors.New("reload rejected")
	}
	var document map[string]any
	if e := yaml.Unmarshal(c.fixture.Content(), &document); e != nil {
		return e
	}
	c.tun, _ = document["tun"].(map[string]any)
	return nil
}

type seamFetcher struct{}

func (seamFetcher) Fetch(context.Context, subscription.FetchRequest) (subscription.FetchResult, error) {
	return subscription.FetchResult{Content: []byte("proxies: []\nproxy-groups: []\nrules:\n  - MATCH,DIRECT\n")}, nil
}

type seamTunDetect struct{}

func (seamTunDetect) Detect(context.Context) (tundetect.Detection, error) {
	return tundetect.Detection{}, nil
}
func seamManager(t *testing.T, change func(*runtimeapi.Options)) (*runtimeapi.Manager, *core.TestTrustedFixture, *seamController, *subscription.Service) {
	t.Helper()
	root := t.TempDir()
	f := core.NewTestTrustedFixture(t, root)
	service, e := subscription.Open(subscription.ServiceOptions{CatalogPath: filepath.Join(root, "catalog.yaml"), CacheDir: filepath.Join(root, "cache"), Downloader: seamFetcher{}})
	if e != nil {
		t.Fatal(e)
	}
	settings := config.Defaults()
	settings.ControllerSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	c := &seamController{fixture: f, tun: map[string]any{"enable": false, "stack": "gVisor"}}
	options := runtimeapi.Options{TrustedCore: f.Trusted, RootConfigInput: func(context.Context, subscription.Document, config.Settings) (subscription.PolicyInput, error) {
		return core.FixturePolicyInput(), nil
	}, Subscriptions: service, Settings: settings, RuntimeConfig: filepath.Join(root, "runtime", "config.yaml"), StagingDir: filepath.Join(root, "staging"), Controller: c, Installer: f, TunDetect: seamTunDetect{}, LookupTCPOccupant: func(string) (int, bool) { return 0, false }}
	if change != nil {
		change(&options)
	}
	return runtimeapi.New(options), f, c, service
}
func assertCode(t *testing.T, e error, code protocol.ErrorCode) {
	t.Helper()
	var api protocol.APIError
	if !errors.As(e, &api) || api.Code != code {
		t.Fatalf("error=%v, want %s", e, code)
	}
}
func TestRootManager_TunPolicyFailureCannotFallBackToPatch(t *testing.T) {
	m, f, c, _ := seamManager(t, func(o *runtimeapi.Options) {
		o.RootConfigInput = func(context.Context, subscription.Document, config.Settings) (subscription.PolicyInput, error) {
			input := core.FixturePolicyInput()
			input.CoreTag = "unsupported-fixture-tag"
			return input, nil
		}
	})
	before := f.Content()
	_, e := m.EnableTun(context.Background(), runtimeapi.Operation{ID: "enable", Source: "test"}, true)
	if e == nil {
		t.Fatal("trusted policy refusal masked by successful PATCH")
	}
	status, e2 := m.TunStatus(context.Background())
	if e2 != nil {
		t.Fatal(e2)
	}
	if status.DesiredEnable || !bytes.Equal(before, f.Content()) || c.patches != 0 {
		t.Fatal("trusted failure did not preserve settings/config or used PATCH")
	}
}
func TestRootManager_ActivationRejectsStaleGeneration(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var pause atomic.Bool
	m, f, c, service := seamManager(t, func(o *runtimeapi.Options) {
		o.RootConfigInput = func(_ context.Context, _ subscription.Document, s config.Settings) (subscription.PolicyInput, error) {
			if pause.Load() && s.Tun["enable"] != true {
				close(entered)
				<-release
			}
			return core.FixturePolicyInput(), nil
		}
	})
	p, e := service.Add("cached", "https://fixture.invalid/sub", subscription.ProxyModeDirect)
	if e != nil {
		t.Fatal(e)
	}
	prepared, e := service.PrepareRefresh(context.Background(), p.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = service.CommitRefresh(prepared); e != nil {
		t.Fatal(e)
	}
	pause.Store(true)
	done := make(chan error, 1)
	go func() {
		_, e := m.UseSubscription(context.Background(), runtimeapi.Operation{ID: "activate", Source: "test"}, p.ID)
		done <- e
	}()
	<-entered
	if _, e = m.EnableTun(context.Background(), runtimeapi.Operation{ID: "enable", Source: "test"}, true); e != nil {
		t.Fatal(e)
	}
	newer := f.Content()
	reloads := c.reloads
	close(release)
	assertCode(t, <-done, protocol.CodeRevisionConflict)
	status, e := m.TunStatus(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if !status.DesiredEnable || !bytes.Equal(newer, f.Content()) || c.reloads != reloads {
		t.Fatal("stale activation overwrote newer config/settings")
	}
}

func cachedProfile(t *testing.T, s *subscription.Service) string {
	t.Helper()
	p, e := s.Add("cached", "https://fixture.invalid/sub", subscription.ProxyModeDirect)
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.PrepareRefresh(context.Background(), p.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.CommitRefresh(r); e != nil {
		t.Fatal(e)
	}
	return p.ID
}

type seamChild struct {
	binary       []byte
	done, joined chan struct{}
	once         sync.Once
}

func (c *seamChild) PID() int         { return 4321 }
func (c *seamChild) Wait() error      { <-c.done; close(c.joined); return nil }
func (c *seamChild) Terminate() error { c.once.Do(func() { close(c.done) }); return nil }
func (c *seamChild) Kill() error      { return c.Terminate() }

type seamStarter struct {
	children chan *seamChild
	starts   atomic.Int32
	inspect  func() []byte
}

func (s *seamStarter) Start() (supervisor.Child, error) {
	c := &seamChild{done: make(chan struct{}), joined: make(chan struct{})}
	s.starts.Add(1)
	if s.inspect != nil {
		c.binary = s.inspect()
	}
	s.children <- c
	return c, nil
}
func startSeamSupervisor(t *testing.T, m *runtimeapi.Manager, s *supervisor.Supervisor, starter *seamStarter) *seamChild {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(3 * time.Second):
			t.Error("supervisor/observer did not join")
		}
	})
	return <-starter.children
}
func awaitSeam(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case e := <-done:
		return e
	case <-time.After(3 * time.Second):
		t.Fatal("Manager transaction/observer deadlocked")
		return nil
	}
}
func TestRootManager_ReloadCompensationAndDegradedStop(t *testing.T) {
	for _, fails := range []int{1, 2} {
		t.Run(map[int]string{1: "restored", 2: "degraded"}[fails], func(t *testing.T) {
			starter := &seamStarter{children: make(chan *seamChild, 8)}
			var manager *runtimeapi.Manager
			sup := supervisor.New(supervisor.Options{Starter: starter, Observe: func(o supervisor.Observation) { manager.Observe(o) }})
			manager, f, c, service := seamManager(t, func(o *runtimeapi.Options) { o.Supervisor = sup })
			id := cachedProfile(t, service)
			child := startSeamSupervisor(t, manager, sup, starter)
			before := f.Content()
			c.failReloads = fails
			done := make(chan error, 1)
			go func() {
				_, e := manager.UseSubscription(context.Background(), runtimeapi.Operation{ID: "use", Source: "test"}, id)
				done <- e
			}()
			e := awaitSeam(t, done)
			assertCode(t, e, protocol.CodeUpstreamFailure)
			if c.reloads != 2 || !bytes.Equal(before, f.Content()) {
				t.Fatal("reload failure did not restore previous bytes and reload twice")
			}
			cap, e := f.Trusted.CommittedConfig(context.Background())
			if e != nil {
				t.Fatal("previous config hash not rebound", e)
			}
			if e = cap.Close(); e != nil {
				t.Fatal(e)
			}
			if fails == 2 {
				select {
				case <-child.joined:
				default:
					t.Fatal("degraded recovery left core running")
				}
				if starter.starts.Load() != 1 {
					t.Fatal("degraded recovery restarted core")
				}
			} else {
				select {
				case <-child.done:
					t.Fatal("successful compensation stopped healthy core")
				default:
				}
			}
		})
	}
}
func TestRootManager_TunDegradedFailureStopsAfterUnlock(t *testing.T) {
	for _, kind := range []string{"reload", "publication"} {
		t.Run(kind, func(t *testing.T) {
			starter := &seamStarter{children: make(chan *seamChild, 8)}
			var manager *runtimeapi.Manager
			sup := supervisor.New(supervisor.Options{Starter: starter, Observe: func(o supervisor.Observation) { manager.Observe(o) }})
			manager, f, c, _ := seamManager(t, func(o *runtimeapi.Options) { o.Supervisor = sup })
			child := startSeamSupervisor(t, manager, sup, starter)
			before := f.Content()
			expectedCode := protocol.CodeUpstreamFailure
			if kind == "reload" {
				c.failReloads = 2
			} else {
				f.FailPublicationAndRecovery()
				expectedCode = protocol.CodeDataFailure
			}
			done := make(chan error, 1)
			go func() {
				_, e := manager.EnableTun(context.Background(), runtimeapi.Operation{ID: "tun", Source: "test"}, true)
				done <- e
			}()
			e := awaitSeam(t, done)
			assertCode(t, e, expectedCode)
			var api protocol.APIError
			if !errors.As(e, &api) || api.Details["degraded"] != true {
				t.Fatal("degraded detail lost")
			}
			status, e := manager.TunStatus(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			if status.DesiredEnable || !bytes.Equal(before, f.Content()) || c.patches != 0 {
				t.Fatal("degraded TUN failure masked or settings/config not restored")
			}
			select {
			case <-child.joined:
			default:
				t.Fatal("degraded TUN left core running")
			}
			if starter.starts.Load() != 1 {
				t.Fatal("degraded TUN restarted core")
			}

		})
	}
}

type seamInstaller struct {
	fixture      *core.TestTrustedFixture
	prepared     func()
	beforeCommit func() error
	commits      atomic.Int32
}

func (i *seamInstaller) Prepare(ctx context.Context, r core.InstallRequest) (core.PreparedCore, error) {
	c, e := i.fixture.Prepare(ctx, r)
	if e != nil {
		return nil, e
	}
	if i.prepared != nil {
		i.prepared()
	}
	return &seamPrepared{PreparedCore: c, owner: i}, nil
}
func (i *seamInstaller) DetectVersion(ctx context.Context, p string) (string, error) {
	return i.fixture.DetectVersion(ctx, p)
}

type seamPrepared struct {
	core.PreparedCore
	owner *seamInstaller
}

func (c *seamPrepared) Commit() (core.InstallResult, error) {
	c.owner.commits.Add(1)
	if c.owner.beforeCommit != nil {
		if e := c.owner.beforeCommit(); e != nil {
			return core.InstallResult{}, e
		}
	}
	return c.PreparedCore.Commit()
}
func TestRootManager_InstallMaintenanceAndCandidateFailure(t *testing.T) {
	for _, kind := range []string{"commit", "stale", "candidate-rejected"} {
		t.Run(kind, func(t *testing.T) {
			starter := &seamStarter{children: make(chan *seamChild, 8)}
			var manager *runtimeapi.Manager
			sup := supervisor.New(supervisor.Options{Starter: starter, Observe: func(o supervisor.Observation) { manager.Observe(o) }})
			installer := &seamInstaller{}
			store := state.NewStore(state.Snapshot{Health: "ok"})
			manager, f, _, _ := seamManager(t, func(o *runtimeapi.Options) { o.Supervisor = sup; o.Installer = installer; o.Store = store })
			installer.fixture = f
			starter.inspect = f.Binary
			child := startSeamSupervisor(t, manager, sup, starter)
			old := f.Binary()
			installer.beforeCommit = func() error {
				select {
				case <-child.joined:
					return nil
				default:
					return errors.New("actual Commit preceded child join")
				}
			}
			var revision uint64
			op := runtimeapi.Operation{ID: "install", Source: "test"}
			if kind == "stale" {
				revision = store.Load().Revision
				op.IfRevision = &revision
				installer.prepared = func() { snapshot := store.Load(); snapshot.Revision++; store.Store(snapshot) }
			}
			if kind == "candidate-rejected" {
				f.Execute = func(_ context.Context, c core.CoreCommand) ([]byte, error) {
					if c.Args[0] == "-t" {
						return nil, errors.New("candidate invalid")
					}
					return []byte("Mihomo v1.19.30"), nil
				}
			}
			_, e := manager.Install(context.Background(), op)
			switch kind {
			case "commit":
				if e != nil {
					t.Fatal(e)
				}
				if installer.commits.Load() != 1 || bytes.Equal(old, f.Binary()) {
					t.Fatal("real candidate pair was not committed")
				}
			case "stale":
				assertCode(t, e, protocol.CodeRevisionConflict)
				if installer.commits.Load() != 0 || !bytes.Equal(old, f.Binary()) {
					t.Fatal("stale revision committed pair")
				}
			case "candidate-rejected":
				if e == nil || installer.commits.Load() != 0 || !bytes.Equal(old, f.Binary()) {
					t.Fatal("candidate validation failure changed old pair")
				}
				select {
				case <-child.done:
					t.Fatal("failed Prepare entered maintenance")
				default:
				}
			}
			if kind != "candidate-rejected" {
				select {
				case restarted := <-starter.children:
					if !bytes.Equal(restarted.binary, f.Binary()) {
						t.Fatal("restart selected a pair before Commit/recovery converged")
					}
					if kind == "commit" && bytes.Equal(restarted.binary, old) {
						t.Fatal("restart selected old pair after Commit")
					}
				case <-time.After(3 * time.Second):
					t.Fatal("prior running lifecycle was not restarted")
				}
			}

		})
	}
}
func TestRootAssembly_RecoversBeforeAnyExecutor(t *testing.T) {
	root := t.TempDir()
	f := core.NewTestTrustedFixture(t, root)
	f.InterruptPair(t)
	paths := platform.NewPaths(root)
	paths.CoreBinary = filepath.Join(paths.Bin, "mihomo")
	if e := os.MkdirAll(paths.Bin, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(paths.CoreBinary, []byte("never executed: stat fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	var purposes []string
	f.Execute = func(_ context.Context, c core.CoreCommand) ([]byte, error) {
		if f.Pending() {
			return nil, errors.New("executor ran before WAL recovery")
		}
		purposes = append(purposes, c.Args[0])
		return []byte("Mihomo v1.19.30"), nil
	}
	settings := config.Defaults()
	settings.ControllerSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	var ports []net.Listener
	for range 3 {
		listener, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		ports = append(ports, listener)
	}
	settings.MixedAddr = ports[0].Addr().String()
	settings.ControllerAddr = ports[1].Addr().String()
	settings.WebAddr = ports[2].Addr().String()
	for _, listener := range ports {
		if e := listener.Close(); e != nil {
			t.Fatal(e)
		}
	}
	assembly, e := app.BuildRuntimeWithOptions(paths, settings, "test", nil, nil, app.RuntimeBuildOptions{TrustedCore: f.Trusted, RootConfigInput: func(context.Context, subscription.Document, config.Settings) (subscription.PolicyInput, error) {
		return core.FixturePolicyInput(), nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	if f.Pending() || len(purposes) != 2 || purposes[0] != "-t" || purposes[1] != "-v" || assembly.Store.Load().Core.Version != "v1.19.30" {
		t.Fatalf("root assembly did not recover/validate/probe in order: %v", purposes)
	}
}
