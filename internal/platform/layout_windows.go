package platform

func platformLayoutDefaults(trustedHome string) LayoutDefaults {
	return LayoutDefaults{
		OS:          "windows",
		BaseDir:     targetJoin("windows", trustedHome, ".mihari"),
		InstallRoot: `C:\Program Files\Mihari`,
		TrustedHome: trustedHome,
	}
}
