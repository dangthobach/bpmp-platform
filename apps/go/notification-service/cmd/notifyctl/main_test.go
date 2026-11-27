package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNotifyCTLLintAndPreview(t *testing.T) {
	dir := t.TempDir()
	bundlePath := filepath.Join(dir, "case.submitted.json")
	bundleJSON := `{
		"template_key": "case.submitted",
		"schema_major": 1,
		"default_locale": "vi-VN",
		"syntax": "gotmpl/v1",
		"variable_schema": {
			"type": "object",
			"required": ["caseCode"],
			"properties": {
				"caseCode": {"type": "string", "example": "CS-100"}
			}
		},
		"variants": [
			{
				"channel": "EMAIL",
				"locale": "vi-VN",
				"subject_tmpl": "Hồ sơ {{ .data.caseCode }}",
				"html_tmpl": "<p>Mã hồ sơ: <b>{{ .data.caseCode }}</b></p>"
			}
		]
	}`
	if err := os.WriteFile(bundlePath, []byte(bundleJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	var outBuf, errBuf bytes.Buffer
	if code := runCLI([]string{"lint", dir}, &outBuf, &errBuf); code != 0 {
		t.Fatalf("notifyctl lint failed (code=%d): %s", code, errBuf.String())
	}
	if !strings.Contains(outBuf.String(), "[OK] case.submitted@1") {
		t.Fatalf("unexpected lint output: %s", outBuf.String())
	}

	previewHTMLPath := filepath.Join(dir, "preview.html")
	outBuf.Reset()
	errBuf.Reset()
	if code := runCLI([]string{"preview", "--channel", "EMAIL", "--locale", "vi-VN", "--out", previewHTMLPath, bundlePath}, &outBuf, &errBuf); code != 0 {
		t.Fatalf("notifyctl preview failed (code=%d): %s", code, errBuf.String())
	}
	written, err := os.ReadFile(previewHTMLPath)
	if err != nil || !strings.Contains(string(written), "<b>CS-100</b>") {
		t.Fatalf("unexpected preview output file: %q err=%v", string(written), err)
	}
}
