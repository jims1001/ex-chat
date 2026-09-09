package pagination_test

import (
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/pkg/pagination"
)

func TestPagination(t *testing.T) {
	t.Run("Params Defaults and Offsets", func(t *testing.T) {
		pDefault := pagination.NewParams(0, 0)
		if pDefault.Page != pagination.DefaultPage || pDefault.PageSize != pagination.DefaultPageSize {
			t.Errorf("expected default page and page size, got %+v", pDefault)
		}
		if pDefault.Offset() != 0 {
			t.Errorf("expected offset 0, got %d", pDefault.Offset())
		}
		if pDefault.Limit() != pagination.DefaultPageSize {
			t.Errorf("expected limit %d, got %d", pagination.DefaultPageSize, pDefault.Limit())
		}

		pCustom := pagination.NewParams(3, 20)
		if pCustom.Offset() != 40 {
			t.Errorf("expected offset 40, got %d", pCustom.Offset())
		}

		pMax := pagination.NewParams(1, 500)
		if pMax.PageSize != pagination.MaxPageSize {
			t.Errorf("expected clamped page size %d, got %d", pagination.MaxPageSize, pMax.PageSize)
		}
	})

	t.Run("Metadata Calculations", func(t *testing.T) {
		meta := pagination.NewMeta(55, 2, 20)
		if meta.TotalPages != 3 {
			t.Errorf("expected 3 total pages for 55 items with size 20, got %d", meta.TotalPages)
		}
		if !meta.HasNext {
			t.Errorf("expected HasNext to be true on page 2")
		}
		if !meta.HasPrev {
			t.Errorf("expected HasPrev to be true on page 2")
		}

		metaLast := pagination.NewMeta(55, 3, 20)
		if metaLast.HasNext {
			t.Errorf("expected HasNext to be false on page 3")
		}
	})

	t.Run("Cursor Pagination", func(t *testing.T) {
		now := time.Now().Unix()
		token := pagination.EncodeCursor(1024, now)
		if len(token) == 0 {
			t.Fatalf("expected non-empty cursor token")
		}

		id, ts, err := pagination.DecodeCursor(token)
		if err != nil {
			t.Fatalf("decode cursor failed: %v", err)
		}
		if id != 1024 || ts != now {
			t.Errorf("expected id=1024, ts=%d; got id=%d, ts=%d", now, id, ts)
		}

		_, _, err = pagination.DecodeCursor("invalid-token")
		if err == nil {
			t.Errorf("expected invalid token to return error")
		}
	})
}
