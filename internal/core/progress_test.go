package core

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestProgressReader_ThrottlesAndReportsFinalBytes(t *testing.T) {
	var updates []Progress
	ctx := WithProgressReporter(context.Background(), func(p Progress) { updates = append(updates, p) })
	r := newProgressReader(ctx, strings.NewReader(strings.Repeat("x", 1024)), 1024)
	buffer := make([]byte, 1)
	for {
		_, err := r.Read(buffer)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(updates) > 3 || updates[len(updates)-1].Received != 1024 {
		t.Fatalf("updates=%d final=%+v", len(updates), updates[len(updates)-1])
	}
}

func TestProgressReader_ReportsAfterInterval(t *testing.T) {
	var updates []Progress
	ctx := WithProgressReporter(context.Background(), func(p Progress) { updates = append(updates, p) })
	r := newProgressReader(ctx, strings.NewReader("payload"), 7)
	r.lastReport = time.Now().Add(-time.Second)
	if n, err := r.Read(make([]byte, 3)); n != 3 || err != nil {
		t.Fatalf("read=%d err=%v", n, err)
	}
	if len(updates) != 2 || updates[1].Received != 3 || updates[1].Total != 7 {
		t.Fatalf("updates=%+v", updates)
	}
}
