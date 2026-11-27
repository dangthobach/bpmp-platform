package renderer

import (
	"errors"
	"strings"
	"testing"
)

func TestASTAllowlistRejectsDisallowedConstructsAndUndeclaredVars(t *testing.T) {
	declared := map[string]FieldSpec{
		"code": {Type: "string"},
	}
	badTemplates := map[string]string{
		"define":             `{{ define "sub" }}hi{{ end }}`,
		"template":           `{{ template "sub" . }}`,
		"block":              `{{ block "b" . }}hi{{ end }}`,
		"call":               `{{ call .data.code }}`,
		"unknown_namespace":  `{{ .env.SECRET }}`,
		"undeclared_data":    `{{ .data.undeclaredField }}`,
		"disallowed_builtin": `{{ printf "%s" .data.code }}`,
	}
	for name, tmpl := range badTemplates {
		t.Run(name, func(t *testing.T) {
			_, err := CompileEphemeral(VariantSource{
				Syntax:             SyntaxGoTmplV1,
				Channel:            "EMAIL",
				Locale:             "vi-VN",
				HTMLTmpl:           tmpl,
				DeclaredDataFields: declared,
			})
			if err == nil {
				t.Fatalf("expected %s template to be rejected", name)
			}
		})
	}
}

func TestSSTIPreventionSafeHTMLSanitizerAndFuncMap(t *testing.T) {
	eng := NewEngine(DefaultRenderLimits())
	declared := map[string]FieldSpec{
		"userInput": {Type: "string"},
		"notice":    {Type: "html_fragment"},
		"amount":    {Type: "money"},
	}
	compiled, err := eng.CompilePublished(VariantSource{
		TemplateVersionID: "ver-1",
		VariantID:         "var-1",
		Syntax:            SyntaxGoTmplV1,
		Channel:           "EMAIL",
		Locale:            "vi-VN",
		SubjectTmpl:       `Hồ sơ {{ upper .data.userInput }}`,
		HTMLTmpl:          `<p>{{ .data.userInput }}</p><div>{{ safeHTML .data.notice }}</div><span>{{ formatMoney .data.amount }}</span>`,
		DeclaredDataFields: declared,
	})
	if err != nil {
		t.Fatalf("compile published: %v", err)
	}

	// Immutable cache hit returns exact same pointer.
	cached, err := eng.CompilePublished(VariantSource{TemplateVersionID: "ver-1", VariantID: "var-1"})
	if err != nil || cached != compiled {
		t.Fatal("expected immutable cache hit")
	}

	out, err := eng.Render(compiled, RenderContext{
		Data: map[string]any{
			"userInput": `{{ .recipient.secret }} <script>alert(1)</script>`,
			"notice":    `<b>Xin chào</b><script>evil()</script><a href="javascript:alert(1)" onclick="x()">bad</a><a href="https://portal.bpmp.local/ok" onclick="steal()">good</a>`,
			"amount":    map[string]any{"amount": "12500000", "currency": "VND"},
		},
		Recipient: map[string]any{"displayName": "Bách", "timezone": "Asia/Ho_Chi_Minh"},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// SSTI payload must remain literal escaped text, never re-evaluated or unescaped!
	if !strings.Contains(out.HTML, `{{ .recipient.secret }} &lt;script&gt;alert(1)&lt;/script&gt;`) {
		t.Fatalf("SSTI / XSS was not escaped: %s", out.HTML)
	}
	// safeHTML must strip <script>, onclick, and javascript: href while keeping https:// href!
	if strings.Contains(out.HTML, "<script>") || strings.Contains(out.HTML, "onclick") || strings.Contains(out.HTML, "javascript:") {
		t.Fatalf("safeHTML sanitizer leaked unsafe markup: %s", out.HTML)
	}
	if !strings.Contains(out.HTML, `<b>Xin chào</b>`) || !strings.Contains(out.HTML, `<a href="https://portal.bpmp.local/ok" rel="noopener noreferrer">good</a>`) {
		t.Fatalf("safeHTML stripped safe tags unexpectedly: %s", out.HTML)
	}
	if !strings.Contains(out.HTML, "12,500,000 VND") {
		t.Fatalf("unexpected money formatting: %s", out.HTML)
	}

	// Insecure http:// link in email HTML must be rejected by channel constraints!
	insecureVar, err := CompileEphemeral(VariantSource{
		Syntax:             SyntaxGoTmplV1,
		Channel:            "EMAIL",
		Locale:             "en-US",
		HTMLTmpl:           `<a href="http://insecure.example.com">click</a>`,
		DeclaredDataFields: map[string]FieldSpec{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Render(insecureVar, RenderContext{}); !errors.Is(err, ErrRenderLimit) {
		t.Fatalf("expected insecure link rejection, got %v", err)
	}
}

func TestSMSSegmentCalculatorAndVietnameseStripAccents(t *testing.T) {
	vn := "Mã OTP của Đặng Thọ Bách là 123456. Vui lòng không chia sẻ mã này cho bất kỳ ai khác!"
	enc, chars, segs := CalculateSMSSegments(vn)
	if enc != "UCS2" || segs != 2 || chars <= 70 {
		t.Fatalf("expected 2-segment UCS2 for %d Vietnamese chars, got enc=%s chars=%d segs=%d", len([]rune(vn)), enc, chars, segs)
	}

	stripped := StripAccents(vn)
	if strings.ContainsAny(stripped, "ăâđêôơưĐáàảãạ") {
		t.Fatalf("StripAccents left Vietnamese diacritics: %q", stripped)
	}
	enc2, _, segs2 := CalculateSMSSegments(stripped)
	if enc2 != "GSM7" || segs2 != 1 {
		t.Fatalf("expected 1-segment GSM7 after StripAccents, got enc=%s segs=%d (%q)", enc2, segs2, stripped)
	}
}
