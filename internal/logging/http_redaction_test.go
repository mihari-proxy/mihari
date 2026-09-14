package logging

import (
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/diagnostics"
)

func TestHTTPDiagnostics_PreservesEscapedJSONMemberNames(t *testing.T) {
	for _, key := range []string{`to\u006ben`, `\u0074oken`, `pass\u0077ord`, `\u0041uthorization`, `api\u005fkey`, `coo\u006bie`} {
		t.Run(key, func(t *testing.T) {
			body := `{"message":"upstream rejected","` + key + `":"unregistered-private-value","count":7}`
			got := diagnosticText(&diagnostics.HTTPError{Status: 503, Body: body}, NewRedactor())
			if !strings.Contains(got, "unregistered-private-value") || !strings.Contains(got, "upstream rejected") || !strings.Contains(got, `"count":7`) {
				t.Fatal("original JSON diagnostic was changed")
			}
		})
	}
}
