package renderer

import (
	"strings"
	"testing"
)

func TestHandlebarsTranspileAndRender(t *testing.T) {
	eng := NewEngine(DefaultRenderLimits())
	declared := map[string]FieldSpec{
		"customerName": {Type: "string"},
		"isVip":        {Type: "boolean"},
		"amount":       {Type: "money"},
		"dueDate":      {Type: "date"},
		"notice":       {Type: "html_fragment"},
		"items": {
			Type:     "array",
			MaxItems: 10,
			Items: &FieldSpec{
				Type: "object",
				Properties: map[string]FieldSpec{
					"title": {Type: "string"},
				},
			},
		},
		"branch": {
			Type: "object",
			Properties: map[string]FieldSpec{
				"name": {Type: "string"},
			},
		},
	}

	compiled, err := CompileEphemeral(VariantSource{
		Syntax:      SyntaxHandlebarsV1,
		Channel:     "EMAIL",
		Locale:      "vi-VN",
		SubjectTmpl: `Xin chào {{customerName}} - {{tenant.name}}`,
		HTMLTmpl: `{{!-- comment ignored --}}
<div>
  <p>Hello {{customerName}} ({{recipient.displayName}})</p>
  {{#if isVip}}<strong>VIP</strong>{{else}}<span>Standard</span>{{/if}}
  {{#unless isVip}}<em>Upgrade</em>{{/unless}}
  {{#with branch}}<p>Branch: {{name}} / {{brand.supportHotline}}</p>{{/with}}
  <ul>{{#each items}}<li>{{title}}</li>{{/each}}</ul>
  <div>{{{notice}}}</div>
  <span>{{formatCurrency amount}} - {{formatDate dueDate}}</span>
</div>`,
		DeclaredDataFields: declared,
	})
	if err != nil {
		t.Fatalf("compile handlebars: %v", err)
	}

	out, err := eng.Render(compiled, RenderContext{
		Data: map[string]any{
			"customerName": `<b>Bách</b> {{.system.secret}}`,
			"isVip":        true,
			"amount":       map[string]any{"amount": "5000000", "currency": "VND"},
			"dueDate":      "2026-12-01",
			"notice":       `<b>Approved</b><script>alert(1)</script>`,
			"items":        []any{map[string]any{"title": "Item A"}, map[string]any{"title": "Item B"}},
			"branch":       map[string]any{"name": "HN-01"},
		},
		Recipient: map[string]any{"displayName": "Nguyen Van A", "timezone": "Asia/Ho_Chi_Minh"},
		Tenant:    map[string]any{"name": "BPMP Bank"},
		Brand:     map[string]any{"supportHotline": "1900-1234"},
	})
	if err != nil {
		t.Fatalf("render handlebars: %v", err)
	}
	if !strings.Contains(out.Subject, "BPMP Bank") {
		t.Fatalf("expected tenant.name in subject, got %q", out.Subject)
	}
	if !strings.Contains(out.HTML, `&lt;b&gt;Bách&lt;/b&gt; {{.system.secret}}`) {
		t.Fatalf("expected HTML escaping and SSTI immunity, got %s", out.HTML)
	}
	if strings.Contains(out.HTML, "<script>") {
		t.Fatalf("expected sanitizer to strip script tag from triple mustache html_fragment: %s", out.HTML)
	}
	if !strings.Contains(out.HTML, "<strong>VIP</strong>") || strings.Contains(out.HTML, "<em>Upgrade</em>") {
		t.Fatalf("unexpected if/unless output: %s", out.HTML)
	}
	if !strings.Contains(out.HTML, "Branch: HN-01 / 1900-1234") {
		t.Fatalf("unexpected with/brand output: %s", out.HTML)
	}
	if !strings.Contains(out.HTML, "<li>Item A</li><li>Item B</li>") {
		t.Fatalf("unexpected each output: %s", out.HTML)
	}
	if !strings.Contains(out.HTML, "5,000,000 VND - 2026-12-01") {
		t.Fatalf("unexpected helper output: %s", out.HTML)
	}
}

func TestHandlebarsDiagnosticsAndSSTI(t *testing.T) {
	declared := map[string]FieldSpec{
		"customerName": {Type: "string"},
		"notice":       {Type: "html_fragment"},
		"active":       {Type: "boolean"},
	}

	tests := []struct {
		name     string
		html     string
		wantCode string
	}{
		{
			name:     "unclosed mustache",
			html:     `<p>Hello {{customerName</p>`,
			wantCode: "UNCLOSED_MUSTACHE",
		},
		{
			name:     "unclosed block",
			html:     `{{#if active}}<p>Hi</p>`,
			wantCode: "UNCLOSED_BLOCK",
		},
		{
			name:     "mismatched closing block",
			html:     `{{#if active}}<p>Hi</p>{{/each}}`,
			wantCode: "MISMATCHED_CLOSING_BLOCK",
		},
		{
			name:     "invalid helper",
			html:     `{{eval customerName}}`,
			wantCode: "INVALID_HELPER",
		},
		{
			name:     "undeclared variable",
			html:     `<p>{{unknownVar}}</p>`,
			wantCode: "UNDECLARED_VARIABLE",
		},
		{
			name:     "triple mustache on non-html_fragment forbidden",
			html:     `<div>{{{customerName}}}</div>`,
			wantCode: "FORBIDDEN_CONSTRUCT",
		},
		{
			name:     "ssti pipe injection forbidden",
			html:     `{{customerName | printf "%s"}}`,
			wantCode: "FORBIDDEN_CONSTRUCT",
		},
		{
			name:     "ssti partial injection forbidden",
			html:     `{{> maliciousPartial}}`,
			wantCode: "FORBIDDEN_CONSTRUCT",
		},
		{
			name:     "ssti script tag forbidden",
			html:     `<div><script>alert(1)</script></div>`,
			wantCode: "FORBIDDEN_CONSTRUCT",
		},
		{
			name:     "unclosed html tag warning",
			html:     `<div><p>{{customerName}}</div>`,
			wantCode: "UNCLOSED_HTML_TAG",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			diags := Diagnose(SyntaxHandlebarsV1, declared, "", tc.html, "")
			found := false
			for _, d := range diags {
				if d.Code == tc.wantCode {
					found = true
					if d.Target != "html" || d.Line < 1 || d.Column < 1 {
						t.Fatalf("invalid diagnostic metadata: %+v", d)
					}
					break
				}
			}
			if !found {
				t.Fatalf("expected diagnostic %s, got %+v", tc.wantCode, diags)
			}
		})
	}
}
