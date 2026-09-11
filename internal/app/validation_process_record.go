package app

// validationProcessRecord checks the cardinality and identity of a PID query.
// Lookup errors must remain distinguishable from a successful empty result.
func validationProcessRecord(pid int, records []ProcessStartIdentity, lookupErr error) (ProcessStartIdentity, error) {
	if lookupErr != nil {
		return ProcessStartIdentity{}, lookupErr
	}
	if len(records) == 0 {
		return ProcessStartIdentity{}, nil
	}
	if len(records) != 1 || records[0].PID != pid || !validProcessStart(records[0]) {
		return ProcessStartIdentity{}, errValidationHandshake
	}
	return records[0], nil
}
