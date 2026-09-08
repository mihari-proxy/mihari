package platform

import (
	"context"
	"errors"
	"testing"
)

func TestDarwinGroupHasPeers_ObservedBoundary(t *testing.T) {
	const owner = 321
	queryFailure := errors.New("query failed")
	for _, test := range []struct {
		name      string
		pids      [2]int32
		bytes     int
		queryErr  error
		wantPeers bool
		wantError bool
	}{
		{name: "only owner", pids: [2]int32{owner}, bytes: 4},
		{name: "owner and child", pids: [2]int32{owner, 322}, bytes: 8, wantPeers: true},
		{name: "child precedes owner", pids: [2]int32{322, owner}, bytes: 8, wantPeers: true},
		{name: "full sample of descendants", pids: [2]int32{322, 323}, bytes: 8, wantPeers: true},
		{name: "no owner", bytes: 0, wantError: true},
		{name: "short sample missing owner", pids: [2]int32{322}, bytes: 4, wantError: true},
		{name: "partial PID", pids: [2]int32{owner}, bytes: 3, wantError: true},
		{name: "partial second PID", pids: [2]int32{owner, 322}, bytes: 7, wantError: true},
		{name: "oversized result", pids: [2]int32{owner, 322}, bytes: 12, wantError: true},
		{name: "negative result", bytes: -1, wantError: true},
		{name: "zero PID", pids: [2]int32{owner, 0}, bytes: 8, wantError: true},
		{name: "negative PID", pids: [2]int32{owner, -1}, bytes: 8, wantError: true},
		{name: "duplicate PID", pids: [2]int32{owner, owner}, bytes: 8, wantError: true},
		{name: "query failure with plausible output", pids: [2]int32{owner}, bytes: 4, queryErr: queryFailure, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			peers, err := darwinGroupHasPeers(context.Background(), owner, owner, func(group int, pids *[2]int32) (int, error) {
				if group != owner {
					t.Fatalf("queried unrelated process group %d", group)
				}
				*pids = test.pids
				return test.bytes, test.queryErr
			})
			if peers != test.wantPeers || (err != nil) != test.wantError {
				t.Fatalf("peers=%v err=%v, want peers=%v error=%v", peers, err, test.wantPeers, test.wantError)
			}
			if test.queryErr != nil && !errors.Is(err, test.queryErr) {
				t.Fatalf("lost query error: %v", err)
			}
		})
	}
}

func TestDarwinGroupHasPeers_RejectsUnownedGroup(t *testing.T) {
	for _, test := range []struct {
		name       string
		pid, group int
	}{
		{name: "inherited parent group", pid: 322, group: 321},
		{name: "zero group", pid: 321, group: 0},
		{name: "init group", pid: 1, group: 1},
		{name: "invalid PID", pid: -1, group: -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			peers, err := darwinGroupHasPeers(context.Background(), test.pid, test.group, func(int, *[2]int32) (int, error) {
				t.Fatal("unowned process group was queried")
				return 0, nil
			})
			if err == nil || peers {
				t.Fatalf("peers=%v err=%v", peers, err)
			}
		})
	}
}

func TestDarwinGroupHasPeers_Cancellation(t *testing.T) {
	for _, duringQuery := range []bool{false, true} {
		t.Run(map[bool]string{false: "before query", true: "during query"}[duringQuery], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if !duringQuery {
				cancel()
			}
			peers, err := darwinGroupHasPeers(ctx, 321, 321, func(_ int, pids *[2]int32) (int, error) {
				if !duringQuery {
					t.Fatal("query ran after cancellation")
				}
				pids[0] = 321
				cancel()
				return 4, nil
			})
			if !errors.Is(err, context.Canceled) || peers {
				t.Fatalf("peers=%v err=%v", peers, err)
			}
		})
	}
}
