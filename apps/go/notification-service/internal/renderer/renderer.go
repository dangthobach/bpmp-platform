package renderer

import (
	"bytes"
	"errors"
	"fmt"
	htmltmpl "html/template"
	"sort"
	"strconv"
	"strings"
	"sync"
	texttmpl "text/template"
	"text/template/parse"
	"time"
	_ "time/tzdata"
	"unicode/utf16"

	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/crypto"
)

const SyntaxGoTmplV1 = "gotmpl/v1"

var (
	ErrUnsupportedSyntax = errors.New("unsupported template syntax; expected gotmpl/v1 or handlebars/v1")
	ErrDisallowedAST     = errors.New("template contains disallowed AST construct")
	ErrRenderLimit       = errors.New("rendered output exceeds configured channel limit")
)

// FieldSpec describes a declared variable in variable_schema so the AST
// validator can verify .data.* paths, range bounds, and html_fragment safety.
type FieldSpec struct {
	Type       string
	MaxItems   int
	PII        bool
	Secret     bool
	Properties map[string]FieldSpec
	Items      *FieldSpec
}

type RenderLimits struct {
	MaxEmailHTMLBytes int
	WarnEmailHTMLBytes int
	MaxSMSSegments    int
	MaxPushBodyBytes  int
}

func DefaultRenderLimits() RenderLimits {
	return RenderLimits{
		MaxEmailHTMLBytes:  102 * 1024,
		WarnEmailHTMLBytes: 95 * 1024,
		MaxSMSSegments:     4,
		MaxPushBodyBytes:   4096,
	}
}

type VariantSource struct {
	TemplateVersionID  string
	VariantID          string
	Syntax             string
	Channel            string
	Locale             string
	SubjectTmpl        string
	HTMLTmpl           string
	TextTmpl           string
	LayoutHTMLTmpl     string
	DeclaredDataFields map[string]FieldSpec
}

type RenderContext struct {
	Data      map[string]any
	Recipient map[string]any
	Tenant    map[string]any
	Brand     map[string]any
	System    map[string]any
}

type RenderOutput struct {
	Channel       string
	Locale        string
	Subject       string
	HTML          string
	Text          string
	HTMLBytes     int
	SMSEncoding   string
	SMSCharCount  int
	SMSSegments   int
	StrippedASCII string
	Warnings      []string
}

type CompiledVariant struct {
	channel        string
	locale         string
	subject        *texttmpl.Template
	text           *texttmpl.Template
	html           *htmltmpl.Template
	layoutHTML     *htmltmpl.Template
	referencedVars map[string]struct{}
	declaredVars   map[string]FieldSpec
}

type Engine struct {
	mu     sync.RWMutex
	cache  map[string]*CompiledVariant
	limits RenderLimits
}

func NewEngine(limits RenderLimits) *Engine {
	if limits.MaxEmailHTMLBytes <= 0 {
		limits = DefaultRenderLimits()
	}
	return &Engine{
		cache:  make(map[string]*CompiledVariant),
		limits: limits,
	}
}

func CacheKey(templateVersionID, variantID string) string {
	return templateVersionID + ":" + variantID
}

// CompilePublished compiles and caches an immutable published variant. Once
// cached under templateVersionID:variantID it never requires invalidation.
func (e *Engine) CompilePublished(src VariantSource) (*CompiledVariant, error) {
	if src.TemplateVersionID == "" || src.VariantID == "" {
		return nil, errors.New("template_version_id and variant_id are required for immutable cache")
	}
	key := CacheKey(src.TemplateVersionID, src.VariantID)
	e.mu.RLock()
	cached := e.cache[key]
	e.mu.RUnlock()
	if cached != nil {
		return cached, nil
	}
	compiled, err := CompileEphemeral(src)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	if existing := e.cache[key]; existing != nil {
		e.mu.Unlock()
		return existing, nil
	}
	e.cache[key] = compiled
	e.mu.Unlock()
	return compiled, nil
}

