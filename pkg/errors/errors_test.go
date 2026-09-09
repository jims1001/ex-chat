package errors_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	apperr "github.com/OracleBetX-Projects/ex-chat/pkg/errors"
)

func TestAppErrorConstructors(t *testing.T) {
	origErr := errors.New("db connection timeout")

	t.Run("NewBadRequest", func(t *testing.T) {
		err := apperr.NewBadRequest("invalid payload", origErr)
		if err.Code != apperr.CodeBadRequest {
			t.Errorf("expected code %s, got %s", apperr.CodeBadRequest, err.Code)
		}
		if err.HTTPStatus != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", err.HTTPStatus)
		}
		if !errors.Is(err, origErr) {
			t.Errorf("expected unwrap to match original error")
		}
	})

	t.Run("NewNotFound", func(t *testing.T) {
		err := apperr.NewNotFound("Conversation", 123)
		if err.HTTPStatus != http.StatusNotFound {
			t.Errorf("expected 404, got %d", err.HTTPStatus)
		}
		if err.Code != apperr.CodeNotFound {
			t.Errorf("expected CodeNotFound, got %s", err.Code)
		}
	})

	t.Run("WithDetails", func(t *testing.T) {
		err := apperr.NewValidationFailed("validation error", map[string]any{"field": "email"})
		if err.HTTPStatus != http.StatusUnprocessableEntity {
			t.Errorf("expected 422, got %d", err.HTTPStatus)
		}
		if err.Details["field"] != "email" {
			t.Errorf("expected details to contain field email")
		}
	})

	t.Run("FromError", func(t *testing.T) {
		if apperr.FromError(nil) != nil {
			t.Errorf("expected nil for nil error")
		}

		plainErr := fmt.Errorf("something broke")
		wrapped := apperr.FromError(plainErr)
		if wrapped.HTTPStatus != http.StatusInternalServerError {
			t.Errorf("expected 500 for generic error")
		}

		// Re-wrapping AppError returns the same instance
		existing := apperr.NewForbidden("denied")
		res := apperr.FromError(existing)
		if res != existing {
			t.Errorf("expected same AppError instance")
		}
	})
}
