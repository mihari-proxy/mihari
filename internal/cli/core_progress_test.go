package cli

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type progressStatusClient struct {
	progress *protocol.OperationProgress
}

func (c progressStatusClient) OperationStatus(context.Context, string) (protocol.OperationStatus, error) {
	return protocol.OperationStatus{State: "running", Progress: c.progress}, nil
}

func TestFollowCoreProgress_RewritesOneStderrLine(t *testing.T) {
	received := int64(1024)
	total := int64(2048)
	ctx, cancel := context.WithCancel(context.Background())
	var buf lockedBuffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		followCoreProgress(ctx, progressStatusClient{progress: &protocol.OperationProgress{
			Phase: protocol.ProgressPhaseDownloading, ReceivedBytes: &received, TotalBytes: &total, ElapsedMilliseconds: 1000,
		}}, "op", &buf, time.Millisecond, time.Now())
	}()
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(buf.String(), "Downloading mihomo core  1.0 KiB / 2.0 KiB  00:01") {
		if time.Now().After(deadline) {
			t.Fatalf("stderr = %q", buf.String())
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	if !strings.HasPrefix(buf.String(), "\r") || !strings.HasSuffix(buf.String(), "\n") {
		t.Fatalf("stderr = %q", buf.String())
	}
}

func TestTrackCoreInstallProgress_JSONStaysQuiet(t *testing.T) {
	var buf lockedBuffer
	stop := trackCoreInstallProgress(context.Background(), true, progressStatusClient{}, "op", &buf)
	stop()
	if buf.String() != "" {
		t.Fatalf("json stderr = %q", buf.String())
	}
}

func TestWriteCoreProgress_ChangedStatusRedrawsWithoutNewline(t *testing.T) {
	var buf lockedBuffer
	received, total := int64(1024), int64(2048)
	client := progressStatusClient{progress: &protocol.OperationProgress{Phase: protocol.ProgressPhaseDownloading, ReceivedBytes: &received, TotalBytes: &total}}
	previous, wrote := writeCoreProgress(context.Background(), client, "op", &buf, time.Time{}, "", false)
	received = 2048
	writeCoreProgress(context.Background(), client, "op", &buf, time.Time{}, previous, wrote)
	output := buf.String()
	if strings.Count(output, "\r") != 2 || strings.Contains(output, "\n") || !strings.Contains(output, "1.0 KiB") || !strings.Contains(output, "2.0 KiB / 2.0 KiB") {
		t.Fatalf("stderr=%q", output)
	}
}

type completingProgressClient struct {
	*fakeRuntimeClient
	started chan struct{}
	stopped chan struct{}
}

func (c *completingProgressClient) OperationStatus(context.Context, string) (protocol.OperationStatus, error) {
	return protocol.OperationStatus{State: "running"}, nil
}

type progressCompletionWriter struct {
	client  *completingProgressClient
	started sync.Once
	stopped sync.Once
}

func (w *progressCompletionWriter) Write(p []byte) (int, error) {
	if strings.HasPrefix(string(p), "\r") {
		w.started.Do(func() { close(w.client.started) })
	}
	if strings.Contains(string(p), "\n") {
		w.stopped.Do(func() { close(w.client.stopped) })
	}
	return len(p), nil
}

func (c *completingProgressClient) InstallCore(ctx context.Context, _ protocol.MutationRequest) (protocol.CoreInstallResult, error) {
	select {
	case <-c.started:
		return protocol.CoreInstallResult{Version: "fixture"}, nil
	case <-ctx.Done():
		return protocol.CoreInstallResult{}, ctx.Err()
	}
}

type progressResultWriter struct {
	t       *testing.T
	stopped <-chan struct{}
}

func (w progressResultWriter) Write(p []byte) (int, error) {
	select {
	case <-w.stopped:
	default:
		w.t.Error("result rendered before progress observer stopped")
	}
	return len(p), nil
}

func TestCoreInstall_StopsProgressBeforeRenderingResult(t *testing.T) {
	c := &completingProgressClient{fakeRuntimeClient: &fakeRuntimeClient{}, started: make(chan struct{}), stopped: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	exit := Execute(ctx, []string{"core", "install"}, progressResultWriter{t, c.stopped}, &progressCompletionWriter{client: c}, Dependencies{RuntimeClient: c})
	if exit != ExitOK {
		t.Fatalf("exit=%d", exit)
	}
}
