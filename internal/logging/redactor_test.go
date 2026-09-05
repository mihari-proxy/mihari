package logging

import (
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestRedactor_CredentialHistorySurvivesReplacement(t *testing.T) {
	r := NewRedactor("ordinary-secret")
	r.RetainCredential(testControllerHex)
	r.RetainCredential(testControlToken)
	r.ReplaceExact([]string{"new-configured-secret"})
	for _, value := range []string{testControllerHex, testControlToken} {
		if r.String("prefix"+value+"suffix") != "prefix***suffix" {
			t.Fatal("embedded used credential was not retained")
		}
	}
	if r.String("ordinary-secret") != "ordinary-secret" {
		t.Fatal("ordinary replacement semantics changed")
	}
	if r.String("new-configured-secret") != "***" {
		t.Fatal("new configured secret missing")
	}
}

func TestRedactor_ConcurrentCredentialAndSnapshotUpdates(t *testing.T) {
	r := NewRedactor()
	var wg sync.WaitGroup
	for _, value := range []string{testControllerHex, testControlToken} {
		wg.Go(func() {
			for range 100 {
				r.RetainCredential(value)
				r.ReplaceExact([]string{"ordinary-secret"})
				_ = r.String("prefix" + value + "suffix")
			}
		})
	}
	wg.Wait()
	r.ReplaceExact(nil)
	for _, value := range []string{testControllerHex, testControlToken} {
		if r.String("prefix"+value+"suffix") != "prefix***suffix" {
			t.Fatal("concurrent history update was lost")
		}
	}
}

func TestRedactorValue_RecursesWithoutMutationAndPreservesNumbers(t *testing.T) {
	hex := testControllerHex
	original := map[string]any{
		"token": "plain-secret",
		"items": []any{
			map[string]any{"url": "visit https://example.test/private?q=token", "count": json.Number("9007199254740993")},
			"Authorization: Bearer hidden-value",
			hex,
		},
	}
	redacted, changed := NewRedactor().Value(original)
	if !changed {
		t.Fatal("changed = false, want true")
	}
	got := redacted.(map[string]any)
	if got["token"] != redactedExact {
		t.Fatalf("token = %#v", got["token"])
	}
	items := got["items"].([]any)
	nested := items[0].(map[string]any)
	if nested["url"] != "visit [REDACTED_URL]" || nested["count"] != json.Number("9007199254740993") {
		t.Fatalf("nested = %#v", nested)
	}
	if strings.Contains(items[1].(string), "hidden-value") || items[2] != redactedExact {
		t.Fatalf("items not redacted: %#v", items)
	}
	if original["token"] != "plain-secret" || original["items"].([]any)[1] != "Authorization: Bearer hidden-value" {
		t.Fatalf("original mutated: %#v", original)
	}
}

func TestRedactorValue_ReportsOneRecordChange(t *testing.T) {
	value := map[string]any{"a": "https://one.test/x", "nested": []any{"https://two.test/y", map[string]any{"password": "secret"}}}
	_, changed := NewRedactor().Value(value)
	if !changed {
		t.Fatal("changed = false")
	}
	clean := map[string]any{"count": json.Number("7"), "ok": true, "nil": nil}
	got, changed := NewRedactor().Value(clean)
	if changed {
		t.Fatalf("clean value reported changed: %#v", got)
	}
	if got.(map[string]any)["count"] != json.Number("7") {
		t.Fatalf("json.Number changed: %#v", got)
	}
}

const (
	testControlToken    = "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"
	testControllerHex   = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testWebCredential   = "web-credential-value-not-sixty-four-hex!!"
	testSubscriptionURL = "https://example.test/sub/abc?token=sub-secret-value"
)

type sampleLogValuer struct {
	text string
}

func (v sampleLogValuer) LogValue() slog.Value {
	return slog.StringValue(v.text)
}

func TestRedactor_ExactSecrets(t *testing.T) {
	redactor := NewRedactor(testControlToken, testWebCredential, testSubscriptionURL)

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "control token",
			input: "auth failed for " + testControlToken,
			want:  "auth failed for ***",
		},
		{
			name:  "web credential non-64-hex",
			input: "login with " + testWebCredential,
			want:  "login with ***",
		},
		{
			name:  "registered subscription url",
			input: "refresh " + testSubscriptionURL,
			want:  "refresh ***",
		},
		{
			name:  "unregistered url uses taxonomy marker",
			input: "fetch https://cdn.example.test/file.bin?sig=1",
			want:  "fetch [REDACTED_URL]",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := redactor.String(test.input)
			if got != test.want {
				t.Fatalf("String() = %q, want %q", got, test.want)
			}
			assertNotContains(t, got, testControlToken, testWebCredential, testSubscriptionURL, "cdn.example.test")
		})
	}
}

