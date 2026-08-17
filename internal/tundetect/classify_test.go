package tundetect

import (
	"reflect"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name      string
		detection Detection
		self      Self
		want      *protocol.TunConflict
	}{
		{
			name:      "empty detection is nil",
			detection: Detection{},
			want:      nil,
		},
		{
			name: "single mihomo is self, subtracts to nil",
			detection: Detection{
				MihomoProcesses: []Process{{Name: "mihomo", PID: 1}},
			},
			self: Self{CorePID: 1},
			want: nil,
		},
		{
			name: "only other mihomo process (signal B alone)",
			detection: Detection{
				MihomoProcesses: []Process{
					{Name: "mihomo", PID: 1},
					{Name: "mihomo-other", PID: 2},
				},
			},
			self: Self{CorePID: 1},
			want: &protocol.TunConflict{
				OtherMihomoProcesses: []string{"mihomo-other (2)"},
			},
		},
		{
			name: "other tun with self inactive is not subtracted (signal A alone)",
			detection: Detection{
				TunInterfaces: []string{"wintun-other"},
			},
			self: Self{TunActive: false},
			want: &protocol.TunConflict{
				OtherTunInterfaces: []string{"wintun-other"},
			},
		},
		{
			name: "other tun with self active subtracts own adapter",
			detection: Detection{
				TunInterfaces: []string{"wintun-self", "wintun-other"},
			},
			self: Self{TunActive: true, TunName: "wintun-self"},
			want: &protocol.TunConflict{
				OtherTunInterfaces: []string{"wintun-other"},
			},
		},
		{
			name: "single tun equals self when active, subtracts to nil",
			detection: Detection{
				TunInterfaces: []string{"wintun-self"},
			},
			self: Self{TunActive: true, TunName: "wintun-self"},
			want: nil,
		},
		{
			name: "both signals A and B",
			detection: Detection{
				TunInterfaces: []string{"wintun-self", "wintun-other"},
				MihomoProcesses: []Process{
					{Name: "mihomo", PID: 1},
					{Name: "mihomo-other", PID: 2},
				},
			},
			self: Self{TunActive: true, TunName: "wintun-self", CorePID: 1},
			want: &protocol.TunConflict{
				OtherTunInterfaces:   []string{"wintun-other"},
				OtherMihomoProcesses: []string{"mihomo-other (2)"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.detection, tt.self)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got=%#v want=%#v", got, tt.want)
			}
		})
	}
}

func TestClassify_SubtractsByPIDNotPosition(t *testing.T) {
	got := Classify(Detection{
		MihomoProcesses: []Process{
			{Name: "mihomo-alpha.exe", PID: 11220},
			{Name: "mihomo.exe", PID: 13400},
		},
	}, Self{CorePID: 13400})
	if got == nil || len(got.OtherMihomoProcesses) != 1 || got.OtherMihomoProcesses[0] != "mihomo-alpha.exe (11220)" {
		t.Fatalf("got=%#v", got)
	}
}

func TestClassify_DoesNotDropAdapterWhenSelfInactive(t *testing.T) {
	got := Classify(Detection{
		TunInterfaces: []string{"mihomo (Meta Tunnel)"},
	}, Self{TunActive: false, CorePID: 13400})
	if got == nil || len(got.OtherTunInterfaces) != 1 || got.OtherTunInterfaces[0] != "mihomo (Meta Tunnel)" {
		t.Fatalf("got=%#v", got)
	}
}

func TestClassify_SubtractsAdapterByName(t *testing.T) {
	got := Classify(Detection{
		TunInterfaces: []string{"Meta", "mihomo (Meta Tunnel)"},
	}, Self{TunActive: true, TunName: "Meta"})
	if got == nil || len(got.OtherTunInterfaces) != 1 || got.OtherTunInterfaces[0] != "mihomo (Meta Tunnel)" {
		t.Fatalf("got=%#v", got)
	}
}

func TestClassify_UnknownLiveNameDropsOne(t *testing.T) {
	got := Classify(Detection{
		TunInterfaces: []string{"Meta"},
	}, Self{TunActive: true, TunName: ""})
	if got != nil {
		t.Fatalf("got=%#v", got)
	}
}

func TestClassify_UnknownPIDKeepsAllProcesses(t *testing.T) {
	got := Classify(Detection{
		MihomoProcesses: []Process{{Name: "mihomo.exe", PID: 13400}},
	}, Self{})
	if got == nil || len(got.OtherMihomoProcesses) != 1 {
		t.Fatalf("got=%#v", got)
	}
}
