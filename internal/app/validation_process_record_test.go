package app

import (
	"context"
	"errors"
	"syscall"
	"testing"
)

type recordValidationProcessProbe struct {
	validationProcessProbe
	records   []ProcessStartIdentity
	lookupErr error
}

func (p *recordValidationProcessProbe) Identify(_ context.Context, pid int) (ProcessStartIdentity, error) {
	return validationProcessRecord(pid, p.records, p.lookupErr)
}

func TestInstallValidation_ProcessQueryRecovery(t *testing.T) {
	id := ProcessStartIdentity{PID: 18, BootID: testBootID, StartUnix: 200}
	wrong := id
	wrong.PID++
	for _, tc := range []struct {
		name         string
		records      []ProcessStartIdentity
		lookupErr    error
		stops, locks int
		wantErr      bool
	}{
		{name: "zero-records", locks: 1},
		{name: "matching-record", records: []ProcessStartIdentity{id}, stops: 1, locks: 1},
		{name: "genuine-EIO", lookupErr: syscall.EIO, wantErr: true},
		{name: "multiple-records", records: []ProcessStartIdentity{id, id}, wantErr: true},
		{name: "wrong-PID", records: []ProcessStartIdentity{wrong}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &recordValidationProcessProbe{records: tc.records, lookupErr: tc.lookupErr}
			err := recoverValidationProcess(context.Background(), id, InstallJournal{}, p)
			if (err != nil) != tc.wantErr || p.stops != tc.stops || p.locks != tc.locks {
				t.Fatalf("recovery query: err=%v stops=%d locks=%d", err, p.stops, p.locks)
			}
			if tc.lookupErr != nil && !errors.Is(err, tc.lookupErr) {
				t.Fatalf("lookup error lost: %v", err)
			}
		})
	}
}
