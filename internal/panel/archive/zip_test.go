package archive

import (
	"archive/zip"
	"bytes"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestExtractZipAcceptsNestedRootWithIndexHTML(t *testing.T) {
	archivePath := writeZip(t, map[string]string{
		"dist/index.html":  "<html>ok</html>",
		"dist/assets/a.js": "console.log(1)",
	})
	dest := filepath.Join(t.TempDir(), "out")
	if err := ExtractZip(archivePath, dest); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dest, "dist", "index.html"))
	if err != nil || string(raw) != "<html>ok</html>" {
		t.Fatalf("index=%q err=%v", raw, err)
	}
}

func TestExtractZipRejectsPathTraversal(t *testing.T) {
	archivePath := writeZip(t, map[string]string{
		"index.html":        "<html>ok</html>",
		"../escape.txt":     "nope",
		"nested/../../evil": "nope",
	})
	dest := filepath.Join(t.TempDir(), "out")
	if err := ExtractZip(archivePath, dest); err == nil {
		t.Fatal("expected path traversal rejection")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "escape.txt")); err == nil {
		t.Fatal("traversal wrote outside destination")
	}
}

func TestExtractZipRejectsAbsolutePaths(t *testing.T) {
	// Use forward-slash absolute-style names that zip.Writer will store as-is.
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	addZipFile(t, writer, "/tmp/evil.txt", "nope")
	addZipFile(t, writer, "index.html", "<html>ok</html>")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "abs.zip")
	if err := os.WriteFile(archivePath, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "out")
	if err := ExtractZip(archivePath, dest); err == nil {
		t.Fatal("expected absolute path rejection")
	}
}

func TestExtractZipRejectsMissingIndexHTML(t *testing.T) {
	archivePath := writeZip(t, map[string]string{
		"readme.txt": "no index",
	})
	dest := filepath.Join(t.TempDir(), "out")
	if err := ExtractZip(archivePath, dest); err == nil {
		t.Fatal("expected missing index.html rejection")
	}
	if _, err := os.Stat(dest); err == nil {
		// Destination may exist empty or partial; require no index.html accepted.
		if _, err := os.Stat(filepath.Join(dest, "index.html")); err == nil {
			t.Fatal("index.html should not be present")
		}
	}
}

func TestExtractZipRejectsSymlinkEntries(t *testing.T) {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	header := &zip.FileHeader{Name: "link-to-etc", Method: zip.Deflate}
	header.SetMode(os.ModeSymlink | 0o777)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("/etc/passwd")); err != nil {
		t.Fatal(err)
	}
	addZipFile(t, writer, "index.html", "<html>ok</html>")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "symlink.zip")
	if err := os.WriteFile(archivePath, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "out")
	if err := ExtractZip(archivePath, dest); err == nil {
		t.Fatal("expected symlink rejection")
	}
}

func TestExtractZipRejectsTooManyEntries(t *testing.T) {
	files := map[string]string{
		"index.html": "<html>ok</html>",
		"a.txt":      "a",
		"b.txt":      "b",
	}
	archivePath := writeZip(t, files)
	dest := filepath.Join(t.TempDir(), "out")
	err := extractZipWithLimits(archivePath, dest, extractLimits{
		maxFile: MaxExtractedFileSize, maxTotal: MaxTotalExtractedBytes, maxEntries: 2, maxDepth: MaxArchiveDepth,
	})
	assertExtractRejected(t, dest, err, "panel archive has too many entries")
}

func TestExtractZipRejectsPathTooDeep(t *testing.T) {
	name := strings.Repeat("a/", MaxArchiveDepth) + "index.html"
	archivePath := writeZip(t, map[string]string{name: "<html>ok</html>"})
	dest := filepath.Join(t.TempDir(), "out")
	err := ExtractZip(archivePath, dest)
	assertExtractRejected(t, dest, err, "panel archive path is too deep")
}