func TestRedactor_SensitiveKeys(t *testing.T) {
	redactor := NewRedactor()
	keys := []string{"secret", "token", "authorization", "password", "credential", "cookie", "api-key", "API_KEY", "Token"}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			attr := redactor.ReplaceAttr(nil, slog.String(key, "visible-secret-value"))
			if attr.Value.Kind() != slog.KindString || attr.Value.String() != "***" {
				t.Fatalf("ReplaceAttr(%q) = %#v, want ***", key, attr)
			}
			assertNotContains(t, attr.Value.String(), "visible-secret-value")
		})
	}
}

func TestRedactor_GenericURLAndAuthAndHex(t *testing.T) {
	redactor := NewRedactor()
	hex64 := testControllerHex

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "https url",
			input: "open https://user:pass@example.test/path?x=1",
			want:  "open [REDACTED_URL]",
		},
		{
			name:  "http url",
			input: "see http://example.test/a",
			want:  "see [REDACTED_URL]",
		},
		{
			name:  "ws url",
			input: "dial ws://example.test/ws",
			want:  "dial [REDACTED_URL]",
		},
		{
			name:  "wss url",
			input: "dial wss://example.test/ws",
			want:  "dial [REDACTED_URL]",
		},
		{
			name:  "bearer",
			input: "Authorization Bearer super-bearer-token-value",
			want:  "Authorization Bearer ***",
		},
		{
			name:  "basic",
			input: "proxy Basic dXNlcjpwYXNz",
			want:  "proxy Basic ***",
		},
		{
			name:  "token equals",
			input: "query token=leaked-token-value",
			want:  "query token=***",
		},
		{
			name:  "password colon",
			input: "cfg password:leaked-password-value",
			want:  "cfg password:***",
		},
		{
			name:  "snake case api key",
			input: "cfg api_key=leaked-api-key-value",
			want:  "cfg api_key=***",
		},
		{
			name:  "quoted token",
			input: `cfg token="leaked token value"`,
			want:  "cfg token=***",
		},
		{
			name:  "quoted token with escape",
			input: `cfg token="leaked \"nested\" value"`,
			want:  "cfg token=***",
		},
		{
			name:  "cookie header",
			input: "request Cookie: session=leaked-cookie-value; preference=also-secret",
			want:  "request Cookie:***",
		},
		{
			name:  "64 hex",
			input: "secret=" + hex64,
			want:  "secret=***",
		},
		{
			name:  "standalone 64 hex",
			input: "id " + hex64 + " ok",
			want:  "id *** ok",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := redactor.String(test.input)
			if got != test.want {
				t.Fatalf("String() = %q, want %q", got, test.want)
			}
			assertNotContains(t, got, "example.test", "super-bearer-token-value", "dXNlcjpwYXNz", "leaked-token-value", "leaked-password-value", "leaked-api-key-value", "leaked token value", "leaked-cookie-value", "also-secret", hex64)
		})
	}
}

