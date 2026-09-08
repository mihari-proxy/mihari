package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"testing"
)

func TestTarGzip_DirectoryPayloadCannotBypassDecompressionLimit(t *testing.T) {
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	payload := make([]byte, 2<<20)
	if err := tw.WriteHeader(&tar.Header{Name: "directory", Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	// Construct an adversarial header that declares a data section for a
	// directory. archive/tar.Writer correctly refuses to write such a section.
	data := raw.Bytes()
	data[156] = tar.TypeDir
	copy(data[148:156], "        ")
	var checksum int
	for _, b := range data[:512] {
		checksum += int(b)
	}
	copy(data[148:156], fmt.Sprintf("%06o\x00 ", checksum))
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := gz.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	written := false
	err := ExtractTarGzipBytes(compressed.Bytes(), Limits{MaxFile: 16, MaxTotal: 32, MaxEntries: 10, MaxDepth: 16}, nil, func(string, []byte) error {
		written = true
		return nil
	})
	if err == nil || written {
		t.Fatal("oversized directory payload bypassed bounded decompression")
	}
}

func TestTarGzip_ActualReleaseLayoutAndUnsafeEntries(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []*tar.Header
		valid   bool
	}{
		{"unix-aio", []*tar.Header{{Name: "mihari", Size: 3, Typeflag: tar.TypeReg}, {Name: "data/bin/mihomo", Size: 3, Typeflag: tar.TypeReg}}, true},
		{"traversal", []*tar.Header{{Name: "../mihari", Size: 3, Typeflag: tar.TypeReg}}, false},
		{"absolute", []*tar.Header{{Name: "/mihari", Size: 3, Typeflag: tar.TypeReg}}, false},
		{"symlink", []*tar.Header{{Name: "data/bin/mihomo", Typeflag: tar.TypeSymlink, Linkname: "/evil"}}, false},
		{"hardlink", []*tar.Header{{Name: "data/bin/mihomo", Typeflag: tar.TypeLink, Linkname: "mihari"}}, false},
		{"device", []*tar.Header{{Name: "device", Typeflag: tar.TypeChar}}, false},
		{"duplicate", []*tar.Header{{Name: "mihari", Size: 3, Typeflag: tar.TypeReg}, {Name: "./mihari", Size: 3, Typeflag: tar.TypeReg}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var raw bytes.Buffer
			gz := gzip.NewWriter(&raw)
			tw := tar.NewWriter(gz)
			for _, h := range tc.entries {
				if err := tw.WriteHeader(h); err != nil {
					t.Fatal(err)
				}
				if h.Size > 0 {
					if _, err := tw.Write([]byte("bin")); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			files := map[string][]byte{}
			err := ExtractTarGzipBytes(raw.Bytes(), Limits{MaxFile: 16, MaxTotal: 32, MaxEntries: 10, MaxDepth: 16}, func(string) error { return nil }, func(name string, body []byte) error { files[name] = body; return nil })
			if tc.valid {
				if err != nil || string(files["data/bin/mihomo"]) != "bin" || string(files["mihari"]) != "bin" {
					t.Fatalf("valid Unix AIO not extracted: files=%v err=%v", files, err)
				}
			} else if err == nil {
				t.Fatal("unsafe tar accepted")
			}
		})
	}
}

func TestTarGzip_VerifiesFooterAfterTarPadding(t *testing.T) {
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := gz.Write(make([]byte, 32768)); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	broken := append([]byte(nil), raw.Bytes()...)
	broken[len(broken)-8] ^= 1
	if err := ExtractTarGzipBytes(broken, Limits{MaxFile: 16, MaxTotal: 32, MaxEntries: 10, MaxDepth: 16}, nil, nil); err == nil {
		t.Fatal("corrupted gzip footer accepted after tar end")
	}
}
