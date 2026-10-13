package handler

import (
	"encoding/json"
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

// bindCustomAttributesJSON preserves number tokens in arbitrary custom
// attributes, including nested objects and arrays. Keep this request-local:
// changing Gin's global decoder would alter unrelated request semantics.
func bindCustomAttributesJSON(c *gin.Context, value any) error {
	if c.Request == nil || c.Request.Body == nil {
		return errors.New("invalid request")
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.UseNumber()
	if binding.EnableDecoderDisallowUnknownFields {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if binding.Validator == nil {
		return nil
	}
	return binding.Validator.ValidateStruct(value)
}
