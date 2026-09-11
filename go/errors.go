package router

// Refusal codes are stable protocol identifiers. Error text remains diagnostic.
const (
	CodeInternal         = "internal"
	CodeInvalidRequest   = "invalid_request"
	CodeCallerRefused    = "caller_refused"
	CodeUnknownOperation = "unknown_operation"
)

// RemoteError is a service refusal. Code is empty for a legacy text-only reply.
// Unknown codes are retained; callers must treat them as refusals too.
type RemoteError struct {
	Code    string
	Message string
}

func (e *RemoteError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "router: " + e.Code
}

// Err reports refusal even when a newer server sends a code without prose.
func (r Response) Err() error {
	if r.Error == "" && r.Code == "" {
		return nil
	}
	return &RemoteError{Code: r.Code, Message: r.Error}
}
