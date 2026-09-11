package protocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDecodeMachineLogRequest_BodyBudget(t *testing.T) {
	valid := `{"schema":"mihari.machine-log-request/v1","to":"2026-09-05T00:00:00Z"}`
	boundary := valid + strings.Repeat(" ", 4096-len(valid))
	now := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	if _, err := DecodeMachineLogRequest(strings.NewReader(boundary), now); err != nil {
		t.Fatalf("4096-byte request rejected: %v", err)
	}
	reader := strings.NewReader(boundary + strings.Repeat(" ", 100))
	if _, err := DecodeMachineLogRequest(reader, now); err == nil {
		t.Fatal("oversized request accepted")
	}
	if reader.Len() != 99 {
		t.Fatalf("body read was not bounded: %d bytes remaining", reader.Len())
	}
}

func TestDecodeMachineLogPayload_PreservesFixtureBytes(t *testing.T) {
	const encoded = "eyJ0aW1lIjoiMjAyNi0wOS0wNVQwMDowMDowMFoiLCJtc2ciOiLoioLngrkiLCJuIjoxZTB9"
	got, err := DecodeMachineLogPayload(encoded)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("{\"time\":\"2026-09-05T00:00:00Z\",\"msg\":\"节点\",\"n\":1e0}")
	if !bytes.Equal(got, want) {
		t.Fatal("wire JSON bytes were normalized")
	}
	line := append(got, '\n')
	digest := sha256.Sum256(line)
	if len(line) != 55 || hex.EncodeToString(digest[:]) != "1625f1821f85ab2dc68c7da55c4fbe769637b7752174c7d5be8a83cd8d388a48" {
		t.Fatal("fixture wire digest changed")
	}
}

func TestDecodeMachineLogPayload_RecordBudget(t *testing.T) {
	boundary := []byte(`{"msg":"` + strings.Repeat("a", (1<<20)-10) + `"}`)
	if len(boundary) != 1<<20 {
		t.Fatal("invalid boundary fixture")
	}
	got, err := DecodeMachineLogPayload(base64.StdEncoding.EncodeToString(boundary))
	if err != nil || !bytes.Equal(got, boundary) {
		t.Fatalf("1 MiB JSON object rejected: %v", err)
	}
	oversized := append([]byte(" "), boundary...)
	if _, err := DecodeMachineLogPayload(base64.StdEncoding.EncodeToString(oversized)); err == nil {
		t.Fatal("oversized payload accepted")
	}
}

func TestDecodeMachineLogPayload_RejectsNoncanonicalOrNonobject(t *testing.T) {
	for name, encoded := range map[string]string{
		"missing padding":  "e30",
		"nonzero pad bits": "e31=",
		"base64 newline":   "e3\n0=",
		"base64 space":     "e3 0=",
		"null":             base64.StdEncoding.EncodeToString([]byte("null")),
		"array":            base64.StdEncoding.EncodeToString([]byte("[]")),
		"string":           base64.StdEncoding.EncodeToString([]byte(`"secret-value"`)),
		"invalid UTF8":     base64.StdEncoding.EncodeToString([]byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}),
		"two objects":      base64.StdEncoding.EncodeToString([]byte("{}{}")),
		"raw newline":      base64.StdEncoding.EncodeToString([]byte("{\n}")),
		"trailing LF":      base64.StdEncoding.EncodeToString([]byte("{}\n")),
		"invalid JSON":     base64.StdEncoding.EncodeToString([]byte("{no}")),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeMachineLogPayload(encoded)
			var api APIError
			if !errors.As(err, &api) || api.Code != CodeDataFailure {
				t.Fatalf("want data_failure, got %v", err)
			}
			if strings.Contains(err.Error(), "secret-value") {
				t.Fatal("payload leaked into error")
			}
		})
	}
}

