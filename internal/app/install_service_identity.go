package app

type serviceObjectVersion struct{ Name, State, Identity string }

func serviceObjectMatches(versions []serviceObjectVersion, state, identity, recordedBoot, currentBoot string) bool {
	for _, v := range versions {
		if v.State == state && (recordedBoot != currentBoot || v.Identity == identity) {
			return true
		}
	}
	return false
}
