package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestReportTimeParamBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		end   bool
		want  string
	}{
		{"date_start_is_UTC_midnight", "2026-01-31", false, "2026-01-31T00:00:00Z"},
		{"date_end_month_rollover", "2026-01-31", true, "2026-02-01T00:00:00Z"},
		{"date_end_year_rollover", "2026-12-31", true, "2027-01-01T00:00:00Z"},
		{"date_end_leap_day", "2024-02-28", true, "2024-02-29T00:00:00Z"},
		{"date_end_after_leap_day", "2024-02-29", true, "2024-03-01T00:00:00Z"},
		{"date_end_nonleap_year", "2026-02-28", true, "2026-03-01T00:00:00Z"},
		{"trim_date", " \t2026-01-31\n", true, "2026-02-01T00:00:00Z"},
		{"unix_epoch_start", "0", false, "1970-01-01T00:00:00Z"},
		{"unix_epoch_end_stays_exact", "0", true, "1970-01-01T00:00:00Z"},
		{"negative_unix_end", "-1", true, "1969-12-31T23:59:59Z"},
		{"fractional_start", "2026-01-31T12:34:56.123456789Z", false, "2026-01-31T12:34:56.123456789Z"},
		{"fractional_end_stays_exact", "2026-01-31T23:59:59.999999999Z", true, "2026-01-31T23:59:59.999999999Z"},
		{"explicit_midnight_stays_exact", "2026-01-31T00:00:00Z", true, "2026-01-31T00:00:00Z"},
		{"positive_quarter_hour_offset", "2026-02-01T05:45:00.123456789+05:45", true, "2026-02-01T00:00:00.123456789Z"},
		{"negative_half_hour_offset", "2026-01-31T20:30:00-03:30", false, "2026-02-01T00:00:00Z"},
		// Explicit RFC3339 offsets identify instants; parsing does not infer IANA DST rules.
		{"spring_before_offset_change", "2026-03-08T01:59:59.5-05:00", false, "2026-03-08T06:59:59.5Z"},
		{"spring_after_offset_change", "2026-03-08T03:00:00-04:00", true, "2026-03-08T07:00:00Z"},
		{"spring_explicit_offset_without_zone_rules", "2026-03-08T02:30:00-05:00", true, "2026-03-08T07:30:00Z"},
		{"fall_first_occurrence", "2026-11-01T01:30:00-04:00", false, "2026-11-01T05:30:00Z"},
		{"fall_second_occurrence", "2026-11-01T01:30:00-05:00", true, "2026-11-01T06:30:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseTimeParam(tc.input, tc.end)
			require.NotNil(t, got)
			require.Equal(t, tc.want, got.Format(time.RFC3339Nano))
			require.Same(t, time.UTC, got.Location())
		})
	}
	for _, input := range []string{"", " \t\n", "not-a-date", "2026-02-29", "2026-13-01", "2026-01-31T12:00:00", "2026-01-31T12:00:00Zjunk", "1769817600.5", "9223372036854775808"} {
		t.Run("invalid/"+input, func(t *testing.T) {
			require.Nil(t, parseTimeParam(input, false))
			require.Nil(t, parseTimeParam(input, true))
		})
	}
}

func TestReportFilterAliasesAndPrecedence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name  string
		query url.Values
		since string
		until string
		bh    bool
	}{
		{"date_aliases", url.Values{"from": {"2026-01-31"}, "to": {"2026-01-31"}}, "2026-01-31T00:00:00Z", "2026-02-01T00:00:00Z", false},
		{"primary_wins", url.Values{"since": {"2026-01-31"}, "until": {"2026-01-31T12:00:00.5Z"}, "from": {"2025-01-01"}, "to": {"2027-12-31"}, "business_hours": {" TRUE "}}, "2026-01-31T00:00:00Z", "2026-01-31T12:00:00.5Z", true},
		{"empty_primary_uses_alias", url.Values{"since": {""}, "until": {""}, "from": {"0"}, "to": {"1"}, "business_hours": {"1"}}, "1970-01-01T00:00:00Z", "1970-01-01T00:00:01Z", true},
		{"invalid_primary_does_not_use_alias", url.Values{"since": {"invalid"}, "until": {"2026-02-29"}, "from": {"2026-01-01"}, "to": {"2026-01-31"}}, "", "", false},
		{"whitespace_primary_does_not_use_alias", url.Values{"since": {" "}, "until": {"\t"}, "from": {"0"}, "to": {"1"}}, "", "", false},
		{"invalid_alias_remains_unset", url.Values{"from": {"bad"}, "to": {"bad"}, "business_hours": {"yes"}}, "", "", false},
		{"one_sided_start", url.Values{"since": {"2026-01-31"}}, "2026-01-31T00:00:00Z", "", false},
		{"one_sided_end", url.Values{"until": {"2026-01-31"}}, "", "2026-02-01T00:00:00Z", false},
		{"reversed_exact_instants_are_preserved", url.Values{"since": {"2"}, "until": {"1"}}, "1970-01-01T00:00:02Z", "1970-01-01T00:00:01Z", false},
		{"equal_instants_are_preserved", url.Values{"since": {"0"}, "until": {"0"}}, "1970-01-01T00:00:00Z", "1970-01-01T00:00:00Z", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/reports/summary?"+tc.query.Encode(), nil)
			got := parseReportFilter(c)
			for _, bound := range []struct {
				name string
				got  *time.Time
				want string
			}{{"since", got.Since, tc.since}, {"until", got.Until, tc.until}} {
				if bound.want == "" {
					require.Nil(t, bound.got, bound.name)
				} else {
					require.NotNil(t, bound.got, bound.name)
					require.Equal(t, bound.want, bound.got.Format(time.RFC3339Nano), bound.name)
					require.Same(t, time.UTC, bound.got.Location())
				}
			}
			require.Equal(t, tc.bh, got.BusinessHours)
		})
	}
}
