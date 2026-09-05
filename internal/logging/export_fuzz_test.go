package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func FuzzExportJSON(f *testing.F) {
	const secret = "fuzz-export-secret"
	seeds := [][]byte{
		[]byte(`{"time":"2026-09-02T12:00:00Z","msg":"ok"}` + "\n"),
		[]byte(`{"time":"2026-09-02T12:00:00Z","msg":"example.test"}` + "\n"),
		[]byte(`{"time":"2026-09-02T12:00:00Z","fuzz-export-secret":"private key","https://example.test/private":"URL key"}` + "\n"),
		append([]byte(`{"time":"2026-09-02T12:00:00Z","msg":"`), append([]byte{0xff}, []byte(`"}`+"\n")...)...),
		[]byte(strings.Repeat("x", MaxExportRecordBytes+1)),
		[]byte(`{"time":"2026-09-02T12:00:00Z","nested":{"authorization":"fuzz-export-secret","message":"https://example.test/private"}}` + "\n"),
		[]byte(`{"time":"2026-09-02T12:00:00Z","value":900719925474099312345678901234567890}` + "\n"),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input []byte) {
		// Keep both seed-only regression runs and any explicitly requested fuzz
		// session bounded independently of generated input size.
		if len(input) > 2*MaxExportRecordBytes+2 {
			t.Skip()
		}
		var output bytes.Buffer
		_, err := exportJSON(context.Background(), bytes.NewReader(input), &output, ExportRange{Kind: RangeAll}, NewRedactor(secret))
		if err != nil {
			t.Fatalf("exportJSON returned an unexpected reader/writer error: %v", err)
		}
		if bytes.Contains(output.Bytes(), []byte(secret)) || bytes.Contains(output.Bytes(), []byte("https://example.test/private")) {
			t.Fatalf("export leaked a known sensitive value: %q", output.Bytes())
		}
		for _, line := range bytes.Split(bytes.TrimSuffix(output.Bytes(), []byte("\n")), []byte("\n")) {
			if len(line) == 0 {
				continue
			}
			decoder := json.NewDecoder(bytes.NewReader(line))
			decoder.UseNumber()
			var record map[string]any
			if err := decoder.Decode(&record); err != nil || record == nil {
				t.Fatalf("output line is not a JSON object: %q: %v", line, err)
			}
			var trailing any
			if err := decoder.Decode(&trailing); err != io.EOF {
				t.Fatalf("output line contains trailing JSON: %q: %v", line, err)
			}
		}
	})
}

func FuzzExportTargetParts(f *testing.F) {
	root := f.TempDir()
	for _, seed := range []string{"logs.zip", "../logs.zip", "logs.txt", "logs.ZIP", ".zip", "logs\x00.zip"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, name string) {
		if len(name) > 4096 {
			t.Skip()
		}
		// Concatenation deliberately preserves traversal segments for the raw
		// case; Join provides the normalized valid-path companion on every OS.
		for _, output := range []string{root + string(filepath.Separator) + name, filepath.Join(root, name)} {
			gotName, parent, _, err := exportTargetParts(ExportRequest{OutputPath: output})
			if err != nil {
				continue
			}
			if !filepath.IsAbs(parent) || filepath.Base(filepath.Clean(output)) != gotName || !strings.EqualFold(filepath.Ext(gotName), ".zip") {
				t.Fatalf("accepted target escaped normalized absolute zip contract: name=%q parent=%q output=%q", gotName, parent, output)
			}
		}
	})
}
