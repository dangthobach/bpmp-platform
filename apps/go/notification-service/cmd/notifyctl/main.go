package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/renderer"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/schema"
)

type TemplateBundle struct {
	TemplateKey    string            `json:"template_key"`
	SchemaMajor    uint32            `json:"schema_major"`
	DefaultLocale  string            `json:"default_locale"`
	Syntax         string            `json:"syntax"`
	VariableSchema json.RawMessage   `json:"variable_schema"`
	Variants       []BundleVariant   `json:"variants"`
}

type BundleVariant struct {
	Channel     string `json:"channel"`
	Locale      string `json:"locale"`
	VariantKind string `json:"variant_kind"`
	SubjectTmpl string `json:"subject_tmpl"`
	HTMLTmpl    string `json:"html_tmpl"`
	TextTmpl    string `json:"text_tmpl"`
}

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
}

func runCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: notifyctl <lint|preview> [flags] <bundle-file-or-dir>")
		return 2
	}
	switch args[0] {
	case "lint":
		return runLint(args[1:], stdout, stderr)
	case "preview":
		return runPreview(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown notifyctl command %q (expected lint or preview)\n", args[0])
		return 2
	}
}

func runLint(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("lint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	baselinePath := fs.String("baseline", "", "optional previous published bundle or schema JSON for breaking-change check")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: notifyctl lint [--baseline <prev.json>] <bundle-file-or-dir>")
		return 2
	}
	bundles, err := loadBundles(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "lint load error: %v\n", err)
		return 1
	}
	var baselineSchema []byte
	if *baselinePath != "" {
		raw, err := os.ReadFile(*baselinePath)
		if err != nil {
			fmt.Fprintf(stderr, "read baseline: %v\n", err)
			return 1
		}
		var maybeBundle TemplateBundle
		if json.Unmarshal(raw, &maybeBundle) == nil && len(maybeBundle.VariableSchema) > 0 {
			baselineSchema = maybeBundle.VariableSchema
		} else {
			baselineSchema = raw
		}
	}

	eng := renderer.NewEngine(renderer.DefaultRenderLimits())
	for _, b := range bundles {
		cs, err := schema.Compile(b.VariableSchema)
		if err != nil {
			fmt.Fprintf(stderr, "[FAIL] %s: schema invalid: %v\n", b.TemplateKey, err)
			return 1
		}
		if len(baselineSchema) > 0 {
			if err := schema.CheckBackwardCompatible(baselineSchema, b.VariableSchema); err != nil {
				fmt.Fprintf(stderr, "[FAIL] %s: breaking schema change: %v\n", b.TemplateKey, err)
				return 1
			}
		}
		exampleJSON, _ := json.Marshal(cs.BuildExampleData())
		exampleData, err := cs.ValidatePayload(exampleJSON)
		if err != nil {
			fmt.Fprintf(stderr, "[FAIL] %s: example data failed schema validation: %v\n", b.TemplateKey, err)
			return 1
		}
		for _, v := range b.Variants {
			if strings.EqualFold(v.VariantKind, "PROVIDER_TEMPLATE") {
				continue
			}
			compiled, err := renderer.CompileEphemeral(renderer.VariantSource{
				Syntax:             b.Syntax,
				Channel:            v.Channel,
				Locale:             v.Locale,
				SubjectTmpl:        v.SubjectTmpl,
				HTMLTmpl:           v.HTMLTmpl,
				TextTmpl:           v.TextTmpl,
				DeclaredDataFields: cs.FieldSpecs(),
			})
			if err != nil {
				fmt.Fprintf(stderr, "[FAIL] %s (%s/%s): %v\n", b.TemplateKey, v.Channel, v.Locale, err)
				return 1
			}
			out, err := eng.Render(compiled, renderer.RenderContext{
				Data:      exampleData,
				Recipient: map[string]any{"displayName": "Sample Recipient", "locale": v.Locale, "timezone": "Asia/Ho_Chi_Minh"},
				Tenant:    map[string]any{"name": "BPMP"},
				Brand:     map[string]any{"supportHotline": "1900-0000", "logoUrl": "https://cdn.bpmp.example.com/logo.png"},
				System:    map[string]any{"unsubscribeUrl": "https://notifications.bpmp.local/unsubscribe", "correlationId": "lint", "sentYear": 2026},
			})
			if err != nil {
				fmt.Fprintf(stderr, "[FAIL] %s (%s/%s): render gate failed: %v\n", b.TemplateKey, v.Channel, v.Locale, err)
				return 1
			}
			for _, w := range out.Warnings {
				fmt.Fprintf(stdout, "[WARN] %s (%s/%s): %s\n", b.TemplateKey, v.Channel, v.Locale, w)
			}
		}
		fmt.Fprintf(stdout, "[OK] %s@%d (%d variants)\n", b.TemplateKey, b.SchemaMajor, len(b.Variants))
	}
	return 0
}

