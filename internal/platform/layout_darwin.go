package platform

func nativeLayoutDefaults(trustedHome string) LayoutDefaults {
	return LayoutDefaults{
		OS:          "darwin",
		BaseDir:     "/Library/Application Support/mihari",
		InstallRoot: "/usr/local/lib/mihari",
		TrustedHome: trustedHome,
		SocketLimit: 103,
	}
}

func nativeRootHome() string { return "/var/root" }
