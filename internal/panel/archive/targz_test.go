package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

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