func runPreview(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("preview", flag.ContinueOnError)
	fs.SetOutput(stderr)
	channel := fs.String("channel", "EMAIL", "target channel (EMAIL, SMS, IN_APP, PUSH)")
	locale := fs.String("locale", "", "target locale (defaults to bundle default_locale)")
	dataFile := fs.String("data", "", "optional JSON sample data file")
	outPath := fs.String("out", "", "optional output file path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: notifyctl preview [--channel EMAIL] [--locale vi-VN] [--data data.json] [--out out.html] <bundle>")
		return 2
	}
	bundles, err := loadBundles(fs.Arg(0))
	if err != nil || len(bundles) == 0 {
		fmt.Fprintf(stderr, "preview load error: %v\n", err)
		return 1
	}
	b := bundles[0]
	cs, err := schema.Compile(b.VariableSchema)
	if err != nil {
		fmt.Fprintf(stderr, "schema error: %v\n", err)
		return 1
	}
	var data map[string]any
	if *dataFile != "" {
		raw, err := os.ReadFile(*dataFile)
		if err != nil {
			fmt.Fprintf(stderr, "read data file: %v\n", err)
			return 1
		}
		data, err = cs.ValidatePayload(raw)
		if err != nil {
			fmt.Fprintf(stderr, "data validation failed: %v\n", err)
			return 1
		}
	} else {
		data = cs.BuildExampleData()
	}

	wantChan := strings.ToUpper(strings.TrimSpace(*channel))
	wantLoc := strings.TrimSpace(*locale)
	if wantLoc == "" {
		wantLoc = b.DefaultLocale
	}
	var chosen *BundleVariant
	for i := range b.Variants {
		if strings.EqualFold(b.Variants[i].Channel, wantChan) && strings.EqualFold(b.Variants[i].Locale, wantLoc) {
			chosen = &b.Variants[i]
			break
		}
	}
	if chosen == nil {
		fmt.Fprintf(stderr, "no variant found for %s/%s\n", wantChan, wantLoc)
		return 1
	}

	eng := renderer.NewEngine(renderer.DefaultRenderLimits())
	compiled, err := renderer.CompileEphemeral(renderer.VariantSource{
		Syntax:             b.Syntax,
		Channel:            chosen.Channel,
		Locale:             chosen.Locale,
		SubjectTmpl:        chosen.SubjectTmpl,
		HTMLTmpl:           chosen.HTMLTmpl,
		TextTmpl:           chosen.TextTmpl,
		DeclaredDataFields: cs.FieldSpecs(),
	})
	if err != nil {
		fmt.Fprintf(stderr, "compile error: %v\n", err)
		return 1
	}
	out, err := eng.Render(compiled, renderer.RenderContext{
		Data:      data,
		Recipient: map[string]any{"displayName": "Preview User", "locale": chosen.Locale, "timezone": "Asia/Ho_Chi_Minh"},
		Tenant:    map[string]any{"name": "BPMP"},
		Brand:     map[string]any{"supportHotline": "1900-0000", "logoUrl": "https://cdn.bpmp.example.com/logo.png"},
		System:    map[string]any{"unsubscribeUrl": "https://notifications.bpmp.local/unsubscribe", "correlationId": "preview", "sentYear": 2026},
	})
	if err != nil {
		fmt.Fprintf(stderr, "render error: %v\n", err)
		return 1
	}

	renderedContent := out.HTML
	if renderedContent == "" {
		renderedContent = out.Text
	}
	if *outPath != "" {
		if err := os.WriteFile(*outPath, []byte(renderedContent), 0o644); err != nil {
			fmt.Fprintf(stderr, "write output: %v\n", err)
			return 1
		}
	} else {
		if out.Subject != "" {
			fmt.Fprintf(stdout, "Subject: %s\n", out.Subject)
		}
		if out.SMSEncoding != "" {
			fmt.Fprintf(stdout, "SMS: encoding=%s chars=%d segments=%d\n", out.SMSEncoding, out.SMSCharCount, out.SMSSegments)
		}
		fmt.Fprintln(stdout, renderedContent)
	}
	return 0
}

func loadBundles(target string) ([]TemplateBundle, error) {
	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		b, err := readBundleFile(target)
		if err != nil {
			return nil, err
		}
		return []TemplateBundle{b}, nil
	}
	var files []string
	err = filepath.WalkDir(target, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".json") {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("no .json bundle files found in directory")
	}
	var out []TemplateBundle
	for _, f := range files {
		b, err := readBundleFile(f)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		out = append(out, b)
	}
	return out, nil
}

func readBundleFile(path string) (TemplateBundle, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return TemplateBundle{}, err
	}
	var b TemplateBundle
	if err := json.Unmarshal(raw, &b); err != nil {
		return TemplateBundle{}, err
	}
	if b.TemplateKey == "" || len(b.VariableSchema) == 0 || len(b.Variants) == 0 {
		return TemplateBundle{}, errors.New("bundle requires template_key, variable_schema, and variants")
	}
	if b.SchemaMajor == 0 {
		b.SchemaMajor = 1
	}
	if b.DefaultLocale == "" {
		b.DefaultLocale = "vi-VN"
	}
	return b, nil
}
