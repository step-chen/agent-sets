package types

import "fmt"

// RetryableError represents an error that indicates the operation can be retried.
// This is typically used for transient errors like network timeouts, rate limits, or temporary server unavailability.
type RetryableError struct {
	Err error
}

func (e *RetryableError) Error() string {
	return fmt.Sprintf("retryable error: %v", e.Err)
}

func (e *RetryableError) Unwrap() error {
	return e.Err
}

// NewRetryableError wraps an existing error as a RetryableError.
func NewRetryableError(err error) error {
	return &RetryableError{Err: err}
}