func TestDecodeMachineLogRequest_ClosedUTCWindow(t *testing.T) {
	now := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name, input string
		from        *time.Time
		to          time.Time
	}{
		{"all through to", `{"schema":"mihari.machine-log-request/v1","to":"2026-09-05T00:00:00Z"}`, nil, now},
		{"closed singleton", `{"schema":"mihari.machine-log-request/v1","from":"2026-09-05T00:00:00Z","to":"2026-09-05T00:00:00Z"}`, &now, now},
		{"future boundary", `{"to":"2026-09-05T00:05:00Z","schema":"mihari.machine-log-request/v1"}`, nil, now.Add(5 * time.Minute)},
		{"nanoseconds", `{"schema":"mihari.machine-log-request/v1","to":"2026-09-05T00:00:00.123456789Z"}`, nil, now.Add(123456789)},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := DecodeMachineLogRequest(strings.NewReader(test.input), now)
			if err != nil {
				t.Fatal(err)
			}
			if got.Schema != "mihari.machine-log-request/v1" || !got.To.Equal(test.to) {
				t.Fatalf("wrong window: %+v", got)
			}
			if (got.From == nil) != (test.from == nil) || (got.From != nil && !got.From.Equal(*test.from)) {
				t.Fatal("from bound changed")
			}
		})
	}
}

func TestDecodeMachineLogRequest_RejectsMalformedOrAmbiguousInput(t *testing.T) {
	now := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	for name, input := range map[string]string{
		"missing schema":    `{"to":"2026-09-05T00:00:00Z"}`,
		"unknown schema":    `{"schema":"mihari.machine-log-request/v2","to":"2026-09-05T00:00:00Z"}`,
		"missing to":        `{"schema":"mihari.machine-log-request/v1"}`,
		"null to":           `{"schema":"mihari.machine-log-request/v1","to":null}`,
		"null from":         `{"schema":"mihari.machine-log-request/v1","from":null,"to":"2026-09-05T00:00:00Z"}`,
		"unknown path":      `{"schema":"mihari.machine-log-request/v1","to":"2026-09-05T00:00:00Z","path":"private-value"}`,
		"source selection":  `{"schema":"mihari.machine-log-request/v1","to":"2026-09-05T00:00:00Z","source":"tui"}`,
		"duplicate key":     `{"schema":"mihari.machine-log-request/v1","to":"2026-09-05T00:00:00Z","to":"2026-09-05T00:00:00Z"}`,
		"escaped duplicate": `{"schema":"mihari.machine-log-request/v1","to":"2026-09-05T00:00:00Z","t\u006f":"2026-09-05T00:00:00Z"}`,
		"wrong case":        `{"schema":"mihari.machine-log-request/v1","To":"2026-09-05T00:00:00Z"}`,
		"reversed":          `{"schema":"mihari.machine-log-request/v1","from":"2026-09-05T00:00:01Z","to":"2026-09-05T00:00:00Z"}`,
		"too far future":    `{"schema":"mihari.machine-log-request/v1","to":"2026-09-05T00:05:00.000000001Z"}`,
		"non UTC":           `{"schema":"mihari.machine-log-request/v1","to":"2026-09-05T08:00:00+08:00"}`,
		"relative":          `{"schema":"mihari.machine-log-request/v1","to":"now"}`,
		"single digit hour": `{"schema":"mihari.machine-log-request/v1","to":"2026-09-05T0:00:00Z"}`,
		"comma fraction":    `{"schema":"mihari.machine-log-request/v1","to":"2026-09-05T00:00:00,1Z"}`,
		"trailing object":   `{"schema":"mihari.machine-log-request/v1","to":"2026-09-05T00:00:00Z"}{}`,
		"array":             `[]`,
		"null":              `null`,
		"truncated":         `{"schema":"mihari.machine-log-request/v1",`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeMachineLogRequest(strings.NewReader(input), now)
			var api APIError
			if !errors.As(err, &api) || api.Code != CodeInvalidArgument {
				t.Fatalf("want invalid_argument, got %v", err)
			}
			if strings.Contains(err.Error(), "private-value") {
				t.Fatal("request value leaked")
			}
		})
	}
}