// CompileEphemeral compiles and AST-validates a template variant in memory
// without inserting it into the global immutable cache (used for DRAFT preview
// and CLI linting).
func CompileEphemeral(src VariantSource) (*CompiledVariant, error) {
	syntax := src.Syntax
	if syntax == "" {
		syntax = SyntaxGoTmplV1
	}
	declared := src.DeclaredDataFields
	if declared == nil {
		declared = map[string]FieldSpec{}
	}
	subjectSrc := src.SubjectTmpl
	textSrc := src.TextTmpl
	htmlSrc := src.HTMLTmpl
	layoutSrc := src.LayoutHTMLTmpl

	switch syntax {
	case SyntaxGoTmplV1:
	case SyntaxHandlebarsV1:
		var err error
		if subjectSrc != "" {
			subjectSrc, err = TranspileHandlebars(subjectSrc, declared)
			if err != nil {
				return nil, fmt.Errorf("transpile subject handlebars: %w", err)
			}
		}
		if textSrc != "" {
			textSrc, err = TranspileHandlebars(textSrc, declared)
			if err != nil {
				return nil, fmt.Errorf("transpile text handlebars: %w", err)
			}
		}
		if htmlSrc != "" {
			htmlSrc, err = TranspileHandlebars(htmlSrc, declared)
			if err != nil {
				return nil, fmt.Errorf("transpile html handlebars: %w", err)
			}
		}
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedSyntax, syntax)
	}

	funcs := templateFuncMap("UTC")
	referenced := make(map[string]struct{})

	out := &CompiledVariant{
		channel:        strings.ToUpper(strings.TrimSpace(src.Channel)),
		locale:         src.Locale,
		referencedVars: referenced,
		declaredVars:   declared,
	}

	if subjectSrc != "" {
		trees, err := parse.Parse("subject", subjectSrc, "{{", "}}", funcs, parseBuiltinStubs)
		if err != nil {
			return nil, fmt.Errorf("parse subject template: %w", err)
		}
		if err := validateTrees(trees, declared, referenced, false); err != nil {
			return nil, fmt.Errorf("subject AST: %w", err)
		}
		t, err := texttmpl.New("subject").Funcs(funcs).Option("missingkey=error").Parse(subjectSrc)
		if err != nil {
			return nil, fmt.Errorf("compile subject template: %w", err)
		}
		out.subject = t
	}

	if textSrc != "" {
		trees, err := parse.Parse("text", textSrc, "{{", "}}", funcs, parseBuiltinStubs)
		if err != nil {
			return nil, fmt.Errorf("parse text template: %w", err)
		}
		if err := validateTrees(trees, declared, referenced, false); err != nil {
			return nil, fmt.Errorf("text AST: %w", err)
		}
		t, err := texttmpl.New("text").Funcs(funcs).Option("missingkey=error").Parse(textSrc)
		if err != nil {
			return nil, fmt.Errorf("compile text template: %w", err)
		}
		out.text = t
	}

	if htmlSrc != "" {
		trees, err := parse.Parse("html", htmlSrc, "{{", "}}", funcs, parseBuiltinStubs)
		if err != nil {
			return nil, fmt.Errorf("parse html template: %w", err)
		}
		if err := validateTrees(trees, declared, referenced, false); err != nil {
			return nil, fmt.Errorf("html AST: %w", err)
		}
		t, err := htmltmpl.New("html").Funcs(funcs).Option("missingkey=error").Parse(htmlSrc)
		if err != nil {
			return nil, fmt.Errorf("compile html template: %w", err)
		}
		out.html = t
	}

	if layoutSrc != "" {
		trees, err := parse.Parse("layout", layoutSrc, "{{", "}}", funcs, parseBuiltinStubs)
		if err != nil {
			return nil, fmt.Errorf("parse layout template: %w", err)
		}
		if err := validateTrees(trees, declared, referenced, true); err != nil {
			return nil, fmt.Errorf("layout AST: %w", err)
		}
		t, err := htmltmpl.New("layout").Funcs(funcs).Option("missingkey=error").Parse(layoutSrc)
		if err != nil {
			return nil, fmt.Errorf("compile layout template: %w", err)
		}
		out.layoutHTML = t
	}

	return out, nil
}

