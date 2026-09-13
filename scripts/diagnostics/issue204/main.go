// Command issue204 reproduces provider-only delay failures using an isolated
// fake controller. It does not start mihomo or access real user data/services.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/mihari-proxy/mihari/internal/control/client"
	"github.com/mihari-proxy/mihari/internal/control/protocol"
	"github.com/mihari-proxy/mihari/internal/control/server"
	"github.com/mihari-proxy/mihari/internal/logging"
	"github.com/mihari-proxy/mihari/internal/mihomo"
	"github.com/mihari-proxy/mihari/internal/runtime"
	"github.com/mihari-proxy/mihari/internal/state"
	"github.com/mihari-proxy/mihari/internal/sysproxy"
	"github.com/mihari-proxy/mihari/internal/tundetect"
)

type handlerTransport struct{ handler http.Handler }

func (t handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	t.handler.ServeHTTP(w, r)
	return w.Result(), nil
}

func main() {
	if err := reproduce(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func reproduce() error {
	const testURL = "https://example.invalid/generate_204"
	var requests []string
	// Mirrors mihomo v1.19.30 hub/route/proxies.go and provider.go:
	// provider members appear in group.all, but have a separate lookup namespace.
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.EscapedPath())
		w.Header().Set("Content-Type", "application/json")
		body := `{"message":"Not Found"}`
		switch r.URL.Path {
		case "/proxies":
			body = `{"proxies":{"GLOBAL":{"name":"GLOBAL","type":"Selector","all":["AllProxy"]},"AllProxy":{"name":"AllProxy","type":"Selector","all":["Provider Node","Inline Node"],"testUrl":"` + testURL + `"},"Inline Node":{"name":"Inline Node","type":"Direct","udp":true}}}`
		case "/proxies/Inline Node/delay", "/providers/proxies/subscribe/Provider Node/healthcheck", "/group/AllProxy/delay":
			if r.URL.Query().Get("url") != testURL || r.URL.Query().Get("timeout") != "5000" {
				w.WriteHeader(http.StatusBadRequest)
				break
			}
			body = `{"delay":42}`
			if r.URL.Path == "/group/AllProxy/delay" {
				body = `{"Provider Node":42,"Inline Node":42}`
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
		// In-memory recorder cannot fail its Write.
		_, _ = w.Write([]byte(body))
	})
	coreHTTP := &http.Client{Transport: handlerTransport{upstream}, Timeout: time.Second}
	controller := mihomo.NewClient("http://fake-core.invalid", "fixture-token", coreHTTP)
	store := state.NewStore(state.Snapshot{})
	manager := runtime.New(runtime.Options{Store: store, Controller: controller, SysProxy: &sysproxy.FakeBackend{}, TunDetect: &tundetect.FakeBackend{}})
	var logs bytes.Buffer
	reporter := logging.NewDiagnosticReporter(slog.New(slog.NewJSONHandler(&logs, nil)), logging.NewRedactor())
	control := server.New(server.Options{Token: "fixture-token", Store: store, Runtime: manager, DiagnosticReporter: reporter})
	local := client.NewHTTP("http://fake-control.invalid", "fixture-token", &http.Client{Transport: handlerTransport{control.Handler()}, Timeout: time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	groups, err := local.ProxyGroups(ctx)
	if err != nil || len(groups.Groups) == 0 || len(groups.Groups[0].Nodes) != 2 {
		return fmt.Errorf("unexpected group fixture: %v", err)
	}
	fmt.Printf("Provider node is listed: name=%q type=%q udp=%v\n", groups.Groups[0].Nodes[0].Name, groups.Groups[0].Nodes[0].Type, groups.Groups[0].Nodes[0].UDP)
	for _, explicit := range []bool{false, true} {
		request := protocol.DelayTestRequest{}
		if explicit {
			request.URL = testURL
		}
		_, err := local.DelayProxy(ctx, "Provider Node", request)
		var api protocol.APIError
		if !errors.As(err, &api) || api.Code != protocol.CodeUpstreamFailure || api.Details["status"] != float64(404) {
			return fmt.Errorf("provider failure was not reproduced: %v", err)
		}
		fmt.Printf("Provider delay explicit_url=%v: code=%s upstream_status=%v\n", explicit, api.Code, api.Details["status"])
	}
	inline, err := local.DelayProxy(ctx, "Inline Node", protocol.DelayTestRequest{})
	if err != nil || inline.Delays["Inline Node"] != 42 {
		return fmt.Errorf("inline control failed: %v", err)
	}
	group, err := local.DelayTest(ctx, "AllProxy", protocol.DelayTestRequest{})
	if err != nil || group.Delays["Provider Node"] != 42 {
		return fmt.Errorf("group control failed: %v", err)
	}
	response, err := coreHTTP.Get("http://fake-core.invalid/providers/proxies/subscribe/Provider%20Node/healthcheck?url=https%3A%2F%2Fexample.invalid%2Fgenerate_204&timeout=5000")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	var delay struct{ Delay uint16 }
	if err := json.NewDecoder(response.Body).Decode(&delay); err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK || delay.Delay != 42 {
		return fmt.Errorf("provider endpoint control failed")
	}
	fmt.Println("Inline, group, and provider-specific controls: 42 ms (synthetic)")
	fmt.Printf("Captured diagnostic records:\n%s", logs.String())
	fmt.Printf("Captured upstream paths: %q\n", requests)
	return nil
}
