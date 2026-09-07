package app

import "testing"

func TestBinaryStageCleanup_OnlyRecordedCrashCandidate(t *testing.T) {
	marker := binaryStageMarker{Schema: binaryStageSchema, StageIdentity: "10:11@12", CandidateIdentity: "10:13@12", SHA256: sha256Hex("candidate")}
	for _, tc := range []struct {
		name, stage, candidate, hash string
		marker                       binaryStageMarker
		want                         bool
	}{
		{"verified crash candidate", marker.StageIdentity, marker.CandidateIdentity, marker.SHA256, marker, true},
		{"published candidate absent", marker.StageIdentity, "", "absent", marker, true},
		{"replaced directory", "10:99@12", marker.CandidateIdentity, marker.SHA256, marker, false},
		{"replaced candidate", marker.StageIdentity, "10:99@12", marker.SHA256, marker, false},
		{"modified candidate", marker.StageIdentity, marker.CandidateIdentity, sha256Hex("changed"), marker, false},
		{"missing private marker", marker.StageIdentity, marker.CandidateIdentity, marker.SHA256, binaryStageMarker{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := binaryStageMatches(tc.marker, tc.stage, tc.candidate, tc.hash); got != tc.want {
				t.Fatalf("cleanup authorization=%v, want %v", got, tc.want)
			}
		})
	}
}
