package modruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"gorm.io/gorm"
)

const cannedResponsePreview = "ops.canned_response.preview.v1"

var cannedPreviewFields = [...]string{"id", "account_id", "short_code", "object_version", "resolved_content"}

type cannedPreviewView struct {
	ID              uint   `json:"id"`
	AccountID       uint   `json:"account_id"`
	ShortCode       string `json:"short_code"`
	ObjectVersion   int64  `json:"object_version"`
	ResolvedContent string `json:"resolved_content"`
}

func cannedPreviewInvalid(message string) error {
	return &ContractFailure{Type: "validation_failed", Message: message}
}

// Names are flat ASCII identifiers, never property paths or expressions.
func validCannedVariableName(ctx context.Context, name string) (bool, error) {
	if name == "" {
		return false, nil
	}
	for i := 0; i < len(name); i++ {
		if i%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || i > 0 && c >= '0' && c <= '9') {
			return false, nil
		}
	}
	return true, ctx.Err()
}

func cannedPreviewVariables(ctx context.Context, raw any) (map[string]string, error) {
	values, ok := raw.(map[string]any)
	if !ok || values == nil {
		return nil, cannedPreviewInvalid("preview variables must be a string-valued object")
	}
	result := make(map[string]string)
	remaining := repository.CannedResponseReadBytes
	for name, rawValue := range values {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		value, ok := rawValue.(string)
		if !ok || len(name) > remaining {
			return nil, cannedPreviewInvalid("preview variables exceed bounds or have invalid types")
		}
		remaining -= len(name)
		if len(value) > remaining {
			return nil, cannedPreviewInvalid("preview variables exceed bounds or have invalid types")
		}
		remaining -= len(value)
		valid, err := validCannedVariableName(ctx, name)
		if err != nil {
			return nil, err
		}
		if !valid || !utf8.ValidString(value) {
			return nil, cannedPreviewInvalid("preview variables have invalid names or text")
		}
		result[name] = value
	}
	return result, ctx.Err()
}

// renderCannedPreview scans source only. Substitution bytes are never reparsed,
// evaluated, or treated as HTML. A failed render never exposes partial content.
func renderCannedPreview(ctx context.Context, source string, values map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(source) > repository.CannedResponseReadBytes || !utf8.ValidString(source) {
		return "", cannedPreviewInvalid("preview source exceeds bounds or has invalid text")
	}
	var out strings.Builder
	appendText := func(value string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(value) > repository.CannedResponseReadBytes-out.Len() {
			return cannedPreviewInvalid("preview output exceeds bounds")
		}
		out.WriteString(value)
		return nil
	}
	fail := func(reason string, offset int) (string, error) {
		return "", cannedPreviewInvalid(fmt.Sprintf("%s at source byte %d", reason, offset))
	}
	literal := 0
	for i := 0; i < len(source); {
		if i%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		if i+1 >= len(source) {
			i++
			continue
		}
		pair := source[i : i+2]
		if pair == "}}" {
			return fail("malformed preview placeholder", i)
		}
		if pair != "{{" {
			i++
			continue
		}
		if err := appendText(source[literal:i]); err != nil {
			return "", err
		}
		start := i
		i += 2
		nameStart := i
		for i < len(source) && source[i] != '}' {
			if i%4096 == 0 {
				if err := ctx.Err(); err != nil {
					return "", err
				}
			}
			c := source[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || i > nameStart && c >= '0' && c <= '9') {
				return fail("malformed preview placeholder", start)
			}
			i++
		}
		if i == nameStart || i+1 >= len(source) || source[i:i+2] != "}}" {
			return fail("malformed preview placeholder", start)
		}
		value, ok := values[source[nameStart:i]]
		if !ok {
			return fail("missing preview variable", start)
		}
		if err := appendText(value); err != nil {
			return "", err
		}
		i += 2
		literal = i
	}
	if err := appendText(source[literal:]); err != nil {
		return "", err
	}
	return out.String(), ctx.Err()
}

func boundCannedPreview(ctx context.Context, view cannedPreviewView) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// The already bounded fields limit a single encoding to at most sixfold JSON
	// escape growth. Check actual encoded data, including metadata, not just text.
	encoded, err := json.Marshal(view)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(encoded) > repository.CannedResponseReadBytes {
		return cannedPreviewInvalid("encoded preview data exceeds bounds")
	}
	return nil
}

func (r *Runtime) registerCannedResponsePreview() error {
	return r.Contracts.RegisterQuery(cannedResponsePreview, func(ctx context.Context, q foundation.QueryEnvelope) (any, error) {
		userID, ok := cannedResponseID(q.Actor.ID)
		if q.Actor.Type != "User" || q.Actor.AuthSource != "module" || !ok {
			return nil, &ContractFailure{Type: "permission_denied", Message: "canned responses require a trusted module User actor"}
		}
		invalid := func() (any, error) { return nil, cannedPreviewInvalid("canned response preview shape is unsupported") }
		if q.Validate() != nil || q.QueryType != cannedResponsePreview || uint64(q.AccountID) > math.MaxInt64 || q.Consistency != "strong" || len(q.Sort) != 1 || q.Sort[0] != "id" || q.Page != "1" || q.Cursor != "" || len(q.Filter) != 2 || len(q.Fields) != len(cannedPreviewFields) {
			return invalid()
		}
		for i, field := range cannedPreviewFields {
			if q.Fields[i] != field {
				return invalid()
			}
		}
		number, ok := q.Filter["id"].(json.Number)
		if !ok {
			return invalid()
		}
		id, ok := cannedResponseID(string(number))
		if !ok {
			return invalid()
		}
		values, err := cannedPreviewVariables(ctx, q.Filter["variables"])
		if err != nil {
			return nil, err
		}
		dependency, ok := r.Config.Dependencies["TEN"]
		if !ok {
			return nil, &ContractFailure{Type: "dependency_unavailable", Message: "TEN membership dependency is unavailable"}
		}
		if err := requireCannedResponseMember(ctx, newModuleClient("OPS", dependency, r.Config.ContractVersion, r.Config.MaxTimeout), q.AccountID, q.Actor, userID); err != nil {
			return nil, err
		}
		page, err := repository.NewCannedResponseRepository(r.DB).ReadBounded(ctx, q.AccountID, "detail_versioned", id, "", 0, 1)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, &ContractFailure{Type: "resource_not_found", Message: "canned response was not found"}
		}
		if err != nil {
			return nil, &ContractFailure{Type: "dependency_unavailable", Message: "canned response data is unavailable within read bounds"}
		}
		source := page.Items[0]
		content, err := renderCannedPreview(ctx, source.Content, values)
		if err != nil {
			return nil, err
		}
		view := cannedPreviewView{ID: source.ID, AccountID: source.AccountID, ShortCode: source.ShortCode, ObjectVersion: page.DetailVersion, ResolvedContent: content}
		if err := boundCannedPreview(ctx, view); err != nil {
			return nil, err
		}
		return view, nil
	})
}
