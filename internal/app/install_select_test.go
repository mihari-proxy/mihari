package app

import (
	"github.com/mihari-proxy/mihari/internal/platform"
	"github.com/mihari-proxy/mihari/internal/service"
	"testing"
)

func TestInstallSource_UsesDefinitionOrExplicitOnly(t *testing.T) {
	layout := platform.ResolvedLayout{Data: platform.Paths{Root: "/var/lib/mihari/data"}, InstallRoot: "/usr/local/lib/mihari"}
	for _, tc := range []struct {
		name, explicit, defined, binary, want string
		installed, bad                        bool
	}{
		{name: "explicit-new", explicit: "/home/owner/legacy", want: "/home/owner/legacy"},
		{name: "legacy-service", defined: "/home/owner/.mihari", binary: "/usr/local/bin/mihari", installed: true, want: "/home/owner/.mihari"},
		{name: "no-home-guess", binary: "/usr/local/bin/mihari", installed: true, bad: true},
		{name: "fixed-source", explicit: "/other", defined: "/home/owner/.mihari", installed: true, bad: true},
		{name: "managed-retain", binary: "/usr/local/lib/mihari/mihari", installed: true},
		{name: "same-target", explicit: "/var/lib/mihari/data", bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			def := service.Definition{Status: service.StatusNotInstalled, Binary: tc.binary, Env: []string{"HOME=/do-not-guess", "SUDO_USER=attacker"}}
			if tc.installed {
				def.Status = service.StatusStopped
			}
			if tc.defined != "" {
				def.Env = append(def.Env, "MIHARI_DATA="+tc.defined)
			}
			got, err := selectInstallSource(InstallRequest{Source: tc.explicit}, def, layout)
			if tc.bad {
				if err == nil {
					t.Fatal("unbound source accepted")
				}
			} else if err != nil || got != tc.want {
				t.Fatalf("source=%q err=%v want=%q", got, err, tc.want)
			}
		})
	}
}
