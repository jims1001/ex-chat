package modruntime

import (
	"context"

	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
)

const persistedFeatureFlagsQuery = "ent.features.list.v1"

func (r *Runtime) registerPersistedFeatureFlags() error {
	return r.Contracts.RegisterQuery(persistedFeatureFlagsQuery, func(ctx context.Context, query foundation.QueryEnvelope) (any, error) {
		// The authenticated caller supplies tenant scope. Actor claims are context,
		// not proof of global authority; no actor can override the envelope account.
		if query.Validate() != nil || query.QueryType != persistedFeatureFlagsQuery || len(query.Filter) != 0 ||
			len(query.Sort) != 1 || query.Sort[0] != "feature_name" || query.Page != "1" || query.Cursor != "" ||
			len(query.Fields) != 2 || query.Fields[0] != "feature_name" || query.Fields[1] != "enabled" || query.Consistency != "strong" {
			return nil, &ContractFailure{Type: "validation_failed", Message: "persisted feature flags query shape is unsupported"}
		}
		flags, err := repository.NewEnterpriseRepository(r.DB.WithContext(ctx)).PersistedFeatureFlags(query.AccountID)
		if err != nil {
			return nil, &ContractFailure{Type: "dependency_unavailable", Message: "persisted feature flags are unavailable"}
		}
		return flags, nil
	})
}
