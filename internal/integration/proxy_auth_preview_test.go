package integration

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mihari-proxy/mihari/internal/config"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	runtimeapi "github.com/mihari-proxy/mihari/internal/runtime"
)

func TestProxyAuthPreview_NativeIPCDoesNotSaveOrPublishDraft(t *testing.T) {
	f := newOperationDiagnosticsIPCFixture(t, config.CommitResult{Committed: true}, nil, func(o *runtimeapi.Options) {
		o.Settings.SetProxyAuth(protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "required", Users: []protocol.ProxyAuthUser{{Username: "fixture", Password: "saved-secret"}}})
	})
	before := f.manager.Snapshot()
	request := protocol.ProxyAuthPreviewRequest{Config: protocol.ProxyAuthConfig{Mode: "enabled", BypassMode: "required", Users: []protocol.ProxyAuthUser{{Username: "fixture", Password: "draft-secret"}}}}
	for _, reveal := range []bool{false, true} {
		request.Reveal = reveal
		preview, err := f.client.PreviewProxyAuth(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		var rendered strings.Builder
		for _, line := range preview.Lines {
			rendered.WriteString(line.Text)
		}
		if strings.Contains(rendered.String(), "draft-secret") != reveal || strings.Contains(rendered.String(), "saved-secret") != reveal {
			t.Fatalf("reveal=%t preview=%s", reveal, rendered.String())
		}
	}
	status, err := f.client.ProxyAuth(t.Context(), true)
	if err != nil || status.Config.Users[0].Password != "saved-secret" || f.saver.CallCount() != 0 || !reflect.DeepEqual(before, f.manager.Snapshot()) {
		t.Fatalf("preview changed state: err=%v saves=%d", err, f.saver.CallCount())
	}
}
