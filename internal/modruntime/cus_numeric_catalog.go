package modruntime

import (
	"context"
	"errors"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"gorm.io/gorm"
)

// CUS alone reads its definition catalog. No values, defaults, arbitrary metadata
// or unbounded catalog list is returned by this owner projection.
type automationCustomDefinitionView struct {
	ID                   uint   `json:"id"`
	AccountID            uint   `json:"account_id"`
	AttributeKey         string `json:"attribute_key"`
	AttributeModel       string `json:"attribute_model"`
	AttributeDisplayType string `json:"attribute_display_type"`
}

func (r *Runtime) registerAutomationNumericCatalog() error {
	return r.Contracts.RegisterQuery("cus.automation.number_definition.get.v1", func(ctx context.Context, q foundation.QueryEnvelope) (any, error) {
		if q.Actor.Type != "System" || q.Actor.AuthSource != "module" {
			return nil, &ContractFailure{Type: "permission_denied", Message: "number catalog requires a module actor"}
		}
		id, err := uintFilter(q.Filter, "id")
		if err != nil {
			return nil, &ContractFailure{Type: "validation_failed", Message: "definition id is required"}
		}
		var row domain.CustomAttributeDefinition
		err = r.DB.WithContext(ctx).Select("id, account_id, attribute_key, attribute_model, attribute_display_type").Where("account_id = ? AND id = ?", q.AccountID, id).First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, &ContractFailure{Type: "resource_not_found", Message: "number definition was not found"}
		}
		if err != nil {
			return nil, &ContractFailure{Type: "dependency_unavailable", Message: "number catalog is unavailable"}
		}
		var count int64
		if err = r.DB.WithContext(ctx).Model(&domain.CustomAttributeDefinition{}).Where("account_id = ? AND attribute_model = ? AND attribute_key = ?", q.AccountID, row.AttributeModel, row.AttributeKey).Count(&count).Error; err != nil {
			return nil, &ContractFailure{Type: "dependency_unavailable", Message: "number catalog is unavailable"}
		}
		if count != 1 {
			return nil, &ContractFailure{Type: "validation_failed", Message: "number definition is ambiguous"}
		}
		return automationCustomDefinitionView{row.ID, row.AccountID, row.AttributeKey, row.AttributeModel, row.AttributeDisplayType}, nil
	})
}
