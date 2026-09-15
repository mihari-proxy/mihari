package system

import "testing"

func TestLocalTaskDiagnostics_PreparationAsyncResultsCarryDistinctValues(t *testing.T) {
	m, _ := replacementFixture(t)
	first := m.startMihariPreparation()
	m.CancelMihariPreparation()
	second := m.startMihariPreparation()
	if first == nil || second == nil {
		t.Fatal("preparation missing")
	}
	b := firstSystemPageResult(t, second).(preparedMihariResultMsg)
	a := firstSystemPageResult(t, first).(preparedMihariResultMsg)
	if a.operation.ID == "" || b.operation.ID == "" || a.operation.ID == b.operation.ID || a.operation.Name != "self.prepare" || b.operation.Name != "self.prepare" {
		t.Fatalf("async preparation metadata missing/mixed: %+v %+v", a.operation, b.operation)
	}
	if a.generation == b.generation {
		t.Fatal("generation guard changed")
	}
}
