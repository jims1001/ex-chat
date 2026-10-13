package modruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/stretchr/testify/require"
)

func TestOPSCannedPreviewRendering(t *testing.T) {
	cases := []struct {
		name, source, want, failure string
		values                      map[string]string
	}{
		{"unicode", "你好 {{name}} ☕ café", "你好 世界 ☕ café", "", map[string]string{"name": "世界"}},
		{"repeated", "{{name}}/{{name}}", "x/x", "", map[string]string{"name": "x"}},
		{"single_braces", "a {name} } { b", "a {name} } { b", "", nil},
		{"empty_source", "", "", "", nil},
		{"empty_value", "a{{name}}b", "ab", "", map[string]string{"name": ""}},
		{"literal_replacement", "{{name}}", "{{missing}} <b>&\"</b> }}", "", map[string]string{"name": "{{missing}} <b>&\"</b> }}"}},
		{"extra_variable", "plain", "plain", "", map[string]string{"extra": "unused"}},
		{"identifier", "{{_x2}}", "ok", "", map[string]string{"_x2": "ok"}},
		{"missing", "你好 {{name}}", "", "missing preview variable at source byte 7", nil},
		{"case_sensitive", "{{Name}}", "", "missing preview variable at source byte 0", map[string]string{"name": "x"}},
		{"empty_name", "{{}}", "", "malformed preview placeholder at source byte 0", nil},
		{"space_name", "{{ name}}", "", "malformed preview placeholder at source byte 0", nil},
		{"trailing_space", "{{name }}", "", "malformed preview placeholder at source byte 0", nil},
		{"numeric_first", "{{1name}}", "", "malformed preview placeholder at source byte 0", nil},
		{"dotted_name", "{{contact.name}}", "", "malformed preview placeholder at source byte 0", nil},
		{"expression", "{{name|upper}}", "", "malformed preview placeholder at source byte 0", nil},
		{"unicode_name", "{{姓名}}", "", "malformed preview placeholder at source byte 0", nil},
		{"open_only", "{{", "", "malformed preview placeholder at source byte 0", nil},
		{"unclosed", "{{name", "", "malformed preview placeholder at source byte 0", nil},
		{"single_close", "{{name}", "", "malformed preview placeholder at source byte 0", nil},
		{"orphan_close", "ab}}", "", "malformed preview placeholder at source byte 2", nil},
		{"nested", "{{a{{b}}}}", "", "malformed preview placeholder at source byte 0", nil},
		{"triple_open", "{{{name}}}", "", "malformed preview placeholder at source byte 0", map[string]string{"name": "x"}},
		{"triple_close", "{{name}}}", "x}", "", map[string]string{"name": "x"}},
		{"first_error", "{{missing}}{{}}", "", "missing preview variable at source byte 0", nil},
		{"no_partial", "prefix{{ok}}{{missing}}", "", "missing preview variable at source byte 12", map[string]string{"ok": "secret"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := renderCannedPreview(context.Background(), tc.source, tc.values)
			if tc.failure != "" {
				var f *ContractFailure
				require.ErrorAs(t, err, &f)
				require.Equal(t, "validation_failed", f.Type)
				require.Equal(t, tc.failure, f.Message)
				require.Empty(t, got)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.want, got)
			}
		})
	}
}

type cannedCancelChecks struct {
	context.Context
	remaining int
	cancel    context.CancelFunc
}

func (c *cannedCancelChecks) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		c.cancel()
	}
	return c.Context.Err()
}
func cannedCancellingContext(checks int) *cannedCancelChecks {
	ctx, cancel := context.WithCancel(context.Background())
	return &cannedCancelChecks{Context: ctx, remaining: checks, cancel: cancel}
}

