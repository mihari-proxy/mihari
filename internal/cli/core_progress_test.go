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
