package tundetect

import (
	"reflect"
	"testing"
)

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

func TestCollectWindowsTunNames_SkipsDownLeftover(t *testing.T) {
	got := collectWindowsTunNames([]windowsTunCandidate{
		{desc: "Wintun Userspace Tunnel", friendly: "本地连接", operStatus: ifOperStatusDown},
		{desc: "Meta Tunnel", friendly: "Meta", operStatus: ifOperStatusUp},
	})
	want := []string{"Meta (Meta Tunnel)"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%q want=%q", got, want)
	}
}

func TestCollectWindowsTunNames_DownOnlyIsEmpty(t *testing.T) {
	got := collectWindowsTunNames([]windowsTunCandidate{
		{desc: "Wintun Userspace Tunnel", friendly: "本地连接", operStatus: ifOperStatusDown},
	})
	if len(got) != 0 {
		t.Fatalf("got=%q", got)
	}
}

func TestCollectWindowsTunNames_KeepsUpCompeting(t *testing.T) {
	got := collectWindowsTunNames([]windowsTunCandidate{
		{desc: "Meta Tunnel", friendly: "mihomo", operStatus: ifOperStatusUp},
	})
	want := []string{"mihomo (Meta Tunnel)"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%q want=%q", got, want)
	}
}

func TestIsWindowsTunUp(t *testing.T) {
	tests := []struct {
		name       string
		operStatus uint32
		want       bool
	}{
		{"up", ifOperStatusUp, true},
		{"down leftover", ifOperStatusDown, false},
		{"testing", ifOperStatusTesting, false},
		{"unknown", ifOperStatusUnknown, false},
		{"dormant", ifOperStatusDormant, false},
		{"not present", ifOperStatusNotPresent, false},
		{"lower layer down", ifOperStatusLowerLayerDown, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isWindowsTunUp(tt.operStatus); got != tt.want {
				t.Fatalf("got=%v want=%v", got, tt.want)
			}
		})
	}
}