func TestExtractZipRejectsDeclaredTotalTooLarge(t *testing.T) {
	index := []byte("<html>ok</html>")
	limits := extractLimits{maxFile: 200, maxTotal: 150, maxEntries: 16, maxDepth: MaxArchiveDepth}
	archivePath := writeRawZip(t, []rawZipFile{
		{Name: "index.html", Payload: index, Uncompressed: uint64(len(index))},
		{Name: "big.txt", Payload: []byte("tiny"), Uncompressed: 200},
	})
	dest := filepath.Join(t.TempDir(), "out")
	err := extractZipWithLimits(archivePath, dest, limits)
	assertExtractRejected(t, dest, err, "panel archive is too large")
}

func TestExtractZipRejectsActualTotalTooLarge(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 40)
	limits := extractLimits{maxFile: 100, maxTotal: 50, maxEntries: 16, maxDepth: MaxArchiveDepth}
	archivePath := writeZip(t, map[string]string{
		"index.html": string(payload),
		"more.txt":   string(payload),
	})
	dest := filepath.Join(t.TempDir(), "out")
	err := extractZipWithLimits(archivePath, dest, limits)
	assertExtractRejected(t, dest, err, "panel archive is too large")
}

func TestExtractFileReturnsCopiedBytes(t *testing.T) {
	payload := []byte("copied-bytes")
	archivePath := writeZip(t, map[string]string{"index.html": string(payload)})
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	target := filepath.Join(t.TempDir(), "out.html")
	written, err := extractFile(reader.File[0], target, MaxExtractedFileSize)
	if err != nil || written != int64(len(payload)) {
		t.Fatalf("written=%d err=%v", written, err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func assertExtractRejected(t *testing.T, dest string, err error, wantMsg string) {
	t.Helper()
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != protocol.CodeDataFailure || api.Message != wantMsg {
		t.Fatalf("err=%v want %q", err, wantMsg)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("dest still exists: %v", statErr)
	}
}

type rawZipFile struct {
	Name         string
	Payload      []byte
	Uncompressed uint64
}

func writeRawZip(t *testing.T, files []rawZipFile) string {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for _, file := range files {
		header := &zip.FileHeader{Name: file.Name, Method: zip.Store}
		header.UncompressedSize64 = file.Uncompressed
		header.CompressedSize64 = uint64(len(file.Payload))
		header.CRC32 = crc32.ChecksumIEEE(file.Payload)
		entry, err := writer.CreateRaw(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(file.Payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "raw.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSafeArchiveName(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"index.html", true},
		{"assets/app.js", true},
		{"dist/index.html", true},
		{"../evil", false},
		{"nested/../../evil", false},
		{"/abs", false},
		{"C:/abs", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := SafeName(tc.name); got != tc.ok {
			t.Errorf("SafeName(%q)=%v want %v", tc.name, got, tc.ok)
		}
	}
}

func writeZip(t *testing.T, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, content := range files {
		addZipFile(t, writer, name, content)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "panel.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func addZipFile(t *testing.T, writer *zip.Writer, name, content string) {
	t.Helper()
	entry, err := writer.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
}

func FuzzSafeArchivePath(f *testing.F) {
	seeds := []string{
		"index.html",
		"assets/app.js",
		".",
		"..",
		"../evil",
		"nested/../../evil",
		"/tmp/abs",
		"C:/Windows/system32",
		`\\unc\share\file`,
		"a\\b/c",
		"name\x00evil",
		"",
		"café/index.html",
		strings.Repeat("a", 300) + "/b",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, name string) {
		root := t.TempDir()
		safe := SafeName(name)
		target, err := resolveTarget(root, name)
		if !safe {
			if err == nil {
				t.Fatalf("SafeName rejected %q but resolveTarget accepted %q", name, target)
			}
			return
		}
		if err != nil {
			// SafeName may accept names that Join still rejects on this platform.
			return
		}
		rel, relErr := filepath.Rel(root, target)
		if relErr != nil {
			t.Fatalf("Rel(%q,%q): %v", root, target, relErr)
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			t.Fatalf("resolved outside root: name=%q target=%q rel=%q", name, target, rel)
		}
	})
}
