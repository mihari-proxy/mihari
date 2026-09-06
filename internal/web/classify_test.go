package web

import (
	"net/http"
	"testing"
)

func TestClassifyTable(t *testing.T) {
	cases := []struct {
		method, path string
		want         Action
	}{
		{http.MethodGet, "/version", ActionProxyRead},
		{http.MethodHead, "/proxies", ActionProxyRead},
		{http.MethodGet, "/rules", ActionProxyRead},
		{http.MethodGet, "/connections", ActionProxyRead},
		{http.MethodGet, "/configs", ActionProxyRead},
		{http.MethodGet, "/traffic", ActionProxyRead},
		{http.MethodGet, "/storage/zashboard", ActionProxyStorage},
		{http.MethodHead, "/storage/zashboard", ActionProxyStorage},
		{http.MethodPut, "/storage/zashboard", ActionProxyStorage},
		{http.MethodDelete, "/storage/metacubexd", ActionProxyStorage},
		{http.MethodPost, "/storage/zashboard", ActionRejectUnknown},
		{http.MethodPut, "/proxies/GLOBAL", ActionMutateSelectProxy},
		{http.MethodDelete, "/connections/abc", ActionMutateClose},
		{http.MethodDelete, "/connections", ActionMutateClose},
		{http.MethodPost, "/proxies/node-1/delay", ActionMutateDelayTest},
		{http.MethodPost, "/restart", ActionMutateRestart},
		{http.MethodPatch, "/configs", ActionMutateConfigs},
		{http.MethodPut, "/configs", ActionMutateConfigs},
		{http.MethodPost, "/upgrade", ActionRejectUpgrade},
		{http.MethodPost, "/upgrade/ui", ActionRejectUpgrade},
		{http.MethodPost, "/proxies/foo/bar", ActionRejectUnknown},
		{http.MethodPut, "/dns/flush", ActionRejectUnknown},
		{http.MethodGet, "/static/app.js", ActionNotAPI},
		{http.MethodGet, "/", ActionNotAPI},
	}
	for _, tc := range cases {
		if got := Classify(tc.method, tc.path); got != tc.want {
			t.Errorf("Classify(%s %s)=%v want %v", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestClassifyUpgradeStreams(t *testing.T) {
	if got := ClassifyUpgrade(http.MethodGet, "/traffic", true); got != ActionProxyWS {
		t.Fatalf("got=%v", got)
	}
	if got := ClassifyUpgrade(http.MethodGet, "/connections", true); got != ActionProxyWS {
		t.Fatalf("got=%v", got)
	}
	if got := ClassifyUpgrade(http.MethodGet, "/connections", false); got != ActionProxyRead {
		t.Fatalf("got=%v", got)
	}
}

func TestClassifyDefaultDenyUnknownWrites(t *testing.T) {
	for _, request := range []struct{ method, path string }{
		{http.MethodPost, "/proxies/foo/bar"},
		{http.MethodPut, "/providers/proxies/remote"},
		{http.MethodPut, "/providers/rules/remote"},
		{http.MethodPost, "/configs/geo"},
	} {
		if got := Classify(request.method, request.path); got != ActionRejectUnknown {
			t.Fatalf("Classify(%s %s)=%v", request.method, request.path, got)
		}
	}
}