func (e *Engine) Render(compiled *CompiledVariant, rctx RenderContext) (RenderOutput, error) {
	if compiled == nil {
		return RenderOutput{}, errors.New("compiled variant is nil")
	}
	tz := "UTC"
	if rctx.Recipient != nil {
		if rawTZ, ok := rctx.Recipient["timezone"].(string); ok && strings.TrimSpace(rawTZ) != "" {
			tz = strings.TrimSpace(rawTZ)
		}
	}
	funcs := templateFuncMap(tz)
	root := map[string]any{
		"data":      nonNilMap(rctx.Data),
		"recipient": nonNilMap(rctx.Recipient),
		"tenant":    nonNilMap(rctx.Tenant),
		"brand":     nonNilMap(rctx.Brand),
		"system":    nonNilMap(rctx.System),
	}

	out := RenderOutput{
		Channel: compiled.channel,
		Locale:  compiled.locale,
	}

	if compiled.subject != nil {
		cloned, err := compiled.subject.Clone()
		if err != nil {
			return RenderOutput{}, err
		}
		cloned.Funcs(funcs)
		var buf bytes.Buffer
		if err := cloned.Execute(&buf, root); err != nil {
			return RenderOutput{}, fmt.Errorf("execute subject template: %w", err)
		}
		out.Subject = strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(buf.String()))
	}

	if compiled.text != nil {
		cloned, err := compiled.text.Clone()
		if err != nil {
			return RenderOutput{}, err
		}
		cloned.Funcs(funcs)
		var buf bytes.Buffer
		if err := cloned.Execute(&buf, root); err != nil {
			return RenderOutput{}, fmt.Errorf("execute text template: %w", err)
		}
		out.Text = buf.String()
	}

	if compiled.html != nil {
		cloned, err := compiled.html.Clone()
		if err != nil {
			return RenderOutput{}, err
		}
		cloned.Funcs(funcs)
		var buf bytes.Buffer
		if err := cloned.Execute(&buf, root); err != nil {
			return RenderOutput{}, fmt.Errorf("execute html template: %w", err)
		}
		bodyHTML := buf.String()
		if compiled.layoutHTML != nil {
			layoutClone, err := compiled.layoutHTML.Clone()
			if err != nil {
				return RenderOutput{}, err
			}
			layoutClone.Funcs(funcs)
			layoutRoot := map[string]any{
				"data":      root["data"],
				"recipient": root["recipient"],
				"tenant":    root["tenant"],
				"brand":     root["brand"],
				"system":    root["system"],
				"content":   htmltmpl.HTML(bodyHTML),
			}
			var layoutBuf bytes.Buffer
			if err := layoutClone.Execute(&layoutBuf, layoutRoot); err != nil {
				return RenderOutput{}, fmt.Errorf("execute layout template: %w", err)
			}
			bodyHTML = layoutBuf.String()
		}
		out.HTML = bodyHTML
		out.HTMLBytes = len(bodyHTML)
	}

	//Unused variable warnings (deterministic sorted order)
	var unused []string
	for field := range compiled.declaredVars {
		if _, ok := compiled.referencedVars[field]; !ok {
			unused = append(unused, field)
		}
	}
	sort.Strings(unused)
	for _, field := range unused {
		out.Warnings = append(out.Warnings, fmt.Sprintf("declared variable %q is not referenced in variant", field))
	}

	// Channel constraint enforcement
	switch compiled.channel {
	case "EMAIL":
		if out.HTMLBytes > e.limits.MaxEmailHTMLBytes {
			return out, fmt.Errorf("%w: email HTML %d bytes exceeds max %d bytes", ErrRenderLimit, out.HTMLBytes, e.limits.MaxEmailHTMLBytes)
		}
		if out.HTMLBytes > e.limits.WarnEmailHTMLBytes {
			out.Warnings = append(out.Warnings, fmt.Sprintf("email HTML size %d bytes approaches Gmail clipping threshold", out.HTMLBytes))
		}
		if err := ValidateEmailHTMLLinks(out.HTML); err != nil {
			return out, fmt.Errorf("%w: %v", ErrRenderLimit, err)
		}
	case "SMS":
		enc, chars, segs := CalculateSMSSegments(out.Text)
		out.SMSEncoding = enc
		out.SMSCharCount = chars
		out.SMSSegments = segs
		if enc == "UCS2" {
			out.StrippedASCII = StripAccents(out.Text)
		}
		if e.limits.MaxSMSSegments > 0 && segs > e.limits.MaxSMSSegments {
			out.Warnings = append(out.Warnings, fmt.Sprintf("SMS requires %d segments (%s), exceeding recommended limit %d", segs, enc, e.limits.MaxSMSSegments))
		}
	case "PUSH":
		if e.limits.MaxPushBodyBytes > 0 && len(out.Text) > e.limits.MaxPushBodyBytes {
			return out, fmt.Errorf("%w: push body %d bytes exceeds max %d bytes", ErrRenderLimit, len(out.Text), e.limits.MaxPushBodyBytes)
		}
	}

	return out, nil
}

