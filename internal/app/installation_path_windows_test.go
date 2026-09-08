package app

import "testing"

func TestInstallationReset_RejectsCaseAliasedCredentialOverlap(t *testing.T) {
	root := `C:\ProgramData\Mihari`
	manifest := InstallationManifest{DataRoot: root, Credential: `c:\programdata\mihari\WEB\control.token`}
	if _, _, err := installationDataEntries(InstallationModeFresh, manifest, nil, nil); err == nil {
		t.Fatal("accepted a case-aliased credential inside a reset entry")
	}
}

func TestInstallationPlan_RejectsCaseAliasedDuplicateEntries(t *testing.T) {
	input := testVerifiedInstallationPlanInput()
	input.Preserve = []InstallationEntry{
		{Path: `C:\ProgramData\Mihari\control.token`, Category: InstallationCategoryCredential},
		{Path: `c:\programdata\mihari\CONTROL.TOKEN`, Category: InstallationCategoryCredential},
	}
	if _, err := BindInstallationPlan(input); err == nil {
		t.Fatal("BindInstallationPlan accepted case-aliased duplicate entries")
	}
}
