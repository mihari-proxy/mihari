package proxies

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/mihari-proxy/mihari/internal/control/protocol"
)

func concurrencyPreferences(t *testing.T, limit int) protocol.TUIPreferences {
	t.Helper()
	var prefs protocol.TUIPreferences
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"proxies":{"extra_latency":true,"auto_latency_test":true,"latency_test_concurrency":%d}}`, limit)), &prefs); err != nil {
		t.Fatal(err)
	}
	return prefs
}

func TestDelayConcurrency_ManualAndAutomaticUseConfiguredLimit(t *testing.T) {
	for _, limit := range []int{1, 2, 8} {
		for _, automatic := range []bool{false, true} {
			t.Run(fmt.Sprintf("limit=%d/automatic=%t", limit, automatic), func(t *testing.T) {
				m, _ := autoFixture()
				t.Cleanup(m.Stop)
				m.SetSize(160, 50)
				m.SetPreferences(concurrencyPreferences(t, limit))
				if automatic {
					m.ReconcileAutoTests(true, true, 1, protocol.CoreStatus{PID: 1})
				} else {
					m.testAll()
				}
				if len(m.inFlight) != limit || len(m.queue) != 9-limit {
					t.Fatalf("active=%d queued=%d, want %d and %d", len(m.inFlight), len(m.queue), limit, 9-limit)
				}
			})
		}
	}
}

func TestDelayConcurrency_RaisingLimitFillsManualQueueWithAutoDisabled(t *testing.T) {
	m, _ := autoFixture()
	t.Cleanup(m.Stop)
	core := protocol.CoreStatus{PID: 1}
	prefs := concurrencyPreferences(t, 1)
	prefs.Proxies.AutoLatencyTest = false
	m.SetPreferences(prefs)
	m.ReconcileAutoTests(true, true, 1, core)
	m.testAll()
	prefs.Proxies.LatencyTestConcurrency = 3
	m.SetPreferences(prefs)
	if cmd := m.ReconcileAutoTests(true, true, 1, core); cmd == nil || len(m.inFlight) != 3 || len(m.queue) != 6 {
		t.Fatalf("increased limit did not fill manual queue: active=%d queued=%d", len(m.inFlight), len(m.queue))
	}
}

func TestDelayConcurrency_LoweringLimitLetsRunningRequestsFinish(t *testing.T) {
	m, _ := autoFixture()
	t.Cleanup(m.Stop)
	m.testAll()
	m.SetPreferences(concurrencyPreferences(t, 1))
	if len(m.inFlight) != 5 {
		t.Fatal("lowering limit discarded active requests")
	}
	for i := range 5 {
		name := fmt.Sprintf("n%d", i)
		if m.delayTasks[name].cancelled {
			t.Fatal("limit change canceled active work")
		}
		m.Update(delayResultMsg{node: name, gen: m.inFlight[name], delay: 20})
		want := max(1, 4-i)
		if len(m.inFlight) != want {
			t.Fatalf("completion=%d active=%d want=%d", i, len(m.inFlight), want)
		}
	}
	if _, started := m.inFlight["n5"]; !started || len(m.queue) != 3 {
		t.Fatal("queue did not resume with new limit")
	}
}

func TestDelayConcurrency_ManualPrioritySharesAutomaticSlots(t *testing.T) {
	m, _ := autoFixture()
	t.Cleanup(m.Stop)
	m.SetSize(160, 50)
	m.SetPreferences(concurrencyPreferences(t, 2))
	m.ReconcileAutoTests(true, true, 1, protocol.CoreStatus{PID: 1})
	m.testNode("n7")
	if len(m.inFlight) != 2 || m.queue[0] != "n7" {
		t.Fatal("manual test bypassed shared limit or lost priority")
	}
	for name, gen := range m.inFlight {
		m.Update(delayResultMsg{node: name, gen: gen, delay: 20})
		break
	}
	if len(m.inFlight) != 2 || m.delayTasks["n7"] == nil || m.delayTasks["n7"].automatic {
		t.Fatal("manual test did not occupy next available slot")
	}
}
