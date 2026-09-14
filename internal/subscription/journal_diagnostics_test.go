package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestJournalDiagnostics_ParserCausesArePreserved(t *testing.T) {
	for _, resource := range []bool{false, true} {
		name := "provider"
		path := providerJournalPath
		if resource {
			name = "resource"
			path = resourceJournalPath
		}
		t.Run(name, func(t *testing.T) {
			files := newMemoryProviderFiles()
			files.objects[path] = []byte("{")
			store := &ProviderStore{files: files}
			var err error
			if resource {
				_, err = store.loadResourceJournal(context.Background())
			} else {
				err = store.recover(context.Background())
			}
			var syntax *json.SyntaxError
			if !errors.As(err, &syntax) {
				t.Fatalf("JSON parser cause lost: %v", err)
			}
		})
	}
}
