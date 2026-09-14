package archive

import (
	"archive/zip"
	"errors"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestArchiveOriginal_ParserCausesArePreserved(t *testing.T) {
	if err := ExtractTarGzipBytes([]byte("not gzip"), Limits{}, nil, nil); !hasPrivateCause(err) || err.Error() != "invalid install archive" {
		t.Fatalf("gzip cause or public message changed: %v", err)
	}
	if err := ExtractZipBytes([]byte("not zip"), Limits{}, nil, nil); !errors.Is(err, zip.ErrFormat) || err.Error() != "invalid panel archive" {
		t.Fatalf("zip cause or public message changed: %v", err)
	}
}

func hasPrivateCause(err error) bool {
	if err == nil {
		return false
	}
	if _, public := err.(protocol.APIError); !public {
		if _, multi := err.(interface{ Unwrap() []error }); !multi {
			return true
		}
	}
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		for _, child := range wrapped.Unwrap() {
			if hasPrivateCause(child) {
				return true
			}
		}
	case interface{ Unwrap() error }:
		return hasPrivateCause(wrapped.Unwrap())
	}
	return false
}