// --- AST Allowlist Validator ---

var allowedFunctions = map[string]bool{
	"formatDate":     true,
	"formatDateTime": true,
	"formatMoney":    true,
	"upper":          true,
	"lower":          true,
	"default":        true,
	"truncate":       true,
	"mask":           true,
	"stripAccents":   true,
	"safeHTML":       true,
	"lookup":         true,
	"log":            true,
	// Safe pure boolean/comparison operators for {{ if ... }}
	"eq":  true,
	"ne":  true,
	"lt":  true,
	"le":  true,
	"gt":  true,
	"ge":  true,
	"and": true,
	"or":  true,
	"not": true,
}

// parseBuiltinStubs registers only the allowlisted Go text/template builtin
// operators for parse.Parse (which does not auto-include template.builtins).
var parseBuiltinStubs = map[string]any{
	"eq":  func(...any) bool { return false },
	"ne":  func(...any) bool { return false },
	"lt":  func(...any) bool { return false },
	"le":  func(...any) bool { return false },
	"gt":  func(...any) bool { return false },
	"ge":  func(...any) bool { return false },
	"and": func(...any) any { return false },
	"or":  func(...any) any { return false },
	"not": func(any) bool { return false },
}

var allowedRootNamespaces = map[string]bool{
	"data":      true,
	"recipient": true,
	"tenant":    true,
	"brand":     true,
	"system":    true,
}

type astScope struct {
	isRoot      bool
	dotSpec     *FieldSpec
	allowLayout bool
	vars        map[string]*FieldSpec
}

func (s astScope) clone() astScope {
	cp := make(map[string]*FieldSpec, len(s.vars))
	for k, v := range s.vars {
		cp[k] = v
	}
	return astScope{
		isRoot:      s.isRoot,
		dotSpec:     s.dotSpec,
		allowLayout: s.allowLayout,
		vars:        cp,
	}
}

func validateTrees(trees map[string]*parse.Tree, declared map[string]FieldSpec, referenced map[string]struct{}, allowLayoutContent bool) error {
	if len(trees) != 1 {
		return fmt.Errorf("%w: define/block sub-templates are forbidden", ErrDisallowedAST)
	}
	for _, tree := range trees {
		if tree == nil || tree.Root == nil {
			continue
		}
		scope := astScope{
			isRoot:      true,
			allowLayout: allowLayoutContent,
			vars:        map[string]*FieldSpec{"$": nil},
		}
		if err := walkNode(tree.Root, scope, declared, referenced); err != nil {
			return err
		}
	}
	return nil
}

