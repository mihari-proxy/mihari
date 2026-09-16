package protocol

import "time"

// CapabilityDiagnostics identifies original diagnostic details and process history.
const CapabilityDiagnostics = "diagnostics-v1"

// DiagnosticCapabilityHeader opts a stream into terminal diagnostic references.
const DiagnosticCapabilityHeader = "X-Mihari-Diagnostics"

// DiagnosticState describes availability independently of the business outcome.
type DiagnosticState string

const (
	DiagnosticAvailable   DiagnosticState = "available"
	DiagnosticReference   DiagnosticState = "reference"
	DiagnosticUnsupported DiagnosticState = "unsupported"
	DiagnosticExpired     DiagnosticState = "expired"
	DiagnosticUnknown     DiagnosticState = "unknown"
	DiagnosticRestarted   DiagnosticState = "restarted"
	DiagnosticUnavailable DiagnosticState = "unavailable"
)

// Diagnostic is an immutable snapshot of one occurrence, never a serialized error.
// Detail preserves collected original text, including any credentials it contains.
type Diagnostic struct {
	ID               string          `json:"id,omitempty"`
	InstanceID       string          `json:"instance_id,omitempty"`
	Sequence         uint64          `json:"sequence,omitempty"`
	Time             time.Time       `json:"time,omitzero"`
	Severity         string          `json:"severity,omitempty"`
	Component        string          `json:"component,omitempty"`
	Event            string          `json:"event,omitempty"`
	OperationID      string          `json:"operation_id,omitempty"`
	Operation        string          `json:"operation,omitempty"`
	Object           string          `json:"object,omitempty"`
	Code             ErrorCode       `json:"code,omitempty"`
	Summary          string          `json:"summary,omitempty"`
	Detail           string          `json:"detail,omitempty"`
	State            DiagnosticState `json:"state,omitempty"`
	Truncated        bool            `json:"truncated,omitempty"`
	TruncationReason string          `json:"truncation_reason,omitempty"`
	RetrievalError   string          `json:"retrieval_error,omitempty"`
}

// Reference returns metadata for fetching the unmodified detail separately.
func (d Diagnostic) Reference() Diagnostic {
	d.Detail = ""
	d.State = DiagnosticReference
	return d
}

// Warning accompanies a committed result without changing its success semantics.
type Warning struct {
	Code       ErrorCode   `json:"code,omitempty"`
	Message    string      `json:"message"`
	Diagnostic *Diagnostic `json:"diagnostic,omitempty"`
}

// MaxWarnings bounds one response's warning metadata independently of detail size.
const MaxWarnings = 64

// WarningOutcome is optional diagnostic data attached to a business result.
type WarningOutcome struct {
	Warnings        []Warning `json:"warnings,omitempty"`
	WarningsOmitted uint64    `json:"warnings_omitted,omitempty"`
}

// DiagnosticWarnings provides typed access at client and renderer boundaries.
func (o *WarningOutcome) DiagnosticWarnings() *WarningOutcome { return o }

// Clone isolates the slice and snapshot pointers from cached or concurrent results.
func (o WarningOutcome) Clone() WarningOutcome {
	copy := WarningOutcome{WarningsOmitted: o.WarningsOmitted}
	if o.Warnings != nil {
		copy.Warnings = make([]Warning, len(o.Warnings))
	}
	for i, warning := range o.Warnings {
		copy.Warnings[i] = warning
		if warning.Diagnostic != nil {
			snapshot := *warning.Diagnostic
			copy.Warnings[i].Diagnostic = &snapshot
		}
	}
	return copy
}

// References avoids retaining large bodies in an operation replay cache.
// An occurrence without a queryable ID retains its only available detail.
func (o WarningOutcome) References() WarningOutcome {
	copy := o.Clone()
	for i := range copy.Warnings {
		if snapshot := copy.Warnings[i].Diagnostic; snapshot != nil && snapshot.ID != "" {
			ref := snapshot.Reference()
			copy.Warnings[i].Diagnostic = &ref
		}
	}
	return copy
}

// Append combines child outcomes with explicit bounded collection loss.
func (o *WarningOutcome) Append(next WarningOutcome) {
	if len(o.Warnings) > MaxWarnings {
		o.WarningsOmitted += uint64(len(o.Warnings) - MaxWarnings)
		o.Warnings = append([]Warning(nil), o.Warnings[:MaxWarnings]...)
	}
	o.WarningsOmitted += next.WarningsOmitted
	for _, warning := range next.Warnings {
		if len(o.Warnings) == MaxWarnings {
			o.WarningsOmitted++
			continue
		}
		if warning.Diagnostic != nil {
			snapshot := *warning.Diagnostic
			warning.Diagnostic = &snapshot
		}
		o.Warnings = append(o.Warnings, warning)
	}
}

// DiagnosticList is a bounded metadata page from one daemon instance.
type DiagnosticList struct {
	Schema         string          `json:"schema"`
	InstanceID     string          `json:"instance_id"`
	OldestSequence uint64          `json:"oldest_sequence"`
	LatestSequence uint64          `json:"latest_sequence"`
	NextSequence   uint64          `json:"next_sequence"`
	LostBefore     bool            `json:"lost_before"`
	HasMore        bool            `json:"has_more"`
	Records        []Diagnostic    `json:"records"`
	State          DiagnosticState `json:"state,omitempty"`
}

// DiagnosticResult distinguishes expiry and restart from a failed business action.
type DiagnosticResult struct {
	Schema     string          `json:"schema"`
	State      DiagnosticState `json:"state"`
	Diagnostic *Diagnostic     `json:"diagnostic,omitempty"`
}
