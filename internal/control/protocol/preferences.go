package protocol

type TUIPreferences struct {
	Schema             string   `json:"schema"`
	Revision           uint64   `json:"revision"`
	ConnectionsColumns []string `json:"connections_columns"`
}

type UpdateTUIPreferencesRequest struct {
	OperationID        string   `json:"operation_id"`
	IfRevision         *uint64  `json:"if_revision,omitempty"`
	ConnectionsColumns []string `json:"connections_columns"`
}