func walkNode(node parse.Node, scope astScope, declared map[string]FieldSpec, referenced map[string]struct{}) error {
	if node == nil {
		return nil
	}
	switch n := node.(type) {
	case *parse.ListNode:
		if n == nil {
			return nil
		}
		for _, child := range n.Nodes {
			if err := walkNode(child, scope, declared, referenced); err != nil {
				return err
			}
		}
		return nil

	case *parse.TextNode, *parse.CommentNode, *parse.StringNode, *parse.BoolNode, *parse.NumberNode, *parse.NilNode:
		return nil

	case *parse.DotNode:
		if scope.isRoot {
			return fmt.Errorf("%w: bare root '.' access is forbidden; reference an explicit field", ErrDisallowedAST)
		}
		return nil

	case *parse.TemplateNode:
		return fmt.Errorf("%w: template/block invocation %q is forbidden", ErrDisallowedAST, n.Name)

	case *parse.ActionNode:
		return walkPipe(n.Pipe, scope, declared, referenced)

	case *parse.IfNode:
		if err := walkPipe(n.Pipe, scope, declared, referenced); err != nil {
			return err
		}
		if err := walkNode(n.List, scope.clone(), declared, referenced); err != nil {
			return err
		}
		return walkNode(n.ElseList, scope.clone(), declared, referenced)

	case *parse.WithNode:
		targetSpec, err := inspectPipeTarget(n.Pipe, scope, declared, referenced)
		if err != nil {
			return err
		}
		inner := scope.clone()
		inner.isRoot = false
		inner.dotSpec = targetSpec
		if err := walkNode(n.List, inner, declared, referenced); err != nil {
			return err
		}
		return walkNode(n.ElseList, scope.clone(), declared, referenced)

	case *parse.RangeNode:
		targetSpec, err := inspectPipeTarget(n.Pipe, scope, declared, referenced)
		if err != nil {
			return err
		}
		if targetSpec != nil {
			if targetSpec.Type != "array" {
				return fmt.Errorf("%w: range target must be an array variable", ErrDisallowedAST)
			}
			if targetSpec.MaxItems <= 0 {
				return fmt.Errorf("%w: range array variable must declare maxItems > 0", ErrDisallowedAST)
			}
		}
		inner := scope.clone()
		inner.isRoot = false
		if targetSpec != nil && targetSpec.Items != nil {
			inner.dotSpec = targetSpec.Items
		} else {
			inner.dotSpec = nil
		}
		if len(n.Pipe.Decl) > 0 {
			valVar := n.Pipe.Decl[len(n.Pipe.Decl)-1]
			inner.vars[valVar.Ident[0]] = inner.dotSpec
			if len(n.Pipe.Decl) > 1 {
				idxSpec := FieldSpec{Type: "integer"}
				inner.vars[n.Pipe.Decl[0].Ident[0]] = &idxSpec
			}
		}
		if err := walkNode(n.List, inner, declared, referenced); err != nil {
			return err
		}
		return walkNode(n.ElseList, scope.clone(), declared, referenced)

	case *parse.CommandNode:
		for idx, arg := range n.Args {
			if ident, ok := arg.(*parse.IdentifierNode); ok {
				if ident.Ident == "call" {
					return fmt.Errorf("%w: 'call' builtin is forbidden", ErrDisallowedAST)
				}
				if idx != 0 || !allowedFunctions[ident.Ident] {
					return fmt.Errorf("%w: function %q is not in allowlist", ErrDisallowedAST, ident.Ident)
				}
				if ident.Ident == "safeHTML" {
					if err := verifySafeHTMLArg(n.Args[1:], scope, declared); err != nil {
						return err
					}
				}
				continue
			}
			if err := walkNode(arg, scope, declared, referenced); err != nil {
				return err
			}
		}
		return nil

	case *parse.PipeNode:
		return walkPipe(n, scope, declared, referenced)

	case *parse.FieldNode:
		_, err := resolveFieldPath(n.Ident, scope, declared, referenced)
		return err

	case *parse.VariableNode:
		varName := n.Ident[0]
		if varName == "$" {
			if len(n.Ident) == 1 {
				return fmt.Errorf("%w: bare '$' root access is forbidden", ErrDisallowedAST)
			}
			rootScope := scope.clone()
			rootScope.isRoot = true
			_, err := resolveFieldPath(n.Ident[1:], rootScope, declared, referenced)
			return err
		}
		boundSpec, exists := scope.vars[varName]
		if !exists {
			return fmt.Errorf("%w: undeclared template variable %s", ErrDisallowedAST, varName)
		}
		if len(n.Ident) > 1 && boundSpec != nil {
			_, err := resolveSubSpec(boundSpec, n.Ident[1:])
			return err
		}
		return nil

	case *parse.ChainNode:
		return fmt.Errorf("%w: chained node access is forbidden", ErrDisallowedAST)

	default:
		return fmt.Errorf("%w: unsupported AST node %T", ErrDisallowedAST, node)
	}
}

func walkPipe(pipe *parse.PipeNode, scope astScope, declared map[string]FieldSpec, referenced map[string]struct{}) error {
	if pipe == nil {
		return nil
	}
	for _, cmd := range pipe.Cmds {
		if err := walkNode(cmd, scope, declared, referenced); err != nil {
			return err
		}
	}
	for _, decl := range pipe.Decl {
		if len(decl.Ident) > 0 {
			scope.vars[decl.Ident[0]] = nil
		}
	}
	return nil
}

func inspectPipeTarget(pipe *parse.PipeNode, scope astScope, declared map[string]FieldSpec, referenced map[string]struct{}) (*FieldSpec, error) {
	if err := walkPipe(pipe, scope, declared, referenced); err != nil {
		return nil, err
	}
	if pipe == nil || len(pipe.Cmds) != 1 || len(pipe.Cmds[0].Args) != 1 {
		return nil, nil
	}
	switch arg := pipe.Cmds[0].Args[0].(type) {
	case *parse.FieldNode:
		return resolveFieldPath(arg.Ident, scope, declared, referenced)
	case *parse.VariableNode:
		if arg.Ident[0] == "$" && len(arg.Ident) > 1 {
			rootScope := scope.clone()
			rootScope.isRoot = true
			return resolveFieldPath(arg.Ident[1:], rootScope, declared, referenced)
		}
	}
	return nil, nil
}

