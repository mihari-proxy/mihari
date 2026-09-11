package update

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func replacementFixture() (ReplacementCandidate, ReplacementSnapshot) {
	return ReplacementCandidate{Version: "v1.2.2", SHA256: strings.Repeat("a", 64), Channel: "main"}, ReplacementSnapshot{
		Targets:                 []ReplacementTarget{{Roles: []string{"service"}, Path: "/private/fixture/mihari", FileID: "file-secret-id", SHA256: strings.Repeat("b", 64), Exists: true, Version: "v1.2.3"}},
		ServiceDefinitionSHA256: strings.Repeat("c", 64),
	}
}
func mustReplacementPreview(t *testing.T, c ReplacementCandidate, s ReplacementSnapshot) ReplacementPreview {
	t.Helper()
	p, err := NewReplacementPreview(c, s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func requireReplacementCode(t *testing.T, err error, code protocol.ErrorCode) {
	t.Helper()
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != code {
		t.Fatalf("err=%v want=%s", err, code)
	}
}
func TestReplacementPreview_Risk(t *testing.T) {
	for _, tc := range []struct {
		name, version string
		exists        bool
		want          ReplacementRisk
	}{
		{"downgrade", "v1.2.3", true, ReplacementDowngrade}, {"upgrade", "v1.2.1", true, ReplacementNone}, {"same", "v1.2.2", true, ReplacementNone}, {"unknown", "dirty", true, ReplacementUnknown}, {"fresh", "", false, ReplacementNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, s := replacementFixture()
			s.Targets[0].Version = tc.version
			s.Targets[0].Exists = tc.exists
			if !tc.exists {
				s.Targets[0].FileID = ""
				s.Targets[0].SHA256 = ""
			}
			p := mustReplacementPreview(t, c, s)
			if p.Risk != tc.want {
				t.Fatalf("risk=%s want=%s", p.Risk, tc.want)
			}
		})
	}
}
func TestReplacementPreview_BindsEveryObservation(t *testing.T) {
	mutations := map[string]func(*ReplacementCandidate, *ReplacementSnapshot){
		"version":  func(c *ReplacementCandidate, s *ReplacementSnapshot) { c.Version = "v1.2.1" },
		"digest":   func(c *ReplacementCandidate, s *ReplacementSnapshot) { c.SHA256 = strings.Repeat("d", 64) },
		"channel":  func(c *ReplacementCandidate, s *ReplacementSnapshot) { c.Channel = "dev" },
		"path":     func(c *ReplacementCandidate, s *ReplacementSnapshot) { s.Targets[0].Path = "/private/other" },
		"identity": func(c *ReplacementCandidate, s *ReplacementSnapshot) { s.Targets[0].FileID = "changed" },
		"content":  func(c *ReplacementCandidate, s *ReplacementSnapshot) { s.Targets[0].SHA256 = strings.Repeat("e", 64) },
		"existence": func(c *ReplacementCandidate, s *ReplacementSnapshot) {
			s.Targets[0].Exists = false
			s.Targets[0].FileID = ""
			s.Targets[0].SHA256 = ""
		},
		"service definition": func(c *ReplacementCandidate, s *ReplacementSnapshot) {
			s.ServiceDefinitionSHA256 = strings.Repeat("f", 64)
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			c, s := replacementFixture()
			p := mustReplacementPreview(t, c, s)
			mutate(&c, &s)
			requireReplacementCode(t, RecheckReplacement(p, c, s), protocol.CodeInvalidState)
			next := mustReplacementPreview(t, c, s)
			requireReplacementCode(t, ValidateReplacementConsent(next, ReplacementConsent{Yes: true, ExpectedPreview: p.ID}), protocol.CodeInvalidState)
		})
	}
}
func TestReplacementPreview_OrderAndOwnership(t *testing.T) {
	c, s := replacementFixture()
	one := s.Targets[0]
	one.Roles = []string{"binary"}
	s.Targets = append(s.Targets, one)
	p := mustReplacementPreview(t, c, s)
	s.Targets[0].Roles[0] = "path"
	if len(p.Snapshot.Targets) != 1 || strings.Join(p.Snapshot.Targets[0].Roles, ",") != "binary,service" {
		t.Fatalf("roles=%v", p.Snapshot.Targets)
	}
	c, s = replacementFixture()
	s.Targets[0].Roles = []string{"service", "binary", "service"}
	q := mustReplacementPreview(t, c, s)
	if p.ID != q.ID || len(p.ID) != 64 {
		t.Fatalf("unstable identity %q/%q", p.ID, q.ID)
	}
}
func TestReplacementConsent_Contract(t *testing.T) {
	c, s := replacementFixture()
	p := mustReplacementPreview(t, c, s)
	requireReplacementCode(t, ValidateReplacementConsent(p, ReplacementConsent{}), protocol.CodeInvalidArgument)
	for _, yes := range []ReplacementConsent{{Yes: true}, {Yes: true, ExpectedPreview: p.ID}} {
		if err := ValidateReplacementConsent(p, yes); err != nil {
			t.Fatal(err)
		}
	}
	for _, bad := range []ReplacementConsent{{ExpectedPreview: p.ID}, {Yes: true, ExpectedPreview: "invalid"}, {Yes: true, ExpectedPreview: strings.Repeat("A", 64)}} {
		requireReplacementCode(t, ValidateReplacementConsent(p, bad), protocol.CodeInvalidArgument)
	}
	s.Targets[0].Version = "v1.0.0"
	p = mustReplacementPreview(t, c, s)
	if err := ValidateReplacementConsent(p, ReplacementConsent{}); err != nil {
		t.Fatal(err)
	}
	requireReplacementCode(t, ValidateReplacementConsent(p, ReplacementConsent{ExpectedPreview: p.ID}), protocol.CodeInvalidArgument)
}
func TestReplacementWarning_SafeAndComplete(t *testing.T) {
	c, s := replacementFixture()
	s.Targets = append(s.Targets, ReplacementTarget{Roles: []string{"binary"}, Path: "/private/unknown", Exists: true, FileID: "unknown-id", SHA256: strings.Repeat("d", 64), Version: "secret\ninvalid"})
	p := mustReplacementPreview(t, c, s)
	if p.Risk != ReplacementDowngrade {
		t.Fatal(p.Risk)
	}
	warning := ReplacementWarning(p)
	for _, part := range []string{"settings", "subscriptions", "state", "generated files", "fail to start", "load data", "data loss", "not a supported configuration migration", "does not roll back disk state", "service", "v1.2.3", "v1.2.2", "unknown"} {
		if !strings.Contains(warning, part) {
			t.Errorf("warning misses %q", part)
		}
	}
	err := ReplacementConfirmationError(p)
	var api protocol.APIError
	if !errors.As(err, &api) {
		t.Fatalf("err=%v", err)
	}
	raw, e := json.Marshal(api)
	if e != nil {
		t.Fatal(e)
	}
	for _, secret := range []string{"/private", "file-secret-id", strings.Repeat("b", 64), "secret\\ninvalid"} {
		if strings.Contains(string(raw), secret) || strings.Contains(warning, secret) {
			t.Errorf("exposed %q", secret)
		}
	}
	if api.Details["reason"] != "replacement_confirmation_required" || api.Details["preview_id"] != p.ID {
		t.Fatal(api.Details)
	}
	c, s = replacementFixture()
	s.Targets[0].Version = ""
	p = mustReplacementPreview(t, c, s)
	if !strings.HasPrefix(ReplacementWarning(p), "Mihari could not determine version compatibility for this replacement.") {
		t.Fatal(ReplacementWarning(p))
	}
}

func TestReplacementPreview_DistinctHardlinkPathsRemainBound(t *testing.T) {
	candidate, snapshot := replacementFixture()
	other := snapshot.Targets[0]
	other.Path = "/private/fixture/path-mihari"
	other.Roles = []string{"path"}
	snapshot.Targets = append(snapshot.Targets, other)
	preview := mustReplacementPreview(t, candidate, snapshot)
	if len(preview.Snapshot.Targets) != 2 {
		t.Fatal("distinct directory entries sharing a file identity were merged")
	}
	for changed := range snapshot.Targets {
		current := snapshot
		current.Targets = append([]ReplacementTarget(nil), snapshot.Targets...)
		current.Targets[changed].FileID = "replacement-same-contents"
		requireReplacementCode(t, RecheckReplacement(preview, candidate, current), protocol.CodeInvalidState)
	}
}
