package ui

import (
	"reflect"
	"testing"
)

func TestPageIDsUseAcceptedRailOrder(t *testing.T) {
	want := []PageID{PageOverview, PageProxies, PageConnections, PageRules, PageLogs, PageSubscriptions, PageWebGUI, PageSystem}
	if got := RailPages(); !reflect.DeepEqual(got, want) {
		t.Fatalf("pages=%v want=%v", got, want)
	}
	got := RailPages()
	got[0] = PageSystem
	if RailPages()[0] != PageOverview {
		t.Fatal("rail page result aliases shared state")
	}
}