func verifySafeHTMLArg(args []parse.Node, scope astScope, declared map[string]FieldSpec) error {
	if len(args) != 1 {
		return nil
	}
	var spec *FieldSpec
	switch arg := args[0].(type) {
	case *parse.FieldNode:
		s, err := resolveFieldPath(arg.Ident, scope, declared, nil)
		if err != nil {
			return err
		}
		spec = s
	case *parse.VariableNode:
		if arg.Ident[0] == "$" && len(arg.Ident) > 1 {
			rootScope := scope.clone()
			rootScope.isRoot = true
			s, err := resolveFieldPath(arg.Ident[1:], rootScope, declared, nil)
			if err != nil {
				return err
			}
			spec = s
		}
	}
	if spec != nil && spec.Type != "html_fragment" && spec.Type != "string" {
		return fmt.Errorf("%w: safeHTML requires html_fragment or string variable, got %s", ErrDisallowedAST, spec.Type)
	}
	return nil
}

func resolveFieldPath(ident []string, scope astScope, declared map[string]FieldSpec, referenced map[string]struct{}) (*FieldSpec, error) {
	if len(ident) == 0 {
		return nil, fmt.Errorf("%w: empty field path", ErrDisallowedAST)
	}
	if !scope.isRoot {
		if scope.dotSpec != nil {
			return resolveSubSpec(scope.dotSpec, ident)
		}
		return nil, nil
	}
	ns := ident[0]
	if scope.allowLayout && ns == "content" && len(ident) == 1 {
		return nil, nil
	}
	if !allowedRootNamespaces[ns] {
		return nil, fmt.Errorf("%w: namespace %q is not allowed (must be data, recipient, tenant, brand, or system)", ErrDisallowedAST, ns)
	}
	if len(ident) < 2 {
		return nil, fmt.Errorf("%w: namespace .%s must reference an explicit property", ErrDisallowedAST, ns)
	}
	if ns != "data" {
		return nil, nil
	}
	topField := ident[1]
	spec, ok := declared[topField]
	if !ok {
		return nil, fmt.Errorf("%w: .data.%s is not declared in variable_schema", ErrDisallowedAST, topField)
	}
	if referenced != nil {
		referenced[topField] = struct{}{}
	}
	if len(ident) > 2 {
		return resolveSubSpec(&spec, ident[2:])
	}
	return &spec, nil
}

func resolveSubSpec(parent *FieldSpec, path []string) (*FieldSpec, error) {
	cur := parent
	for _, seg := range path {
		if cur == nil {
			return nil, nil
		}
		if cur.Type == "money" && (seg == "amount" || seg == "currency") {
			strSpec := FieldSpec{Type: "string"}
			cur = &strSpec
			continue
		}
		if len(cur.Properties) == 0 {
			return nil, fmt.Errorf("%w: property %q does not exist on %s", ErrDisallowedAST, seg, cur.Type)
		}
		next, ok := cur.Properties[seg]
		if !ok {
			return nil, fmt.Errorf("%w: property %q is not declared in nested object schema", ErrDisallowedAST, seg)
		}
		cp := next
		cur = &cp
	}
	return cur, nil
}

// --- Deterministic Pure FuncMap ---

