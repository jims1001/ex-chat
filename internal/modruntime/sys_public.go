package modruntime

import (
	"context"

	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
)

const publicSystemConfigurationQuery = "sys.configuration.get.v1"

func (r *Runtime) registerPublicSystemConfiguration() error {
	return r.Contracts.RegisterQuery(publicSystemConfigurationQuery, func(ctx context.Context, query foundation.QueryEnvelope) (any, error) {
		// This single global public view has no privileged variant. Actor and
		// account are transport context, not proof of installation authority.
		if query.Validate() != nil || query.QueryType != publicSystemConfigurationQuery || len(query.Filter) != 0 ||
			len(query.Sort) != 1 || query.Sort[0] != "config_key" || query.Page != "1" || query.Cursor != "" ||
			len(query.Fields) != 2 || query.Fields[0] != "config_key" || query.Fields[1] != "value" || query.Consistency != "strong" {
			return nil, &ContractFailure{Type: "validation_failed", Message: "public configuration query shape is unsupported"}
		}
		view, err := repository.NewSystemConfigRepository(r.DB.WithContext(ctx)).PublicBrandingSnapshot()
		if err != nil {
			return nil, &ContractFailure{Type: "dependency_unavailable", Message: "public configuration is unavailable"}
		}
		return view, nil
	})
}
