package service

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

var definitionStates = []struct {
	running, enabled bool
}{
	{false, false},
	{false, true},
	{true, false},
	{true, true},
}

func loadDefinitionTestdata(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "definitions", name))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("\r")) {
		t.Fatalf("%s contains CR", name)
	}
	return raw
}

type fakeRunner struct {
	calls    [][]string
	reloaded bool
	handlers []runnerHandler
}

type runnerHandler struct {
	match func([]string) bool
	run   func([]string) (CommandResult, error)
}

func (f *fakeRunner) handle(match func([]string) bool, run func([]string) (CommandResult, error)) {
	f.handlers = append(f.handlers, runnerHandler{match: match, run: run})
}

func (f *fakeRunner) Run(_ context.Context, argv []string) (CommandResult, error) {
	copied := append([]string(nil), argv...)
	f.calls = append(f.calls, copied)
	if len(copied) == 0 || !unixAbs(copied[0]) {
		return CommandResult{}, invalidServiceState("service manager tool path is not absolute")
	}
	for _, arg := range copied {
		if strings.Contains(arg, "gui/") {
			return CommandResult{}, invalidServiceState("gui domain is not allowed")
		}
		if arg == "--runtime" {
			return CommandResult{}, invalidServiceState("runtime mask is not allowed")
		}
	}
	if containsArg(copied, "daemon-reload") {
		f.reloaded = true
	}
	for i := len(f.handlers) - 1; i >= 0; i-- {
		if f.handlers[i].match(copied) {
			return f.handlers[i].run(copied)
		}
	}
	return CommandResult{}, invalidServiceState("unexpected service manager invocation")
}

type memFS struct {
	files map[string]DefinitionFile
	links map[string]string
}

func newMemFS() *memFS {
	return &memFS{files: map[string]DefinitionFile{}, links: map[string]string{}}
}

func (m *memFS) Read(_ context.Context, name string) (DefinitionFile, error) {
	if target, ok := m.links[name]; ok {
		kind := "link"
		if target == defaultDevNull {
			kind = "mask"
		}
		return DefinitionFile{Path: name, Kind: kind, Mode: 0o777}, nil
	}
	file, ok := m.files[name]
	if !ok {
		return DefinitionFile{}, os.ErrNotExist
	}
	file.Bytes = append([]byte(nil), file.Bytes...)
	return file, nil
}

func (m *memFS) Write(_ context.Context, file DefinitionFile) error {
	copied := file
	copied.Bytes = append([]byte(nil), file.Bytes...)
	m.files[file.Path] = copied
	delete(m.links, file.Path)
	return nil
}

func (m *memFS) Mask(_ context.Context, name, target string) error {
	if strings.Contains(name, "/run/") {
		return invalidServiceState("runtime mask is not allowed")
	}
	m.links[name] = target
	delete(m.files, name)
	return nil
}

func (m *memFS) Remove(_ context.Context, name string) error {
	delete(m.files, name)
	delete(m.links, name)
	return nil
}

func (m *memFS) ReadLink(_ context.Context, name string) (string, error) {
	target, ok := m.links[name]
	if !ok {
		return "", os.ErrNotExist
	}
	return target, nil
}

func (m *memFS) List(_ context.Context, dir string) ([]string, error) {
	prefix := dir
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	var names []string
	seen := map[string]struct{}{}
	for name := range m.files {
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
			seen[name] = struct{}{}
		}
	}
	for name := range m.links {
		if _, ok := seen[name]; ok {
			continue
		}
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	return names, nil
}

func (m *memFS) masked(name string) bool {
	return m.links[name] == defaultDevNull
}

type fakeTree struct {
	empty         bool
	emptyAfter    int
	signals       []string
	groups        []string
	lookups       int
	alive         bool
	signalLookups int
	identity      ProcessIdentity
}

func (t *fakeTree) Identify(_ context.Context, pid int) (ProcessIdentity, error) {
	if pid <= 0 {
		return ProcessIdentity{}, nil
	}
	id := t.identity
	if id.StartUnix == 0 || id.BootID == "" {
		id.BootID = "boot-test"
		id.StartUnix = 1700000000
		id.StartUsec = 42
	}
	id.PID = pid
	return id, nil
}

func (t *fakeTree) Empty(context.Context, string) (bool, error) {
	if t.empty {
		return true, nil
	}
	if t.emptyAfter > 0 && len(t.signals) >= t.emptyAfter {
		return true, nil
	}
	return false, nil
}

func (t *fakeTree) SignalGroup(_ context.Context, group, signal string) error {
	t.groups = append(t.groups, group)
	t.signals = append(t.signals, signal)
	return nil
}

func (t *fakeTree) Lookup(_ context.Context, id ProcessIdentity) (bool, error) {
	t.lookups++
	if id.PID <= 0 {
		return false, nil
	}
	if incompleteProcessIdentity(id) {
		return false, invalidServiceState("service process identity is unknown")
	}
	return t.alive, nil
}

func (t *fakeTree) SignalIdentity(_ context.Context, id ProcessIdentity, signal string) error {
	t.signalLookups++
	if id.PID <= 0 || id.StartUnix == 0 {
		return invalidServiceState("service process identity is unknown")
	}
	if !t.alive {
		return nil
	}
	t.signals = append(t.signals, signal)
	if t.emptyAfter > 0 && len(t.signals) >= t.emptyAfter {
		t.alive = false
	}
	return nil
}

type fakeClock struct {
	now    time.Time
	sleeps []time.Duration
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
	return nil
}

type recordingHook struct {
	kinds []string
}

func (h *recordingHook) wrap(ctx context.Context, action DefinitionAction, apply func(context.Context) error) error {
	h.kinds = append(h.kinds, action.Kind)
	if apply == nil {
		return invalidServiceState("missing service action")
	}
	return apply(ctx)
}

func containsArg(argv []string, want string) bool {
	for _, arg := range argv {
		if arg == want {
			return true
		}
	}
	return false
}

func argvHasPrefix(argv, prefix []string) bool {
	if len(argv) < len(prefix) {
		return false
	}
	for i, arg := range prefix {
		if argv[i] != arg {
			return false
		}
	}
	return true
}

func requireInvalidState(t *testing.T, err error) {
	t.Helper()
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeInvalidState {
		t.Fatalf("want invalid_state, got %v", err)
	}
	if strings.Contains(api.Message, "MIHARI_") || strings.Contains(strings.ToLower(api.Message), "secret") {
		t.Fatalf("error leaked env: %q", api.Message)
	}
}

func trustedUnitFile(t *testing.T) DefinitionFile {
	t.Helper()
	return DefinitionFile{
		Path:  defaultSystemdUnitFile,
		Bytes: loadDefinitionTestdata(t, "mihari.service"),
		Owner: 0,
		Mode:  0o644,
		Kind:  "unit",
	}
}

func trustedDropinFile(t *testing.T) DefinitionFile {
	t.Helper()
	return DefinitionFile{
		Path:  path.Join(defaultSystemdDropinDir, "10-mihari.conf"),
		Bytes: loadDefinitionTestdata(t, "10-mihari.conf"),
		Owner: 0,
		Mode:  0o644,
		Kind:  "dropin",
	}
}

func trustedPlistFile(t *testing.T) DefinitionFile {
	t.Helper()
	return DefinitionFile{
		Path:  defaultPlistPath,
		Bytes: loadDefinitionTestdata(t, "mihari.plist"),
		Owner: 0,
		Mode:  0o644,
		Kind:  "plist",
	}
}