func templateFuncMap(recipientTZ string) map[string]any {
	return map[string]any{
		"formatDate": func(val any, layoutOpt ...string) (string, error) {
			layout := "2006-01-02"
			if len(layoutOpt) > 0 && layoutOpt[0] != "" {
				layout = layoutOpt[0]
			}
			s := fmt.Sprint(val)
			for _, candidate := range []string{"2006-01-02", time.RFC3339, time.RFC3339Nano} {
				if parsed, err := time.Parse(candidate, s); err == nil {
					return parsed.Format(layout), nil
				}
			}
			return "", fmt.Errorf("formatDate: invalid date %q", s)
		},
		"formatDateTime": func(val any, opts ...string) (string, error) {
			tz := recipientTZ
			layout := "2006-01-02 15:04 MST"
			if len(opts) >= 1 && opts[0] != "" {
				tz = opts[0]
			}
			if len(opts) >= 2 && opts[1] != "" {
				layout = opts[1]
			}
			s := fmt.Sprint(val)
			parsed, err := time.Parse(time.RFC3339Nano, s)
			if err != nil {
				parsed, err = time.Parse(time.RFC3339, s)
				if err != nil {
					return "", fmt.Errorf("formatDateTime: invalid RFC3339 datetime %q", s)
				}
			}
			loc, err := time.LoadLocation(tz)
			if err != nil {
				loc = time.UTC
			}
			return parsed.In(loc).Format(layout), nil
		},
		"formatMoney": func(val any, currencyOpt ...string) (string, error) {
			var amountStr, currency string
			switch v := val.(type) {
			case map[string]any:
				amountStr = fmt.Sprint(v["amount"])
				currency = fmt.Sprint(v["currency"])
			case string:
				amountStr = v
			case float64:
				amountStr = strconv.FormatFloat(v, 'f', -1, 64)
			case int, int64:
				amountStr = fmt.Sprint(v)
			default:
				return "", errors.New("formatMoney: unsupported value")
			}
			if len(currencyOpt) > 0 && currencyOpt[0] != "" {
				currency = currencyOpt[0]
			}
			formatted, err := formatDecimalWithGrouping(amountStr)
			if err != nil {
				return "", err
			}
			if currency != "" && currency != "<nil>" {
				return formatted + " " + strings.ToUpper(currency), nil
			}
			return formatted, nil
		},
		"upper": func(s any) string { return strings.ToUpper(fmt.Sprint(s)) },
		"lower": func(s any) string { return strings.ToLower(fmt.Sprint(s)) },
		"default": func(fallback, val any) any {
			if val == nil {
				return fallback
			}
			if s, ok := val.(string); ok && s == "" {
				return fallback
			}
			return val
		},
		"truncate": func(max int, s string) string {
			if max <= 0 {
				return ""
			}
			runes := []rune(s)
			if len(runes) <= max {
				return s
			}
			if max <= 3 {
				return string(runes[:max])
			}
			return string(runes[:max-3]) + "..."
		},
		"mask": func(kind, s string) string {
			return crypto.MaskAddress(kind, s)
		},
		"stripAccents": StripAccents,
		"safeHTML": func(val any) (htmltmpl.HTML, error) {
			if val == nil {
				return "", nil
			}
			return htmltmpl.HTML(SanitizeHTMLFragment(fmt.Sprint(val))), nil
		},
		"lookup": func(container any, key any) any {
			switch c := container.(type) {
			case map[string]any:
				return c[fmt.Sprint(key)]
			case []any:
				if idx, err := strconv.Atoi(fmt.Sprint(key)); err == nil && idx >= 0 && idx < len(c) {
					return c[idx]
				}
			}
			return ""
		},
		"log": func(_ ...any) string {
			return ""
		},
	}
}

func formatDecimalWithGrouping(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("formatMoney: empty amount")
	}
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	} else if strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	parts := strings.Split(s, ".")
	if len(parts) > 2 || parts[0] == "" {
		return "", fmt.Errorf("formatMoney: invalid decimal %q", raw)
	}
	for _, part := range parts {
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				return "", fmt.Errorf("formatMoney: invalid decimal %q", raw)
			}
		}
	}
	intPart := strings.TrimLeft(parts[0], "0")
	if intPart == "" {
		intPart = "0"
	}
	var b strings.Builder
	if neg && intPart != "0" {
		b.WriteByte('-')
	}
	rem := len(intPart) % 3
	if rem > 0 {
		b.WriteString(intPart[:rem])
		if len(intPart) > rem {
			b.WriteByte(',')
		}
	}
	for i := rem; i < len(intPart); i += 3 {
		b.WriteString(intPart[i : i+3])
		if i+3 < len(intPart) {
			b.WriteByte(',')
		}
	}
	if len(parts) == 2 && parts[1] != "" {
		b.WriteByte('.')
		b.WriteString(parts[1])
	}
	return b.String(), nil
}

func nonNilMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// --- Vietnamese Diacritic Stripper & GSM-7 / UCS-2 SMS Calculator ---