func TestRedactor_ShortTokenNotFalsePositive(t *testing.T) {
	redactor := NewRedactor()
	input := "status=ok id=ab12 code=1"
	got := redactor.String(input)
	if got != input {
		t.Fatalf("short tokens were altered: got %q", got)
	}
}

func TestRedactor_TooShortExactIgnored(t *testing.T) {
	redactor := NewRedactor("ok", "ab", "", "tok")
	input := "status=ok ab tok ready"
	got := redactor.String(input)
	if got != input {
		t.Fatalf("too-short exact values must not enter set: got %q", got)
	}
}

func TestRedactor_ReplaceExactUpdatesSnapshot(t *testing.T) {
	redactor := NewRedactor()
	secret := "registered-later-credential-xyz"
	if strings.Contains(redactor.String("x "+secret), "***") {
		t.Fatal("unregistered credential must not redact yet")
	}
	redactor.ReplaceExact([]string{"", secret, "no"})
	got := redactor.String("x " + secret)
	if got != "x ***" {
		t.Fatalf("ReplaceExact String() = %q, want %q", got, "x ***")
	}
	// "no" is too short / must not wipe normal text
	if redactor.String("no problem") != "no problem" {
		t.Fatalf("short exact must stay dropped after ReplaceExact")
	}
}

func TestRedactor_ReplaceAttrGroupLogValuerError(t *testing.T) {
	redactor := NewRedactor(testWebCredential)
	attr := redactor.ReplaceAttr(nil, slog.Group("auth",
		slog.String("token", "nested-token-value"),
		slog.String("note", "user "+testWebCredential),
		slog.Any("err", errors.New("boom "+testWebCredential)),
		slog.Any("detail", sampleLogValuer{text: "lv " + testWebCredential}),
		slog.Int("count", 3),
		slog.Bool("ok", true),
	))
	if attr.Value.Kind() != slog.KindGroup {
		t.Fatalf("want group, got %v", attr.Value.Kind())
	}
	children := attr.Value.Group()
	byKey := map[string]slog.Value{}
	for _, child := range children {
		byKey[child.Key] = child.Value
	}
	if byKey["token"].String() != "***" {
		t.Fatalf("sensitive nested key = %q", byKey["token"].String())
	}
	if byKey["note"].String() != "user ***" {
		t.Fatalf("note = %q", byKey["note"].String())
	}
	if byKey["err"].String() != "boom ***" {
		t.Fatalf("err = %q", byKey["err"].String())
	}
	if byKey["detail"].String() != "lv ***" {
		t.Fatalf("detail = %q", byKey["detail"].String())
	}
	if byKey["count"].Int64() != 3 || !byKey["ok"].Bool() {
		t.Fatalf("non-sensitive number/bool changed: count=%v ok=%v", byKey["count"], byKey["ok"])
	}
	for _, child := range children {
		assertNotContains(t, child.Value.String(), testWebCredential, "nested-token-value")
	}
}

func TestRedactor_TaxonomyNotCollapsed(t *testing.T) {
	redactor := NewRedactor(testWebCredential)
	got := redactor.String("cred=" + testWebCredential + " url=https://example.test/x")
	if !strings.Contains(got, "***") {
		t.Fatalf("exact must use ***: %q", got)
	}
	if !strings.Contains(got, "[REDACTED_URL]") {
		t.Fatalf("url must use [REDACTED_URL]: %q", got)
	}
	withoutURLMarker := strings.ReplaceAll(got, "[REDACTED_URL]", "")
	if strings.Contains(withoutURLMarker, "[REDACTED]") {
		t.Fatalf("bare [REDACTED] present: %q", got)
	}
}

func assertNotContains(t *testing.T, haystack string, needles ...string) {
	t.Helper()
	for _, needle := range needles {
		if needle != "" && strings.Contains(haystack, needle) {
			t.Fatalf("output %q still contains %q", haystack, needle)
		}
	}
}
