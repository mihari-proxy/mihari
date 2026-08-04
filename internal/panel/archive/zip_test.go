package archive

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
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
