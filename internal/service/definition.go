package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

const (
	DefinitionActionMask       = "mask"
	DefinitionActionUnmask     = "unmask"
	DefinitionActionDisable    = "disable"
	DefinitionActionEnable     = "enable"
	DefinitionActionStop       = "stop"
	DefinitionActionStart      = "start"
	DefinitionActionDefinition = "definition"
	DefinitionActionDropin     = "dropin"
	DefinitionActionDisabled   = "disabled"
	DefinitionActionReload     = "reload"

	serviceUnitName = "mihari.service"
	serviceLabel    = "mihari"

	defaultSystemctl        = "/usr/bin/systemctl"
	defaultLaunchctl        = "/bin/launchctl"
	defaultSystemdUnitDir   = "/etc/systemd/system"
	defaultSystemdUnitFile  = "/etc/systemd/system/mihari.service"
	defaultSystemdDropinDir = "/etc/systemd/system/mihari.service.d"
	defaultPlistPath        = "/Library/LaunchDaemons/mihari.plist"
	defaultCgroupRoot       = "/sys/fs/cgroup"
	defaultDevNull          = "/dev/null"

	stopWait = 30 * time.Second
	termWait = 5 * time.Second
	killWait = 5 * time.Second
	pollWait = 100 * time.Millisecond
)

// DefinitionAdapter is the Unix service-manager seam consumed by install transactions.
// Windows keeps Controller and does not implement these methods.
type DefinitionAdapter interface {
	InspectDefinition(context.Context) (Definition, error)
	DisableAutostartAndStop(context.Context) error
	WaitOwnedTreeExit(context.Context) error
	WriteDefinition(context.Context, Definition) error
	RestoreDefinition(context.Context, Definition) error
	Start(context.Context) error
	Probe(context.Context) (Definition, error)
}

// Definition is a read-only snapshot of the managed unit or launchd job.
type Definition struct {
	Status  StatusKind
	Enabled bool
	Running bool
	Masked  bool
	Binary  string
	Args    []string
	Env     []string
	Files   []DefinitionFile
	Links   []DefinitionLink
	Process ProcessIdentity
}

// DefinitionFile is one unit, drop-in, mask, or plist object.
type DefinitionFile struct {
	// Identity records the saved inode. Restoration may replace a later unit
	// or mask; transaction recovery validates its recorded publication versions.
	Identity string
	Path     string
	Bytes    []byte
	Owner    uint32
	Mode     uint32
	Kind     string
}

// DefinitionLink is an enable/wants symlink recorded for exact restore.
type DefinitionLink struct {
	Identity string
	Path     string
	Target   string
	Owner    uint32
	Mode     uint32
}

// ProcessIdentity identifies a managed process across PID reuse.
type ProcessIdentity struct {
	PID       int
	BootID    string
	StartUnix int64
	StartUsec uint32
	Group     string
	Comm      string
}

// DefinitionAction is one external mutation T14 wraps with intent/done.
type DefinitionAction struct {
	// Path and File identify a concrete private-backed definition effect.
	Path string
	File *DefinitionFile
	Link string

	Kind       string
	TargetRole string
	OldState   string
	NewState   string
}

// RecoveryAdapter performs one recorded effect beneath an outer journal lease.
// ReplayAction must only be called after its caller persisted an intent.
type RecoveryAdapter interface {
	DefinitionAdapter
	ObserveAction(context.Context, DefinitionAction) (string, error)
	ReplayAction(context.Context, DefinitionAction) error
}

// ActionHook surrounds one concrete manager or filesystem effect.
// T14 supplies a journaled hook; adapters must call it once per effect.
type ActionHook func(ctx context.Context, action DefinitionAction, apply func(context.Context) error) error

// DirectActionHook runs apply without journaling. Tests and T14 replace it.
func DirectActionHook(ctx context.Context, action DefinitionAction, apply func(context.Context) error) error {
	if apply == nil {
		return invalidServiceState("missing service action")
	}
	return apply(ctx)
}

// CommandRunner executes argv[0] as an absolute path with argv[1:] as arguments.
type CommandRunner interface {
	Run(ctx context.Context, argv []string) (CommandResult, error)
}

// CommandResult is a completed process observation. Non-zero ExitCode is not itself an error.
type CommandResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

// DefinitionStore reads and publishes service definition objects.
type DefinitionStore interface {
	Read(ctx context.Context, path string) (DefinitionFile, error)
	Write(ctx context.Context, file DefinitionFile) error
	Mask(ctx context.Context, path, target string) error
	Remove(ctx context.Context, path string) error
	ReadLink(ctx context.Context, path string) (string, error)
	List(ctx context.Context, dir string) ([]string, error)
}