func TestOPSCannedPreviewRendererBounds(t *testing.T) {
	ctx := context.Background()
	limit := repository.CannedResponseReadBytes
	t.Run("decoded_exact_boundary", func(t *testing.T) {
		got, err := renderCannedPreview(ctx, "{{x}}", map[string]string{"x": strings.Repeat("a", limit)})
		require.NoError(t, err)
		require.Len(t, got, limit)
		got, err = renderCannedPreview(ctx, "{{x}}a", map[string]string{"x": strings.Repeat("a", limit)})
		require.Error(t, err)
		require.Empty(t, got)
	})
	t.Run("repeated_expansion", func(t *testing.T) {
		got, err := renderCannedPreview(ctx, strings.Repeat("{{x}}", 1000), map[string]string{"x": strings.Repeat("a", 2000)})
		require.Error(t, err)
		require.Empty(t, got)
	})
	t.Run("source_limit", func(t *testing.T) {
		got, err := renderCannedPreview(ctx, strings.Repeat("a", limit+1), nil)
		require.Error(t, err)
		require.Empty(t, got)
	})
	t.Run("source_utf8", func(t *testing.T) {
		got, err := renderCannedPreview(ctx, string([]byte{255}), nil)
		require.Error(t, err)
		require.Empty(t, got)
	})
	t.Run("encoded_metadata_boundary", func(t *testing.T) {
		view := cannedPreviewView{ID: 9007199254740993, AccountID: 91, ShortCode: "code", ObjectVersion: 9007199254740993}
		base, err := json.Marshal(view)
		require.NoError(t, err)
		view.ResolvedContent = strings.Repeat("a", limit-len(base))
		require.NoError(t, boundCannedPreview(ctx, view))
		view.ResolvedContent += "a"
		require.Error(t, boundCannedPreview(ctx, view))
	})
	t.Run("json_escape_growth", func(t *testing.T) {
		require.Error(t, boundCannedPreview(ctx, cannedPreviewView{ResolvedContent: strings.Repeat("\x01", limit/6+1)}))
	})
	t.Run("variable_budget", func(t *testing.T) {
		v, err := cannedPreviewVariables(ctx, map[string]any{"x": strings.Repeat("a", limit-1)})
		require.NoError(t, err)
		require.Len(t, v["x"], limit-1)
		_, err = cannedPreviewVariables(ctx, map[string]any{"x": strings.Repeat("a", limit)})
		require.Error(t, err)
		_, err = cannedPreviewVariables(ctx, map[string]any{strings.Repeat("a", limit+1): ""})
		require.Error(t, err)
	})
	t.Run("variable_direct_types", func(t *testing.T) {
		for _, v := range []any{nil, map[string]any(nil), map[string]string{"x": "y"}, map[string]any{"x": nil}, map[string]any{"x": 1}, map[string]any{"x": []any{}}, map[string]any{"": "x"}, map[string]any{"x.y": "x"}, map[string]any{"x": string([]byte{255})}} {
			_, err := cannedPreviewVariables(ctx, v)
			require.Error(t, err)
		}
	})
	t.Run("cancel_literal_scan", func(t *testing.T) {
		c := cannedCancellingContext(4)
		defer c.cancel()
		out, err := renderCannedPreview(c, strings.Repeat("a", limit), nil)
		require.ErrorIs(t, err, context.Canceled)
		require.Empty(t, out)
	})
	t.Run("cancel_placeholder_scan", func(t *testing.T) {
		c := cannedCancellingContext(5)
		defer c.cancel()
		out, err := renderCannedPreview(c, "{{"+strings.Repeat("a", limit-4)+"}}", nil)
		require.ErrorIs(t, err, context.Canceled)
		require.Empty(t, out)
	})
	t.Run("cancel_name_validation", func(t *testing.T) {
		c := cannedCancellingContext(4)
		defer c.cancel()
		_, err := cannedPreviewVariables(c, map[string]any{strings.Repeat("a", limit): ""})
		require.ErrorIs(t, err, context.Canceled)
	})
	t.Run("cancel_variable_map", func(t *testing.T) {
		c := cannedCancellingContext(5)
		defer c.cancel()
		_, err := cannedPreviewVariables(c, map[string]any{"a": "", "b": "", "c": "", "d": ""})
		require.ErrorIs(t, err, context.Canceled)
	})
	t.Run("cancel_encoding", func(t *testing.T) {
		c := cannedCancellingContext(2)
		defer c.cancel()
		require.ErrorIs(t, boundCannedPreview(c, cannedPreviewView{ResolvedContent: strings.Repeat("a", limit/2)}), context.Canceled)
	})
}
