package app

func retainedDataMatches(expected JournalObject, expectedMarker string, actual JournalObject, actualMarker, boot string) bool {
	if !expected.Present || !actual.Present || expectedMarker != actualMarker {
		return false
	}
	if expected.BootID == boot {
		return expected.Identity == actual.Identity && expected.MountID == actual.MountID
	}
	return validSHA256(expectedMarker)
}
