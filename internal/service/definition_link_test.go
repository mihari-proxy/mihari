package service

import (
	"errors"
	"os"
	"testing"
)

func TestDefinitionLink_RejectsUnrestorableTarget(t *testing.T) {
	configured := "/fixture/systemd/mihari.service"
	for _, tc := range []struct {
		name, unit, target string
		want               error
	}{
		{"default unit", "", defaultSystemdUnitFile, nil},
		{"configured unit", configured, configured, nil},
		{"mask", configured, defaultDevNull, nil},
		{"not a link", configured, "", os.ErrInvalid},
		{"unconfigured unit", configured, defaultSystemdUnitFile, os.ErrPermission},
		{"arbitrary target", configured, "/root/other.service", os.ErrPermission},
		{"relative target", configured, "../mihari.service", os.ErrPermission},
		{"noncanonical target", configured, "/fixture/systemd/../systemd/mihari.service", os.ErrPermission},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := checkedDefinitionLink(tc.unit, "", tc.target)
			if !errors.Is(err, tc.want) || err == nil && got != tc.target || err != nil && got != "" {
				t.Fatalf("checked link=%q err=%v want=%v", got, err, tc.want)
			}
		})
	}
}

func TestDefinitionLink_UsesConfiguredMaskTarget(t *testing.T) {
	unit, mask := "/fixture/systemd/mihari.service", "/fixture/dev-null"
	for _, target := range []string{unit, mask} {
		got, err := checkedDefinitionLink(unit, mask, target)
		if err != nil || got != target || !allowedDefinitionLink(unit, mask, target) {
			t.Fatalf("configured target rejected: %q err=%v", got, err)
		}
	}
}
