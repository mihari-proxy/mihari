package app

const binaryStageSchema = "mihari.binary-stage/v1"

type binaryStageMarker struct {
	Schema            string `json:"schema"`
	StageIdentity     string `json:"stage_identity"`
	CandidateIdentity string `json:"candidate_identity"`
	SHA256            string `json:"sha256"`
}

func binaryStageMatches(marker binaryStageMarker, stageID, candidateID, candidateHash string) bool {
	if marker.Schema != binaryStageSchema || marker.StageIdentity == "" || marker.CandidateIdentity == "" || !validSHA256(marker.SHA256) || stageID != marker.StageIdentity {
		return false
	}
	return (candidateHash == "absent" && candidateID == "") || (candidateID == marker.CandidateIdentity && candidateHash == marker.SHA256)
}
