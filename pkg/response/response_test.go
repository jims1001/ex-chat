package response_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/pkg/errors"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestResponse(t *testing.T) {
	t.Run("Success Response", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		response.Success(c, map[string]string{"foo": "bar"})

		if w.Code != http.StatusOK {
			t.Errorf("expected 200 OK, got %d", w.Code)
		}

		var resp response.Response
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if !resp.Success {
			t.Errorf("expected success true")
		}
	})

	t.Run("Created Response", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		response.Created(c, map[string]int{"id": 42})

		if w.Code != http.StatusCreated {
			t.Errorf("expected 201 Created, got %d", w.Code)
		}
	})

	t.Run("Paginated Response", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		items := []string{"item1", "item2"}
		response.Paginated(c, items, 50, 1, 10)

		if w.Code != http.StatusOK {
			t.Errorf("expected 200 OK, got %d", w.Code)
		}

		var resp struct {
			Success bool                   `json:"success"`
			Data    response.PaginatedData `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.Data.Total != 50 || resp.Data.TotalPages != 5 {
			t.Errorf("expected total 50 and total_pages 5, got %+v", resp.Data)
		}
	})

	t.Run("AppError Response", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		appErr := errors.NewNotFound("Contact", 999)
		response.AppError(c, appErr)

		if w.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", w.Code)
		}

		var resp response.Response
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.Success {
			t.Errorf("expected success false")
		}
	})
}
