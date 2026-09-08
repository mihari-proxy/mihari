//go:build darwin && unix_security

package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/core"
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/securitytest"
	"github.com/mihari-proxy/mihari/internal/supervisor"
	"golang.org/x/sys/unix"
)

const bootoutHelperRole = "MIHARI_LAUNCHD_BOOTOUT_HELPER"
const bootoutHelperDirectory = "MIHARI_LAUNCHD_BOOTOUT_DIRECTORY"

type bootoutFixture struct{ root, dir, nonce, label, binary string }
type bootoutProcess struct{ PID, PGID, Parent int }
type bootoutJobRecord struct {
	Schema string `json:"schema"`
	Label  string `json:"label"`
	Plist  string `json:"plist"`
	PGID   int    `json:"pgid"`
}

func init() {
	if role := os.Getenv(bootoutHelperRole); role != "" {
		if err := runBootoutHelper(role); err != nil {
			// Never print launchd's environment or unrestricted manager output.
			fmt.Fprintln(os.Stderr, "isolated launchd bootout helper failed")
			os.Exit(1)
		}
		os.Exit(0)
	}
}

func TestSecurityLaunchdBootoutDrainsSharedProcessGroup(t *testing.T) {
	securitytest.Parent(t)
	root := os.Getenv("MIHARI_SECURITY_ROOT")
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	nonce := hex.EncodeToString(random[:])
	dir := filepath.Join(root, "launchd-bootout-"+nonce)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := validatedBootoutFixture(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err := bootoutJobAbsent(ctx, f.label); err != nil {
		t.Fatal("fixture label already exists or cannot be inspected", err)
	}
	if err := os.Mkdir(filepath.Join(root, "launchd-jobs"), 0700); err != nil && !errors.Is(err, os.ErrExist) {
		t.Fatal(err)
	}
	// Register cleanup before bootstrap, including failures during startup.
	// Keep the private intent/plist for the independent always-recovery runner.
	bootstrapAttempted := false
	t.Cleanup(func() {
		if !bootstrapAttempted {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
		defer cancel()
		if _, err := validatedBootoutFixture(dir); err != nil {
			t.Error(err)
			return
		}
		_ = bootoutLaunchctl(ctx, "bootout", "system/"+f.label)
		if err := bootoutJobAbsent(ctx, f.label); err != nil {
			// The unique fixture label remains the only signal authority here.
			_ = bootoutLaunchctl(ctx, "kill", "SIGKILL", "system/"+f.label)
			_ = bootoutLaunchctl(ctx, "bootout", "system/"+f.label)
			if err := bootoutJobAbsent(ctx, f.label); err != nil {
				t.Error("fixture launchd job remains loaded", err)
				return
			}
		}
		record, err := readBootoutRecord(f)
		if err != nil {
			t.Error(err)
			return
		}
		if err := confirmBootoutCleanup(ctx, record.PGID, waitBootoutGroupGone); err != nil {
			t.Error("fixture process group cleanup is unconfirmed", err)
		}
	})
	if err := writeBootoutFile(f.dir, "job.plist", bootoutPlist(f)); err != nil {
		t.Fatal(err)
	}
	if err := storeBootoutRecord(f, 0); err != nil {
		t.Fatal(err)
	}
	bootstrapAttempted = true
	if err := bootoutLaunchctl(ctx, "bootstrap", "system", filepath.Join(f.dir, "job.plist")); err != nil {
		t.Fatal("fixture bootstrap failed", err)
	}
	var processes [3]bootoutProcess
	for i, role := range []string{"daemon", "core", "grandchild"} {
		if err := bootoutWait(ctx, func() (bool, error) {
			raw, err := os.ReadFile(filepath.Join(f.dir, role+"-ready.json"))
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			return true, json.Unmarshal(raw, &processes[i])
		}); err != nil {
			t.Fatal("fixture readiness failed", err)
		}
	}
	group := processes[0].PGID
	if group <= 1 || group != processes[0].PID || group == unix.Getpgrp() {
		t.Fatal("launchd did not isolate the fixture daemon as group leader")
	}
	for i, process := range processes {
		observed, err := unix.Getpgid(process.PID)
		if process.PID <= 1 || err != nil || observed != group || process.PGID != group {
			t.Fatal("actual daemon/core/grandchild did not share the launchd process group")
		}
		if i > 0 && process.Parent != processes[i-1].PID {
			t.Fatal("fixture descendant parent mismatch")
		}
	}
	record, err := readBootoutRecord(f)
	if err != nil || record.PGID != group {
		t.Fatal("durable fixture process group disagrees", err)
	}
	if err := bootoutLaunchctl(ctx, "bootout", "system/"+f.label); err != nil {
		t.Fatal("fixture bootout failed", err)
	}
	proof, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	// The helper lifetime is one minute. This shorter proof cannot pass merely
	// because the intentionally TERM-resistant descendants reached their bound.
	if err := waitBootoutGroupGone(proof, group); err != nil {
		t.Fatal("bootout left members in the shared process group", err)
	}
	if err := bootoutJobAbsent(ctx, f.label); err != nil {
		t.Fatal("bootout left the fixture job loaded", err)
	}
}

func validatedBootoutFixture(dir string) (bootoutFixture, error) {
	var f bootoutFixture
	if os.Geteuid() != 0 || os.Getenv("CI") != "true" || os.Getenv("GITHUB_ACTIONS") != "true" || os.Getenv("RUNNER_ENVIRONMENT") != "github-hosted" || os.Getenv("MIHARI_ISOLATED_SECURITY_CI") != "1" {
		return f, os.ErrPermission
	}
	f.root = os.Getenv("MIHARI_SECURITY_ROOT")
	if !regexp.MustCompile(`^/Library/MihariSecurity-[a-f0-9]{12}$`).MatchString(f.root) || platform.SystemLayoutDefaults().BaseDir != filepath.Join(f.root, "system") {
		return f, os.ErrPermission
	}
	f.nonce = strings.TrimPrefix(filepath.Base(dir), "launchd-bootout-")
	if !regexp.MustCompile(`^[a-f0-9]{16}$`).MatchString(f.nonce) || dir != filepath.Join(f.root, "launchd-bootout-"+f.nonce) {
		return f, os.ErrPermission
	}
	f.dir = dir
	f.label = "com.mihari.security." + strings.TrimPrefix(f.root, "/Library/MihariSecurity-") + ".shared-group." + f.nonce
	var err error
	f.binary, err = os.Executable()
	if err != nil || f.binary != filepath.Join(f.root, "shared", "internal-integration.test") {
		return f, os.ErrPermission
	}
	var st unix.Stat_t
	if err := unix.Lstat(f.binary, &st); err != nil {
		return f, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0777 != 0755 || st.Uid != 0 || st.Nlink != 1 {
		return f, os.ErrPermission
	}
	trusted, err := platform.OpenTrustedRoot(context.Background(), f.dir, platform.RootPolicy{Owner: 0, Mode: 0700})
	if err != nil {
		return f, err
	}
	return f, trusted.Close()
}

func bootoutEnvironment(f bootoutFixture, role string) []string {
	env := []string{"PATH=/usr/bin:/bin", bootoutHelperRole + "=" + role, bootoutHelperDirectory + "=" + f.dir}
	for _, key := range []string{"CI", "GITHUB_ACTIONS", "RUNNER_ENVIRONMENT", "MIHARI_ISOLATED_SECURITY_CI", "MIHARI_SECURITY_ROOT", "MIHARI_SECURITY_RESULTS"} {
		env = append(env, key+"="+os.Getenv(key))
	}
	return env
}

func bootoutPlist(f bootoutFixture) []byte {
	quoted := func(value string) string {
		var out bytes.Buffer
		_ = xml.EscapeText(&out, []byte(value))
		return out.String()
	}
	var out strings.Builder
	fmt.Fprintf(&out, `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Label</key><string>%s</string><key>ProgramArguments</key><array><string>%s</string></array><key>RunAtLoad</key><true/><key>KeepAlive</key><false/><key>AbandonProcessGroup</key><false/><key>ExitTimeOut</key><integer>2</integer><key>EnvironmentVariables</key><dict>`, quoted(f.label), quoted(f.binary))
	for _, entry := range bootoutEnvironment(f, "daemon") {
		key, value, _ := strings.Cut(entry, "=")
		fmt.Fprintf(&out, "<key>%s</key><string>%s</string>", quoted(key), quoted(value))
	}
	out.WriteString("</dict></dict></plist>\n")
	return []byte(out.String())
}

func bootoutLaunchctl(ctx context.Context, args ...string) error {
	commandCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	command := exec.CommandContext(commandCtx, "/bin/launchctl", args...)
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C"}
	return command.Run()
}

func bootoutJobAbsent(ctx context.Context, label string) error {
	err := bootoutLaunchctl(ctx, "print", "system/"+label)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 113 {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect fixture launchd job: %w", err)
	}
	return errors.New("fixture launchd job absence was not confirmed")
}

func waitBootoutGroupGone(ctx context.Context, pgid int) error {
	if pgid <= 1 || pgid == unix.Getpgrp() {
		return os.ErrPermission
	}
	return bootoutWait(ctx, func() (bool, error) {
		err := unix.Kill(-pgid, 0)
		if errors.Is(err, unix.ESRCH) {
			return true, nil
		}
		return false, err
	})
}

func bootoutWait(ctx context.Context, check func() (bool, error)) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if done, err := check(); done || err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func writeBootoutFile(dir, name string, raw []byte) (resultErr error) {
	root, err := platform.OpenTrustedRoot(context.Background(), dir, platform.RootPolicy{Owner: 0, Mode: 0700})
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	return root.WriteFile(context.Background(), name, raw, 0600, nil)
}

func readBootoutRecord(f bootoutFixture) (record bootoutJobRecord, resultErr error) {
	ctx := context.Background()
	root, err := platform.OpenTrustedRoot(ctx, filepath.Join(f.root, "launchd-jobs"), platform.RootPolicy{Owner: 0, Mode: 0700})
	if err != nil {
		return record, err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	file, _, err := root.OpenFile(ctx, f.nonce+".json", 0600)
	if err != nil {
		return record, err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	return decodeBootoutRecord(f, file)
}

func decodeBootoutRecord(f bootoutFixture, input io.Reader) (bootoutJobRecord, error) {
	var record bootoutJobRecord
	raw, err := io.ReadAll(io.LimitReader(input, 4097))
	if err != nil {
		return record, err
	}
	if len(raw) > 4096 || json.Unmarshal(raw, &record) != nil || record.Schema != "mihari.security-launchd-job/v1" || record.Label != f.label || record.Plist != filepath.Join(f.dir, "job.plist") || record.PGID < 0 || record.PGID == 1 {
		return record, os.ErrPermission
	}
	return record, nil
}

func storeBootoutRecord(f bootoutFixture, pgid int) (resultErr error) {
	ctx := context.Background()
	root, err := platform.OpenTrustedRoot(ctx, filepath.Join(f.root, "launchd-jobs"), platform.RootPolicy{Owner: 0, Mode: 0700})
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	var expected *platform.FileIdentity
	file, identity, err := root.OpenFile(ctx, f.nonce+".json", 0600)
	if err == nil {
		current, readErr := decodeBootoutRecord(f, file)
		if err := errors.Join(readErr, file.Close()); err != nil {
			return err
		}
		if current.PGID != 0 && current.PGID != pgid {
			return os.ErrPermission
		}
		expected = &identity
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else if pgid != 0 {
		return os.ErrPermission
	}
	raw, err := json.Marshal(bootoutJobRecord{"mihari.security-launchd-job/v1", f.label, filepath.Join(f.dir, "job.plist"), pgid})
	if err != nil {
		return err
	}
	return root.WriteFile(ctx, f.nonce+".json", append(raw, '\n'), 0600, expected)
}

func runBootoutHelper(role string) error {
	f, err := validatedBootoutFixture(os.Getenv(bootoutHelperDirectory))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ready := func() error {
		raw, err := json.Marshal(bootoutProcess{os.Getpid(), unix.Getpgrp(), os.Getppid()})
		if err != nil {
			return err
		}
		return writeBootoutFile(f.dir, role+"-ready.json", raw)
	}
	switch role {
	case "grandchild":
		signal.Ignore(unix.SIGTERM)
		if err := ready(); err != nil {
			return err
		}
		<-ctx.Done()
		return nil
	case "core":
		signal.Ignore(unix.SIGTERM)
		grandchild := exec.Command(f.binary)
		grandchild.Env = bootoutEnvironment(f, "grandchild")
		if err := grandchild.Start(); err != nil {
			return err
		}
		done := make(chan error, 1)
		go func() { done <- grandchild.Wait() }()
		if err := ready(); err != nil {
			return err
		}
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			return nil
		}
	case "daemon":
		if unix.Getpgrp() != os.Getpid() {
			return errors.New("fixture daemon is not group leader")
		}
		if err := storeBootoutRecord(f, unix.Getpgrp()); err != nil {
			return err
		}
		if err := ready(); err != nil {
			return err
		}
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, unix.SIGTERM)
		defer signal.Stop(stop)
		starter := supervisor.CommandStarter{ShareProcessGroup: true, Stdout: io.Discard, Stderr: io.Discard, CommandFactory: func(context.Context) (core.CoreCommand, func() error, error) {
			return core.CoreCommand{Binary: f.binary, Home: f.dir, Env: bootoutEnvironment(f, "core")}, func() error { return nil }, nil
		}}
		child, err := starter.Start()
		if err != nil {
			return err
		}
		done := make(chan error, 1)
		go func() { done <- child.Wait() }()
		// Deliberately leave TERM-resistant descendants for launchd to collect.
		// This helper process exits immediately after returning from init.
		select {
		case <-stop:
			return nil
		case err := <-done:
			return err
		case <-ctx.Done():
			return nil
		}
	default:
		return os.ErrInvalid
	}
}
