package platform

func platformLayoutDefaults(trustedHome string) LayoutDefaults {
	return LayoutDefaults{
		OS:          "linux",
		BaseDir:     "/var/lib/mihari",
		InstallRoot: "/usr/local/lib/mihari",
		TrustedHome: trustedHome,
		SocketLimit: 107,
	}
}
