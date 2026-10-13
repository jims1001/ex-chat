package handler

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/gin-gonic/gin"
)

func reportCountValidation(c *gin.Context, status int, field, code, message string) {
	middleware.AbortReportCountError(c, status, code, "validation", message, []middleware.ReportCountFieldError{{Field: field, Code: code, Message: message}}, false, "")
}

func parseReportCountRequest(c *gin.Context) (string, domain.ReportCountGrouping, service.ReportFilter, int64, bool) {
	filter := service.ReportFilter{}
	grouping := domain.ReportCountDay
	q, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		reportCountValidation(c, 400, "query", "invalid_query", "Query parameters have invalid encoding")
		return "", grouping, filter, 0, false
	}
	allowed := map[string]bool{"metric": true, "type": true, "id": true, "since": true, "until": true, "from": true, "to": true, "group_by": true, "timezone_offset": true, "timezone": true, "business_hours": true, "format": true}
	for key, values := range q {
		if !allowed[key] {
			reportCountValidation(c, 400, "query", "unknown_parameter", "Unknown query parameter")
			return "", grouping, filter, 0, false
		}
		if len(values) != 1 {
			reportCountValidation(c, 400, key, "duplicate_parameter", "Query parameters must have a single value")
			return "", grouping, filter, 0, false
		}
	}
	metric := q.Get("metric")
	switch metric {
	case "conversations_count", "incoming_messages_count", "outgoing_messages_count":
	case "avg_first_response_time", "avg_resolution_time", "reply_time", "resolutions_count", "bot_resolutions_count", "bot_handoffs_count":
		reportCountValidation(c, 422, "metric", "unsupported_metric", "This metric is not supported by the count series")
		return "", grouping, filter, 0, false
	default:
		reportCountValidation(c, 400, "metric", "invalid_metric", "A recognized metric is required")
		return "", grouping, filter, 0, false
	}
	typ := q.Get("type")
	if typ == "" {
		typ = "account"
	}
	switch typ {
	case "account":
	case "agent", "inbox", "team", "label":
		reportCountValidation(c, 422, "type", "unsupported_dimension", "Only account count series are supported")
		return "", grouping, filter, 0, false
	default:
		reportCountValidation(c, 400, "type", "invalid_type", "Unknown report dimension")
		return "", grouping, filter, 0, false
	}
	if values, exists := q["id"]; exists {
		if _, err := strconv.ParseUint(values[0], 10, 64); err != nil {
			reportCountValidation(c, 400, "id", "invalid_id", "Report ID must be an unsigned integer")
		} else {
			reportCountValidation(c, 422, "id", "unsupported_id", "An ID is not applicable to an account count series")
		}
		return "", grouping, filter, 0, false
	}
	group := q.Get("group_by")
	switch group {
	case "hour":
		grouping = domain.ReportCountHour
	case "month":
		grouping = domain.ReportCountMonth
	case "year":
		grouping = domain.ReportCountYear
	case "week":
		reportCountValidation(c, 422, "group_by", "unsupported_grouping", "Weekly count series are not supported")
		return "", grouping, filter, 0, false
	}
	// As specified in product doc20, unknown group_by values fall back to day.
	if _, exists := q["timezone"]; exists {
		reportCountValidation(c, 422, "timezone", "unsupported_timezone", "IANA timezones are not supported by this fixed-offset count series")
		return "", grouping, filter, 0, false
	}
	offsetSeconds, offsetErr := parseReportCountOffset(q.Get("timezone_offset"))
	if offsetErr != nil {
		if errors.Is(offsetErr, errReportCountOffsetInvalid) {
			reportCountValidation(c, 400, "timezone_offset", "invalid_timezone_offset", "Timezone offset must be a finite decimal number")
		} else {
			reportCountValidation(c, 422, "timezone_offset", "unsupported_timezone_offset", "Timezone offset must be exactly representable as whole seconds within the supported arithmetic range")
		}
		return "", grouping, filter, 0, false
	}
	if value := q.Get("business_hours"); value != "" {
		b, err := strconv.ParseBool(strings.ToLower(strings.TrimSpace(value)))
		if err != nil {
			reportCountValidation(c, 400, "business_hours", "invalid_business_hours", "Business hours must be a boolean")
			return "", grouping, filter, 0, false
		}
		filter.BusinessHours = b
	}
	if format := q.Get("format"); format != "" && format != "json" {
		if format == "csv" {
			reportCountValidation(c, 422, "format", "unsupported_format", "Count series support JSON only")
		} else {
			reportCountValidation(c, 400, "format", "invalid_format", "Unknown report format")
		}
		return "", grouping, filter, 0, false
	}
	since, until := q.Get("since"), q.Get("until")
	if since == "" {
		since = q.Get("from")
	}
	if until == "" {
		until = q.Get("to")
	}
	filter.Since, filter.Until = parseTimeParam(since, false), parseTimeParam(until, true)
	for _, bound := range []struct {
		field string
		valid bool
	}{{"since", filter.Since != nil}, {"until", filter.Until != nil}} {
		if !bound.valid {
			reportCountValidation(c, 400, bound.field, "invalid_time_bound", "An explicit valid time bound is required")
			return "", grouping, filter, 0, false
		}
	}
	return metric, grouping, filter, offsetSeconds, true
}

func (h *ReportHandler) GetCountSeries(c *gin.Context) {
	metric, grouping, filter, offsetSeconds, ok := parseReportCountRequest(c)
	if !ok {
		return
	}
	points, err := h.reportService.GetCountSeriesWithOffset(c.Request.Context(), c.GetUint(middleware.ContextAccountID), metric, grouping, filter, offsetSeconds)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrReportCountOffset):
			reportCountValidation(c, 422, "timezone_offset", "unsupported_timezone_offset", "Timezone offset exceeds the supported arithmetic range")
		case errors.Is(err, service.ErrReportCountQueryRepresentation), errors.Is(err, service.ErrReportCountBounds):
			reportCountValidation(c, 422, "since/until", "report_query_bound_unsupported", "Report query bounds require supported timestamp representations")
		case errors.Is(err, service.ErrReportCountStorageRepresentation):
			middleware.AbortReportCountError(c, 503, "report_storage_representation_unsupported", "external", "Report counts require supported timestamp storage", nil, false, "report_storage")
		case errors.Is(err, service.ErrReportCountBudget):
			reportCountValidation(c, 422, "since/until", "report_range_too_large", "Report range may intersect at most 400 selected calendar buckets")
		case errors.Is(err, context.DeadlineExceeded):
			middleware.AbortReportCountError(c, 504, "report_timeout", "external", "Report count request timed out", nil, true, "report_storage")
		case errors.Is(err, context.Canceled):
			middleware.AbortReportCountError(c, 503, "request_cancelled", "external", "Report count request was cancelled", nil, false, "report_storage")
		default:
			middleware.AbortReportCountError(c, 503, "report_unavailable", "external", "Report counts are temporarily unavailable", nil, true, "report_storage")
		}
		return
	}
	c.JSON(http.StatusOK, points)
}
