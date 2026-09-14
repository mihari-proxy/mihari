package protocol

type ErrorCode string

const (
	CodeInvalidArgument     ErrorCode = "invalid_argument"
	CodeDaemonUnavailable   ErrorCode = "daemon_unavailable"
	CodeInvalidState        ErrorCode = "invalid_state"
	CodePermissionDenied    ErrorCode = "permission_denied"
	CodeRevisionConflict    ErrorCode = "revision_conflict"
	CodeUpstreamFailure     ErrorCode = "upstream_failure"
	CodeNetworkFailure      ErrorCode = "network_failure"
	CodeDataFailure         ErrorCode = "data_failure"
	CodeInternal            ErrorCode = "internal"
	CodeManagedField        ErrorCode = "managed_field"
	CodeManagedOperation    ErrorCode = "managed_operation"
	CodeUnsupportedMutation ErrorCode = "unsupported_mutation"
	CodeSystemProxyConflict ErrorCode = "system_proxy_conflict"
	CodeSystemProxyNotOwned ErrorCode = "system_proxy_not_owned"
	CodeTunConflict         ErrorCode = "tun_conflict"
)

type APIError struct {
	Code    ErrorCode      `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (e APIError) Error() string { return e.Message }

type apiErrorCause struct {
	public APIError
	cause  error
}

func (e apiErrorCause) Error() string   { return e.public.Error() }
func (e apiErrorCause) Unwrap() []error { return []error{e.public, e.cause} }

func wrapAPIErrorCause(public APIError, cause error) error {
	if cause == nil {
		return public
	}
	return apiErrorCause{public: public, cause: cause}
}

type ErrorEnvelope struct {
	Schema string   `json:"schema"`
	Error  APIError `json:"error"`
}

func NewError(code ErrorCode, message string, details map[string]any) ErrorEnvelope {
	return ErrorEnvelope{
		Schema: "mihari.error/v1",
		Error:  APIError{Code: code, Message: message, Details: details},
	}
}