var vietnameseAccentReplacer = strings.NewReplacer(
	"à", "a", "á", "a", "ả", "a", "ã", "a", "ạ", "a",
	"ă", "a", "ằ", "a", "ắ", "a", "ẳ", "a", "ẵ", "a", "ặ", "a",
	"â", "a", "ầ", "a", "ấ", "a", "ẩ", "a", "ẫ", "a", "ậ", "a",
	"À", "A", "Á", "A", "Ả", "A", "Ã", "A", "Ạ", "A",
	"Ă", "A", "Ằ", "A", "Ắ", "A", "Ẳ", "A", "Ẵ", "A", "Ặ", "A",
	"Â", "A", "Ầ", "A", "Ấ", "A", "Ẩ", "A", "Ẫ", "A", "Ậ", "A",
	"đ", "d", "Đ", "D",
	"è", "e", "é", "e", "ẻ", "e", "ẽ", "e", "ẹ", "e",
	"ê", "e", "ề", "e", "ế", "e", "ể", "e", "ễ", "e", "ệ", "e",
	"È", "E", "É", "E", "Ẻ", "E", "Ẽ", "E", "Ẹ", "E",
	"Ê", "E", "Ề", "E", "Ế", "E", "Ể", "E", "Ễ", "E", "Ệ", "E",
	"ì", "i", "í", "i", "ỉ", "i", "ĩ", "i", "ị", "i",
	"Ì", "I", "Í", "I", "Ỉ", "I", "Ĩ", "I", "Ị", "I",
	"ò", "o", "ó", "o", "ỏ", "o", "õ", "o", "ọ", "o",
	"ô", "o", "ồ", "o", "ố", "o", "ổ", "o", "ỗ", "o", "ộ", "o",
	"ơ", "o", "ờ", "o", "ớ", "o", "ở", "o", "ỡ", "o", "ợ", "o",
	"Ò", "O", "Ó", "O", "Ỏ", "O", "Õ", "O", "Ọ", "O",
	"Ô", "O", "Ồ", "O", "Ố", "O", "Ổ", "O", "Ỗ", "O", "Ộ", "O",
	"Ơ", "O", "Ờ", "O", "Ớ", "O", "Ở", "O", "Ỡ", "O", "Ợ", "O",
	"ù", "u", "ú", "u", "ủ", "u", "ũ", "u", "ụ", "u",
	"ư", "u", "ừ", "u", "ứ", "u", "ử", "u", "ữ", "u", "ự", "u",
	"Ù", "U", "Ú", "U", "Ủ", "U", "Ũ", "U", "Ụ", "U",
	"Ư", "U", "Ừ", "U", "Ứ", "U", "Ử", "U", "Ữ", "U", "Ự", "U",
	"ỳ", "y", "ý", "y", "ỷ", "y", "ỹ", "y", "ỵ", "y",
	"Ỳ", "Y", "Ý", "Y", "Ỷ", "Y", "Ỹ", "Y", "Ỵ", "Y",
)

func StripAccents(s string) string {
	return vietnameseAccentReplacer.Replace(s)
}

var gsm7BasicSet = func() map[rune]bool {
	const basic = "@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞ !\"#¤%&'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà"
	m := make(map[rune]bool, len(basic))
	for _, r := range basic {
		m[r] = true
	}
	return m
}()

var gsm7ExtSet = func() map[rune]bool {
	const ext = "\f^{}\\[~]|€"
	m := make(map[rune]bool, len(ext))
	for _, r := range ext {
		m[r] = true
	}
	return m
}()

// CalculateSMSSegments detects GSM-7 (160/153 septets) vs UCS-2 (70/67 UTF-16
// code units for Vietnamese diacritics or emoji) and computes exact segments.
func CalculateSMSSegments(text string) (encoding string, charCount int, segments int) {
	if text == "" {
		return "GSM7", 0, 0
	}
	septets := 0
	isGSM7 := true
	for _, r := range text {
		if gsm7BasicSet[r] {
			septets++
		} else if gsm7ExtSet[r] {
			septets += 2
		} else {
			isGSM7 = false
			break
		}
	}
	if isGSM7 {
		if septets <= 160 {
			return "GSM7", septets, 1
		}
		return "GSM7", septets, (septets + 152) / 153
	}
	u16Units := len(utf16.Encode([]rune(text)))
	if u16Units <= 70 {
		return "UCS2", u16Units, 1
	}
	return "UCS2", u16Units, (u16Units + 66) / 67
}
