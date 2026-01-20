package types

import (
	"errors"
	"testing"
)

func TestRetryableError(t *testing.T) {
	baseErr := errors.New("base error")
	retryErr := NewRetryableError(baseErr)

	// Test Error() string
	expectedMsg := "retryable error: base error"
	if retryErr.Error() != expectedMsg {
		t.Errorf("expected error message %q, got %q", expectedMsg, retryErr.Error())
	}

	// Test Unwrap()
	unwrapped := errors.Unwrap(retryErr)
	if unwrapped != baseErr {
		t.Errorf("expected unwrapped error to be %v, got %v", baseErr, unwrapped)
	}

	// Test errors.As
	var target *RetryableError
	if !errors.As(retryErr, &target) {
		t.Error("expected errors.As to match RetryableError")
	}

	// Test errors.Is (semantics check via Unwrap)
	if !errors.Is(retryErr, baseErr) {
		t.Error("expected errors.Is to match base error")
	}
}