// ProcessTree observes and signals a verified cgroup or launchd identity.
type ProcessTree interface {
	Empty(ctx context.Context, group string) (bool, error)
	SignalGroup(ctx context.Context, group, signal string) error
	Lookup(ctx context.Context, id ProcessIdentity) (bool, error)
	SignalIdentity(ctx context.Context, id ProcessIdentity, signal string) error
	Identify(ctx context.Context, pid int) (ProcessIdentity, error)
}

// Clock is an injectable sleeper used by WaitOwnedTreeExit.
type Clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// OSCommandRunner invokes an absolute executable without a shell.
type OSCommandRunner struct{}

func (OSCommandRunner) Run(ctx context.Context, argv []string) (CommandResult, error) {
	if err := ctx.Err(); err != nil {
		return CommandResult{}, err
	}
	if len(argv) == 0 || !unixAbs(argv[0]) {
		return CommandResult{}, invalidServiceState("service manager tool path is not absolute")
	}
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	result := CommandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if command.ProcessState != nil {
		result.ExitCode = command.ProcessState.ExitCode()
	}
	if err == nil {
		return result, nil
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return CommandResult{}, invalidServiceState("service manager is unavailable")
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return result, nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return CommandResult{}, err
	}
	return CommandResult{}, invalidServiceState("service manager query failed")
}

func unixAbs(path string) bool {
	return strings.HasPrefix(path, "/") && !strings.Contains(path, "\x00")
}

func invalidServiceState(message string) error {
	return protocol.APIError{Code: protocol.CodeInvalidState, Message: message}
}

func runAbsolute(ctx context.Context, runner CommandRunner, argv []string) (CommandResult, error) {
	if err := ctx.Err(); err != nil {
		return CommandResult{}, err
	}
	if runner == nil {
		return CommandResult{}, invalidServiceState("service manager is unavailable")
	}
	if len(argv) == 0 || !unixAbs(argv[0]) {
		return CommandResult{}, invalidServiceState("service manager tool path is not absolute")
	}
	result, err := runner.Run(ctx, argv)
	if err != nil {
		return CommandResult{}, redactServiceError(err)
	}
	return result, nil
}

func redactServiceError(err error) error {
	if err == nil {
		return nil
	}
	var api protocol.APIError
	if errors.As(err, &api) {
		api.Details = nil
		if api.Code == "" {
			api.Code = protocol.CodeInvalidState
		}
		if api.Message == "" {
			api.Message = "service manager query failed"
		}
		return api
	}
	return invalidServiceState("service manager query failed")
}

func applyAction(ctx context.Context, hook ActionHook, action DefinitionAction, apply func(context.Context) error) error {
	if hook == nil {
		hook = DirectActionHook
	}
	return hook(ctx, action, apply)
}

func cloneDefinition(def Definition) Definition {
	out := def
	out.Args = append([]string(nil), def.Args...)
	out.Env = append([]string(nil), def.Env...)
	out.Files = append([]DefinitionFile(nil), def.Files...)
	for i := range out.Files {
		out.Files[i].Bytes = append([]byte(nil), def.Files[i].Bytes...)
	}
	out.Links = append([]DefinitionLink(nil), def.Links...)
	return out
}

func incompleteProcessIdentity(id ProcessIdentity) bool {
	return id.PID > 0 && (id.StartUnix == 0 || id.BootID == "")
}

func requireZeroExit(result CommandResult) error {
	if result.ExitCode != 0 {
		return invalidServiceState("service manager query failed")
	}
	return nil
}

func classifyCgroupEmpty(dirExists bool, eventsErr error, events []byte) (bool, error) {
	if !dirExists {
		return true, nil
	}
	if errors.Is(eventsErr, os.ErrNotExist) {
		return false, invalidServiceState("service process tree is unknown")
	}
	if eventsErr != nil {
		return false, invalidServiceState("service process tree is unknown")
	}
	populated, ok := parseCgroupPopulated(events)
	if !ok {
		return false, invalidServiceState("service process tree is unknown")
	}
	return !populated, nil
}

func parseCgroupPopulated(raw []byte) (bool, bool) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	found := false
	populated := false
	for scanner.Scan() {
		line := scanner.Text()
		key, value, ok := strings.Cut(line, "=")
		if !ok || key != "populated" {
			continue
		}
		if found {
			return false, false
		}
		found = true
		switch value {
		case "1":
			populated = true
		case "0":
			populated = false
		default:
			return false, false
		}
	}
	return populated, found && scanner.Err() == nil
}

func definitionStatus(installed, running bool) StatusKind {
	if !installed {
		return StatusNotInstalled
	}
	if running {
		return StatusRunning
	}
	return StatusStopped
}

var (
	_ DefinitionAdapter = (*SystemdAdapter)(nil)
	_ DefinitionAdapter = (*LaunchdAdapter)(nil)
)
