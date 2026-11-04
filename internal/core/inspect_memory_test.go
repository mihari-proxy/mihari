package core

import (
	"context"
	"testing"
)

func BenchmarkFileUpdateInspect(b *testing.B) {
	benchmarkFileUpdateInspect(b)
}

// Inspect returns metadata and must not allocate a copy of the executable.
// The generous budget covers platform file checks while remaining independent
// of the 4 MiB object size; correctness checks remain in the transaction tests.
func TestFileUpdateInspect_BoundedMemory(t *testing.T) {
	result := testing.Benchmark(benchmarkFileUpdateInspect)
	if allocated := result.AllocedBytesPerOp(); allocated > 512<<10 {
		t.Fatalf("metadata inspection allocated %d bytes for a 4 MiB object; want at most 512 KiB", allocated)
	}
}

func benchmarkFileUpdateInspect(b *testing.B) {
	store, err := NewUpdateStore(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	tx := "1234567890abcdef1234567890abcdef"
	body := make([]byte, 4<<20)
	if err := store.Save(context.Background(), UpdateCandidate, tx, body); err != nil {
		b.Fatal(err)
	}
	want := digest(body)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for range b.N {
		object, err := store.Inspect(context.Background(), UpdateCandidate, tx)
		if err != nil || !object.Present || object.SHA256 != want {
			b.Fatalf("inspection=%+v err=%v", object, err)
		}
	}
}
