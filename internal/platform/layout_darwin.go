package platform

func platformLayoutDefaults(trustedHome string) LayoutDefaults {
	return LayoutDefaults{
		OS:          "darwin",
		BaseDir:     "/Library/Application Support/mihari",
		InstallRoot: "/usr/local/lib/mihari",
		TrustedHome: trustedHome,
		SocketLimit: 103,
	}
}
