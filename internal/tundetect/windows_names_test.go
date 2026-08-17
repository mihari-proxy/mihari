package tundetect

import "testing"

func TestIsWindowsTunAdapter(t *testing.T) {
	tests := []struct {
		name     string
		desc     string
		friendly string
		want     bool
	}{
		{"legacy wintun desc", "Wintun Userspace Tunnel", "Ethernet 2", true},
		{"sparkle meta tunnel", "Meta Tunnel", "mihomo", true},
		{"friendly mihomo only", "Some Vendor NIC", "mihomo", true},
		{"friendly Meta exact", "Virtual", "Meta", true},
		{"wireguard tunnel", "WireGuard Tunnel", "OfficeNet", true},
		{"wlan", "Intel(R) Wi-Fi 6 AX201 160MHz", "WLAN", false},
		{"tap windows", "TAP-Windows Adapter V9", "以太网 8", false},
		{"sangfor", "Sangfor SSL VPN CS Support System VNIC", "以太网 7", false},
		{"netease tap", "Netease UU TAP-Win32 Adapter V9.21", "以太网 6", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isWindowsTunAdapter(tt.desc, tt.friendly); got != tt.want {
				t.Fatalf("got=%v want=%v", got, tt.want)
			}
		})
	}
}

func TestFormatAdapterName(t *testing.T) {
	if got := formatAdapterName("Meta Tunnel", "mihomo"); got != "mihomo (Meta Tunnel)" {
		t.Fatalf("got=%q", got)
	}
	if got := formatAdapterName("Wintun Userspace Tunnel", "Wintun Userspace Tunnel"); got != "Wintun Userspace Tunnel" {
		t.Fatalf("got=%q", got)
	}
	if got := formatAdapterName("Wintun Userspace Tunnel", ""); got != "Wintun Userspace Tunnel" {
		t.Fatalf("got=%q", got)
	}
}
