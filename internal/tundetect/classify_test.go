package tundetect

import (
	"reflect"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name          string
		detection     Detection
		selfTunActive bool
		want          *protocol.TunConflict
	}{
		{
			name:      "empty detection is nil",
			detection: Detection{},
			want:      nil,
		},
		{
			name: "single mihomo is self, subtracts to nil",
			detection: Detection{
				MihomoProcesses: []string{"mihomo"},
			},
			want: nil,
		},
		{
			name: "only other mihomo process (signal B alone)",
			detection: Detection{
				MihomoProcesses: []string{"mihomo", "mihomo-other"},
			},
			want: &protocol.TunConflict{
				OtherMihomoProcesses: []string{"mihomo-other"},
			},
		},
		{
			name: "other tun with self inactive is not subtracted (signal A alone)",
			detection: Detection{
				TunInterfaces: []string{"wintun-other"},
			},
			selfTunActive: false,
			want: &protocol.TunConflict{
				OtherTunInterfaces: []string{"wintun-other"},
			},
		},
		{
			name: "other tun with self active subtracts own adapter",
			detection: Detection{
				TunInterfaces: []string{"wintun-self", "wintun-other"},
			},
			selfTunActive: true,
			want: &protocol.TunConflict{
				OtherTunInterfaces: []string{"wintun-other"},
			},
		},
		{
			name: "single tun equals self when active, subtracts to nil",
			detection: Detection{
				TunInterfaces: []string{"wintun-self"},
			},
			selfTunActive: true,
			want:          nil,
		},
		{
			name: "both signals A and B",
			detection: Detection{
				TunInterfaces:   []string{"wintun-self", "wintun-other"},
				MihomoProcesses: []string{"mihomo", "mihomo-other"},
			},
			selfTunActive: true,
			want: &protocol.TunConflict{
				OtherTunInterfaces:   []string{"wintun-other"},
				OtherMihomoProcesses: []string{"mihomo-other"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.detection, tt.selfTunActive)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got=%#v want=%#v", got, tt.want)
			}
		})
	}
}
