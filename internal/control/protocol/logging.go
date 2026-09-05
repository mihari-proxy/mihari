package protocol

// LoggingStatus is the complete effective file logging configuration returned by the v1 control API.
type LoggingStatus struct {
	Schema    string `json:"schema"`
	Revision  uint64 `json:"revision"`
	Level     string `json:"level"`
	MaxSizeMB int64  `json:"max_size_mb"`
	MaxFiles  int64  `json:"max_files"`
	Dir       string `json:"dir"`
}

// LoggingUpdateRequest carries an optional partial update for file logging configuration.
type LoggingUpdateRequest struct {
	OperationID string  `json:"operation_id"`
	IfRevision  *uint64 `json:"if_revision,omitempty"`
	Level       *string `json:"level,omitempty"`
	MaxSizeMB   *int64  `json:"max_size_mb,omitempty"`
	MaxFiles    *int64  `json:"max_files,omitempty"`
}
