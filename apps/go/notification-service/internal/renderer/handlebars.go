package renderer

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"text/template/parse"
)

const SyntaxHandlebarsV1 = "handlebars/v1"

// DiagnosticQuickFix describes a suggested text replacement for a diagnostic.
type DiagnosticQuickFix struct {
	Label        string `json:"label"`
	ReplaceStart int    `json:"replace_start"`
	ReplaceEnd   int    `json:"replace_end"`
	Replacement  string `json:"replacement"`
}

// Diagnostic matches the prototype & ADR-013 diagnostic contract.
type Diagnostic struct {
	Code     string              `json:"code"`
	Severity string              `json:"severity"` // "error" | "warning"
	Target   string              `json:"target"`   // "subject" | "html" | "text"
	Line     int                 `json:"line"`
	Column   int                 `json:"column"`
	Message  string              `json:"message"`
	QuickFix *DiagnosticQuickFix `json:"quick_fix,omitempty"`
}

var (
	hbsBlockHelpers = map[string]bool{
		"if":     true,
		"unless": true,
		"each":   true,
		"with":   true,
	}

	hbsInlineHelpers = map[string]string{
		"lookup":         "lookup",
		"log":            "log",
		"eq":             "eq",
		"ne":             "ne",
		"gt":             "gt",
		"lt":             "lt",
		"gte":            "ge",
		"lte":            "le",
		"and":            "and",
		"or":             "or",
		"not":            "not",
		"formatCurrency": "formatMoney",
		"formatMoney":    "formatMoney",
		"formatDate":     "formatDate",
		"formatDateTime": "formatDateTime",
		"upper":          "upper",
		"lower":          "lower",
		"default":        "default",
		"truncate":       "truncate",
		"mask":           "mask",
		"stripAccents":   "stripAccents",
	}

	hbsReservedPrefixes = map[string]bool{
		"recipient": true,
		"tenant":    true,
		"brand":     true,
		"system":    true,
	}

	hbsIdentRe     = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.-]*$`)
	hbsNumRe       = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
	htmlVoidTags   = map[string]bool{
		"area": true, "base": true, "br": true, "col": true, "embed": true,
		"hr": true, "img": true, "input": true, "link": true, "meta": true,
		"param": true, "source": true, "track": true, "wbr": true,
	}
	htmlOpenTagRe  = regexp.MustCompile(`^<([a-zA-Z][a-zA-Z0-9-]*)(\s[^>]*?)?(\/?)>`)
	htmlCloseTagRe = regexp.MustCompile(`^</([a-zA-Z][a-zA-Z0-9-]*)\s*>`)
)

type hbsBlockFrame struct {
	name        string
	line        int
	column      int
	offsetStart int
	offsetEnd   int
	hasElse     bool
	scopesDot   bool
	dotSpec     *FieldSpec
}

// TranspileHandlebars converts a restricted Handlebars template (syntax handlebars/v1)
// into a gotmpl/v1 template string that passes through the exact same AST allowlist,
// missingkey=error, and SSTI protections.
func TranspileHandlebars(src string, declared map[string]FieldSpec) (string, error) {
	if src == "" {
		return "", nil
	}
	out, diags := transpileAndDiagnoseHBS(src, "html", declared, false)
	for _, d := range diags {
		if d.Severity == "error" {
			if d.Code == "FORBIDDEN_CONSTRUCT" || d.Code == "UNDECLARED_VARIABLE" {
				return "", fmt.Errorf("%w: [%s] line %d col %d: %s", ErrDisallowedAST, d.Code, d.Line, d.Column, d.Message)
			}
			return "", fmt.Errorf("[%s] line %d col %d: %s", d.Code, d.Line, d.Column, d.Message)
		}
	}
	return out, nil
}

// Diagnose analyzes subject, html, and text template sources against the declared
// variable schema and returns prototype-compatible diagnostics.
func Diagnose(syntax string, declared map[string]FieldSpec, subject, htmlSrc, textSrc string) []Diagnostic {
	if syntax == "" {
		syntax = SyntaxGoTmplV1
	}
	if declared == nil {
		declared = map[string]FieldSpec{}
	}
	var all []Diagnostic
	switch syntax {
	case SyntaxHandlebarsV1:
		if subject != "" {
			_, d := transpileAndDiagnoseHBS(subject, "subject", declared, false)
			all = append(all, d...)
		}
		if htmlSrc != "" {
			_, d := transpileAndDiagnoseHBS(htmlSrc, "html", declared, true)
			all = append(all, d...)
		}
		if textSrc != "" {
			_, d := transpileAndDiagnoseHBS(textSrc, "text", declared, false)
			all = append(all, d...)
		}
	case SyntaxGoTmplV1:
		all = append(all, diagnoseGoTmpl("subject", subject, declared)...)
		all = append(all, diagnoseGoTmpl("html", htmlSrc, declared)...)
		if htmlSrc != "" && strings.Contains(htmlSrc, "<") {
			all = append(all, validateHTMLTags(htmlSrc, buildLineStarts(htmlSrc))...)
		}
		all = append(all, diagnoseGoTmpl("text", textSrc, declared)...)
	default:
		all = append(all, Diagnostic{
			Code:     "FORBIDDEN_CONSTRUCT",
			Severity: "error",
			Target:   "html",
			Line:     1,
			Column:   1,
			Message:  fmt.Sprintf("unsupported template syntax %q", syntax),
		})
	}
	if all == nil {
		return []Diagnostic{}
	}
	return all
}

func diagnoseGoTmpl(target, src string, declared map[string]FieldSpec) []Diagnostic {
	if src == "" {
		return nil
	}
	funcs := templateFuncMap("UTC")
	trees, err := parse.Parse(target, src, "{{", "}}", funcs, parseBuiltinStubs)
	if err != nil {
		return []Diagnostic{{
			Code:     "INVALID_EXPRESSION_SYNTAX",
			Severity: "error",
			Target:   target,
			Line:     1,
			Column:   1,
			Message:  err.Error(),
		}}
	}
	referenced := make(map[string]struct{})
	if err := validateTrees(trees, declared, referenced, false); err != nil {
		code := "FORBIDDEN_CONSTRUCT"
		if strings.Contains(err.Error(), "not declared") {
			code = "UNDECLARED_VARIABLE"
		}
		return []Diagnostic{{
			Code:     code,
			Severity: "error",
			Target:   target,
			Line:     1,
			Column:   1,
			Message:  err.Error(),
		}}
	}
	return nil
}

func transpileAndDiagnoseHBS(text, target string, declared map[string]FieldSpec, checkHTML bool) (string, []Diagnostic) {
	var out strings.Builder
	var diags []Diagnostic
	lineStarts := buildLineStarts(text)
	var stack []hbsBlockFrame

	pushDiag := func(offset int, severity, code, msg string, fix *DiagnosticQuickFix) {
		line, col := lineColAt(lineStarts, offset)
		diags = append(diags, Diagnostic{
			Code:     code,
			Severity: severity,
			Target:   target,
			Line:     line,
			Column:   col,
			Message:  msg,
			QuickFix: fix,
		})
	}

	inNestedScope := func() bool {
		for k := len(stack) - 1; k >= 0; k-- {
			if stack[k].scopesDot {
				return true
			}
		}
		return false
	}
	currentDotSpec := func() *FieldSpec {
		for k := len(stack) - 1; k >= 0; k-- {
			if stack[k].scopesDot {
				return stack[k].dotSpec
			}
		}
		return nil
	}

	i := 0
	n := len(text)
	for i < n {
		// 1. Comments: {{!-- ... --}} and {{! ... }}
		if strings.HasPrefix(text[i:], "{{!--") {
			closeIdx := strings.Index(text[i+5:], "--}}")
			if closeIdx == -1 {
				pushDiag(i, "error", "UNCLOSED_MUSTACHE", `Handlebars comment "{{!--" is missing closing "--}}".`, &DiagnosticQuickFix{
					Label: "Close comment --}}", ReplaceStart: n, ReplaceEnd: n, Replacement: "--}}",
				})
				break
			}
			i = i + 5 + closeIdx + 4
			continue
		}
		if strings.HasPrefix(text[i:], "{{!") {
			closeIdx := strings.Index(text[i+3:], "}}")
			if closeIdx == -1 {
				pushDiag(i, "error", "UNCLOSED_MUSTACHE", `Handlebars comment "{{!" is missing closing "}}".`, &DiagnosticQuickFix{
					Label: "Close comment }}", ReplaceStart: n, ReplaceEnd: n, Replacement: "}}",
				})
				break
			}
			i = i + 3 + closeIdx + 2
			continue
		}

		// 2. Opening {{ or {{{
		if i+1 < n && text[i] == '{' && text[i+1] == '{' {
			isTripleOpen := i+2 < n && text[i+2] == '{'
			openLen := 2
			if isTripleOpen {
				openLen = 3
			}
			openStart := i
			exprStart := i + openLen

			j := exprStart
			foundClose := false
			isTripleClose := false
			for j < n {
				if j+1 < n && text[j] == '}' && text[j+1] == '}' {
					foundClose = true
					isTripleClose = j+2 < n && text[j+2] == '}'
					break
				}
				if j+1 < n && text[j] == '{' && text[j+1] == '{' {
					break
				}
				if text[j] == '\n' {
					break
				}
				j++
			}
			if !foundClose {
				closeNeeded := "}}"
				if isTripleOpen {
					closeNeeded = "}}}"
				}
				snippetEnd := j
				if snippetEnd > openStart+32 {
					snippetEnd = openStart + 32
				}
				pushDiag(openStart, "error", "UNCLOSED_MUSTACHE",
					fmt.Sprintf("Expression %q is missing closing %q.", strings.TrimSpace(text[openStart:snippetEnd]), closeNeeded),
					&DiagnosticQuickFix{Label: "Add " + closeNeeded, ReplaceStart: j, ReplaceEnd: j, Replacement: closeNeeded},
				)
				i = maxInt(openStart+openLen, j)
				continue
			}

			closeLen := 2
			if isTripleClose {
				closeLen = 3
			}
			exprEnd := j + closeLen
			rawExpr := text[exprStart:j]
			trimmed := strings.TrimSpace(rawExpr)
			fullToken := text[openStart:exprEnd]

			if isTripleOpen != isTripleClose {
				repl := "{{" + rawExpr + "}}"
				if isTripleOpen {
					repl = "{{{" + rawExpr + "}}}"
				}
				pushDiag(openStart, "error", "BRACE_COUNT_MISMATCH",
					fmt.Sprintf("Mismatched mustache braces in %q.", fullToken),
					&DiagnosticQuickFix{Label: "Match braces", ReplaceStart: openStart, ReplaceEnd: exprEnd, Replacement: repl},
				)
			}

			if trimmed == "" {
				pushDiag(openStart, "error", "EMPTY_EXPRESSION",
					fmt.Sprintf("Empty Handlebars expression %q.", fullToken),
					&DiagnosticQuickFix{Label: "Remove empty expression", ReplaceStart: openStart, ReplaceEnd: exprEnd, Replacement: ""},
				)
				i = exprEnd
				continue
			}

			// Reject Go-template / SSTI injection constructs inside Handlebars expressions
			if strings.ContainsAny(trimmed, "|`:$") || strings.HasPrefix(trimmed, ">") || strings.HasPrefix(trimmed, "&") {
				pushDiag(openStart, "error", "FORBIDDEN_CONSTRUCT",
					fmt.Sprintf("Forbidden construct in expression %q.", fullToken), nil)
				i = exprEnd
				continue
			}

			// 3. Block open {{#...}}
			if strings.HasPrefix(trimmed, "#") {
				if isTripleOpen {
					pushDiag(openStart, "error", "FORBIDDEN_CONSTRUCT",
						fmt.Sprintf("Triple mustache cannot be used for block helper %q.", fullToken), nil)
					i = exprEnd
					continue
				}
				afterHash := strings.TrimSpace(trimmed[1:])
				parts, tokErr := tokenizeHBSArgs(afterHash)
				if tokErr != nil || len(parts) == 0 {
					pushDiag(openStart, "error", "INVALID_HELPER",
						fmt.Sprintf("Block opener %q is missing helper name after '#'.", fullToken), nil)
					i = exprEnd
					continue
				}
				helperName := parts[0]
				argTokens := parts[1:]
				line, col := lineColAt(lineStarts, openStart)

				if !hbsBlockHelpers[helperName] {
					pushDiag(openStart, "error", "INVALID_HELPER",
						fmt.Sprintf("Block helper #%s is not supported (allowed: #if, #unless, #each, #with).", helperName), nil)
					stack = append(stack, hbsBlockFrame{name: helperName, line: line, column: col, offsetStart: openStart, offsetEnd: exprEnd})
					i = exprEnd
					continue
				}
				if len(argTokens) == 0 {
					pushDiag(openStart, "error", "MISSING_HELPER_ARG",
						fmt.Sprintf("Block helper {{#%s}} requires an argument.", helperName), nil)
					stack = append(stack, hbsBlockFrame{name: helperName, line: line, column: col, offsetStart: openStart, offsetEnd: exprEnd})
					i = exprEnd
					continue
				}

				goExpr, targetSpec, errCode, errMsg := transpileHBSExpr(argTokens, inNestedScope(), currentDotSpec(), declared)
				if errCode != "" {
					pushDiag(openStart, "error", errCode, errMsg, nil)
				}

				frame := hbsBlockFrame{
					name:        helperName,
					line:        line,
					column:      col,
					offsetStart: openStart,
					offsetEnd:   exprEnd,
				}
				switch helperName {
				case "if":
					out.WriteString("{{ if " + goExpr + " }}")
				case "unless":
					out.WriteString("{{ if not (" + goExpr + ") }}")
				case "each":
					frame.scopesDot = true
					if targetSpec != nil && targetSpec.Items != nil {
						frame.dotSpec = targetSpec.Items
					}
					out.WriteString("{{ range " + goExpr + " }}")
				case "with":
					frame.scopesDot = true
					frame.dotSpec = targetSpec
					out.WriteString("{{ with " + goExpr + " }}")
				}
				stack = append(stack, frame)
				i = exprEnd
				continue
			}

			// 4. Block close {{/...}}
			if strings.HasPrefix(trimmed, "/") {
				closeName := strings.TrimSpace(trimmed[1:])
				if closeName == "" {
					pushDiag(openStart, "error", "INVALID_HELPER",
						fmt.Sprintf("Block closer %q is missing helper name after '/'.", fullToken), nil)
				} else if len(stack) == 0 {
					pushDiag(openStart, "error", "UNEXPECTED_CLOSING_BLOCK",
						fmt.Sprintf("Unexpected closing block {{/%s}} with no matching opener.", closeName),
						&DiagnosticQuickFix{Label: fmt.Sprintf("Remove {{/%s}}", closeName), ReplaceStart: openStart, ReplaceEnd: exprEnd, Replacement: ""},
					)
				} else {
					top := stack[len(stack)-1]
					if top.name == closeName {
						stack = stack[:len(stack)-1]
						out.WriteString("{{ end }}")
					} else {
						matchIdx := -1
						for k := len(stack) - 2; k >= 0; k-- {
							if stack[k].name == closeName {
								matchIdx = k
								break
							}
						}
						if matchIdx != -1 {
							for len(stack)-1 > matchIdx {
								unclosed := stack[len(stack)-1]
								stack = stack[:len(stack)-1]
								pushDiag(unclosed.offsetStart, "error", "UNCLOSED_BLOCK",
									fmt.Sprintf("Block {{#%s}} (line %d) was not closed before {{/%s}}.", unclosed.name, unclosed.line, closeName),
									&DiagnosticQuickFix{Label: fmt.Sprintf("Insert {{/%s}}", unclosed.name), ReplaceStart: openStart, ReplaceEnd: openStart, Replacement: fmt.Sprintf("{{/%s}}", unclosed.name)},
								)
								out.WriteString("{{ end }}")
							}
							stack = stack[:len(stack)-1]
							out.WriteString("{{ end }}")
						} else {
							pushDiag(openStart, "error", "MISMATCHED_CLOSING_BLOCK",
								fmt.Sprintf("Closing block {{/%s}} does not match open block {{#%s}} (line %d).", closeName, top.name, top.line),
								&DiagnosticQuickFix{Label: fmt.Sprintf("Change to {{/%s}}", top.name), ReplaceStart: openStart, ReplaceEnd: exprEnd, Replacement: fmt.Sprintf("{{/%s}}", top.name)},
							)
							stack = stack[:len(stack)-1]
							out.WriteString("{{ end }}")
						}
					}
				}
				i = exprEnd
				continue
			}

			// 5. {{else}} or {{else if ...}}
			if trimmed == "else" || strings.HasPrefix(trimmed, "else ") || strings.HasPrefix(trimmed, "else\t") {
				if len(stack) == 0 {
					pushDiag(openStart, "error", "ORPHAN_ELSE",
						fmt.Sprintf("%q appears outside any block helper.", fullToken),
						&DiagnosticQuickFix{Label: "Remove orphan {{else}}", ReplaceStart: openStart, ReplaceEnd: exprEnd, Replacement: ""},
					)
				} else if trimmed == "else" {
					top := &stack[len(stack)-1]
					if top.hasElse {
						pushDiag(openStart, "error", "DUPLICATE_ELSE",
							fmt.Sprintf("Block {{#%s}} (line %d) already has an {{else}} branch.", top.name, top.line), nil)
					} else {
						top.hasElse = true
						out.WriteString("{{ else }}")
					}
				} else {
					rest := strings.TrimSpace(trimmed[4:])
					if !strings.HasPrefix(rest, "if") || (len(rest) > 2 && rest[2] != ' ' && rest[2] != '\t') {
						pushDiag(openStart, "error", "INVALID_HELPER",
							fmt.Sprintf("Invalid syntax %q after 'else'.", fullToken), nil)
					} else {
						condStr := strings.TrimSpace(rest[2:])
						if condStr == "" {
							pushDiag(openStart, "error", "MISSING_HELPER_ARG",
								fmt.Sprintf("Branch %q is missing condition after 'else if'.", fullToken), nil)
						} else {
							condTokens, tokErr := tokenizeHBSArgs(condStr)
							if tokErr != nil {
								pushDiag(openStart, "error", "INVALID_EXPRESSION_SYNTAX", tokErr.Error(), nil)
							} else {
								goExpr, _, errCode, errMsg := transpileHBSExpr(condTokens, inNestedScope(), currentDotSpec(), declared)
								if errCode != "" {
									pushDiag(openStart, "error", errCode, errMsg, nil)
								}
								out.WriteString("{{ else if " + goExpr + " }}")
							}
						}
					}
				}
				i = exprEnd
				continue
			}

			// 6. Scalar variable or inline helper {{var}} / {{{rawHtml}}} / {{helper arg...}}
			parts, tokErr := tokenizeHBSArgs(trimmed)
			if tokErr != nil || len(parts) == 0 {
				pushDiag(openStart, "error", "INVALID_EXPRESSION_SYNTAX",
					fmt.Sprintf("Invalid expression %q.", fullToken), nil)
				i = exprEnd
				continue
			}

			if isTripleOpen {
				if len(parts) != 1 {
					pushDiag(openStart, "error", "FORBIDDEN_CONSTRUCT",
						fmt.Sprintf("Triple mustache %q only supports a single html_fragment variable.", fullToken), nil)
					i = exprEnd
					continue
				}
				goPath, spec, errCode, errMsg := resolveHBSVarPath(parts[0], inNestedScope(), currentDotSpec(), declared)
				if errCode != "" {
					pushDiag(openStart, "error", errCode, errMsg, nil)
					i = exprEnd
					continue
				}
				if spec == nil || spec.Type != "html_fragment" {
					gotType := "unknown"
					if spec != nil {
						gotType = spec.Type
					}
					pushDiag(openStart, "error", "FORBIDDEN_CONSTRUCT",
						fmt.Sprintf("Triple mustache %q is only allowed for variables declared as type html_fragment (got %s).", fullToken, gotType), nil)
					i = exprEnd
					continue
				}
				out.WriteString("{{ safeHTML " + goPath + " }}")
				i = exprEnd
				continue
			}

			goExpr, _, errCode, errMsg := transpileHBSExpr(parts, inNestedScope(), currentDotSpec(), declared)
			if errCode != "" {
				pushDiag(openStart, "error", errCode, errMsg, nil)
				i = exprEnd
				continue
			}
			out.WriteString("{{ " + goExpr + " }}")
			i = exprEnd
			continue
		}

		// 7. Stray closing }} or }}}
		if i+1 < n && text[i] == '}' && text[i+1] == '}' {
			tokLen := 2
			if i+2 < n && text[i+2] == '}' {
				tokLen = 3
			}
			tok := text[i : i+tokLen]
			pushDiag(i, "error", "UNEXPECTED_CLOSE_MUSTACHE",
				fmt.Sprintf("Unexpected closing %q without matching opening '{{'.", tok),
				&DiagnosticQuickFix{Label: "Remove " + tok, ReplaceStart: i, ReplaceEnd: i + tokLen, Replacement: ""},
			)
			i += tokLen
			continue
		}

		out.WriteByte(text[i])
		i++
	}

	// 8. Remaining unclosed blocks
	for len(stack) > 0 {
		unclosed := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		pushDiag(unclosed.offsetStart, "error", "UNCLOSED_BLOCK",
			fmt.Sprintf("Block {{#%s}} opened at line %d is missing closing {{/%s}}.", unclosed.name, unclosed.line, unclosed.name),
			&DiagnosticQuickFix{Label: fmt.Sprintf("Append {{/%s}}", unclosed.name), ReplaceStart: n, ReplaceEnd: n, Replacement: fmt.Sprintf("{{/%s}}", unclosed.name)},
		)
	}

	// 9. HTML structural tag balance & dangerous tag checks
	if checkHTML && strings.Contains(text, "<") {
		diags = append(diags, validateHTMLTags(text, lineStarts)...)
	}

	return out.String(), diags
}

func transpileHBSExpr(tokens []string, nested bool, dotSpec *FieldSpec, declared map[string]FieldSpec) (string, *FieldSpec, string, string) {
	if len(tokens) == 0 {
		return "", nil, "EMPTY_EXPRESSION", "empty expression"
	}
	if len(tokens) == 1 {
		tok := tokens[0]
		if hbsBlockHelpers[tok] {
			return "", nil, "INVALID_HELPER", fmt.Sprintf("Block helper %q is missing '#' prefix (use {{#%s ...}}).", tok, tok)
		}
		if isHBSLiteral(tok) {
			return normalizeHBSLiteral(tok), nil, "", ""
		}
		return resolveHBSVarPath(tok, nested, dotSpec, declared)
	}

	head := tokens[0]
	if hbsBlockHelpers[head] {
		return "", nil, "INVALID_HELPER", fmt.Sprintf("Block helper %q is missing '#' prefix (use {{#%s ...}}).", head, head)
	}
	goFunc, ok := hbsInlineHelpers[head]
	if !ok {
		return "", nil, "INVALID_HELPER", fmt.Sprintf("Helper %q is not in the supported allowlist.", head)
	}
	args := make([]string, 0, len(tokens)-1)
	for _, arg := range tokens[1:] {
		if isHBSLiteral(arg) {
			args = append(args, normalizeHBSLiteral(arg))
			continue
		}
		goPath, _, errCode, errMsg := resolveHBSVarPath(arg, nested, dotSpec, declared)
		if errCode != "" {
			return "", nil, errCode, errMsg
		}
		args = append(args, goPath)
	}
	return goFunc + " " + strings.Join(args, " "), nil, "", ""
}

func resolveHBSVarPath(tok string, nested bool, dotSpec *FieldSpec, declared map[string]FieldSpec) (string, *FieldSpec, string, string) {
	if tok == "this" || tok == "." {
		if !nested {
			return "", nil, "FORBIDDEN_CONSTRUCT", "bare 'this' or '.' reference at root scope is forbidden"
		}
		return ".", dotSpec, "", ""
	}
	if strings.HasPrefix(tok, "this.") {
		if !nested {
			return "", nil, "FORBIDDEN_CONSTRUCT", "'this.*' is only valid inside #each or #with blocks"
		}
		sub := tok[len("this."):]
		if !hbsIdentRe.MatchString(sub) {
			return "", nil, "INVALID_EXPRESSION_SYNTAX", fmt.Sprintf("invalid identifier %q", tok)
		}
		if dotSpec != nil {
			spec, err := resolveSubSpec(dotSpec, strings.Split(sub, "."))
			if err != nil {
				return "", nil, "UNDECLARED_VARIABLE", err.Error()
			}
			return "." + sub, spec, "", ""
		}
		return "." + sub, nil, "", ""
	}
	if !hbsIdentRe.MatchString(tok) {
		return "", nil, "INVALID_EXPRESSION_SYNTAX", fmt.Sprintf("invalid variable identifier %q", tok)
	}

	segments := strings.Split(tok, ".")
	first := segments[0]

	// Check reserved prefixes (recipient.*, tenant.*, brand.*, system.*)
	if hbsReservedPrefixes[first] {
		if len(segments) < 2 {
			return "", nil, "FORBIDDEN_CONSTRUCT", fmt.Sprintf("reserved namespace %q must reference an explicit property", first)
		}
		if nested {
			return "$." + tok, nil, "", ""
		}
		return "." + tok, nil, "", ""
	}

	// Reject explicit data.* or unknown system namespaces if someone tries .env or similar
	if first == "data" && len(segments) >= 2 {
		// Allow explicit data.foo as synonym for foo
		segments = segments[1:]
		first = segments[0]
		tok = strings.Join(segments, ".")
	}

	if nested {
		if dotSpec != nil {
			if spec, err := resolveSubSpec(dotSpec, segments); err == nil {
				return "." + tok, spec, "", ""
			}
		}
		// Fallback to root $.data.* if variable exists in top-level declared schema
		if rootSpec, exists := declared[first]; exists {
			cp := rootSpec
			if len(segments) > 1 {
				sub, err := resolveSubSpec(&cp, segments[1:])
				if err != nil {
					return "", nil, "UNDECLARED_VARIABLE", err.Error()
				}
				return "$.data." + tok, sub, "", ""
			}
			return "$.data." + tok, &cp, "", ""
		}
		if dotSpec == nil {
			return "." + tok, nil, "", ""
		}
		return "", nil, "UNDECLARED_VARIABLE", fmt.Sprintf("variable %q is not declared in current scope or variable_schema", tok)
	}

	rootSpec, exists := declared[first]
	if !exists {
		return "", nil, "UNDECLARED_VARIABLE", fmt.Sprintf("variable %q is not declared in variable_schema", first)
	}
	cp := rootSpec
	if len(segments) > 1 {
		sub, err := resolveSubSpec(&cp, segments[1:])
		if err != nil {
			return "", nil, "UNDECLARED_VARIABLE", err.Error()
		}
		return ".data." + tok, sub, "", ""
	}
	return ".data." + tok, &cp, "", ""
}

func tokenizeHBSArgs(s string) ([]string, error) {
	var tokens []string
	i := 0
	n := len(s)
	for i < n {
		for i < n && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= n {
			break
		}
		if s[i] == '"' || s[i] == '\'' {
			quote := s[i]
			start := i
			i++
			for i < n && s[i] != quote {
				if s[i] == '\\' && i+1 < n {
					i += 2
					continue
				}
				i++
			}
			if i >= n {
				return nil, errors.New("unterminated string literal in expression")
			}
			i++
			tokens = append(tokens, s[start:i])
		} else {
			start := i
			for i < n && s[i] != ' ' && s[i] != '\t' {
				i++
			}
			tokens = append(tokens, s[start:i])
		}
	}
	return tokens, nil
}

func isHBSLiteral(tok string) bool {
	if len(tok) >= 2 && ((tok[0] == '"' && tok[len(tok)-1] == '"') || (tok[0] == '\'' && tok[len(tok)-1] == '\'')) {
		return true
	}
	if tok == "true" || tok == "false" {
		return true
	}
	return hbsNumRe.MatchString(tok)
}

func normalizeHBSLiteral(tok string) string {
	if len(tok) >= 2 && tok[0] == '\'' && tok[len(tok)-1] == '\'' {
		inner := strings.ReplaceAll(tok[1:len(tok)-1], `"`, `\"`)
		return `"` + inner + `"`
	}
	return tok
}

func validateHTMLTags(text string, lineStarts []int) []Diagnostic {
	type tagFrame struct {
		tag         string
		line        int
		offsetStart int
		offsetEnd   int
	}
	var diags []Diagnostic
	var stack []tagFrame
	n := len(text)
	i := 0

	pushHTML := func(offset int, severity, code, msg string, fix *DiagnosticQuickFix) {
		line, col := lineColAt(lineStarts, offset)
		diags = append(diags, Diagnostic{
			Code:     code,
			Severity: severity,
			Target:   "html",
			Line:     line,
			Column:   col,
			Message:  msg,
			QuickFix: fix,
		})
	}

	for i < n {
		if i+1 < n && text[i] == '{' && text[i+1] == '{' {
			closeIdx := strings.Index(text[i+2:], "}}")
			if closeIdx == -1 {
				break
			}
			i = i + 2 + closeIdx + 2
			continue
		}
		if strings.HasPrefix(text[i:], "<!--") {
			endIdx := strings.Index(text[i+4:], "-->")
			if endIdx == -1 {
				break
			}
			i = i + 4 + endIdx + 3
			continue
		}
		if strings.HasPrefix(text[i:], "<!") {
			endIdx := strings.IndexByte(text[i+2:], '>')
			if endIdx == -1 {
				break
			}
			i = i + 2 + endIdx + 1
			continue
		}
		if text[i] == '<' {
			if m := htmlCloseTagRe.FindStringSubmatchIndex(text[i:]); m != nil {
				tag := strings.ToLower(text[i+m[2] : i+m[3]])
				tagEnd := i + m[1]
				if htmlVoidTags[tag] {
					i = tagEnd
					continue
				}
				if len(stack) == 0 {
					pushHTML(i, "warning", "UNEXPECTED_HTML_CLOSE_TAG",
						fmt.Sprintf("HTML closing tag </%s> has no matching opening <%s>.", tag, tag),
						&DiagnosticQuickFix{Label: fmt.Sprintf("Remove </%s>", tag), ReplaceStart: i, ReplaceEnd: tagEnd, Replacement: ""},
					)
				} else {
					top := stack[len(stack)-1]
					if top.tag == tag {
						stack = stack[:len(stack)-1]
					} else {
						matchIdx := -1
						for k := len(stack) - 2; k >= 0; k-- {
							if stack[k].tag == tag {
								matchIdx = k
								break
							}
						}
						if matchIdx != -1 {
							for len(stack)-1 > matchIdx {
								unclosed := stack[len(stack)-1]
								stack = stack[:len(stack)-1]
								pushHTML(unclosed.offsetStart, "warning", "UNCLOSED_HTML_TAG",
									fmt.Sprintf("HTML tag <%s> (line %d) was not closed before </%s>.", unclosed.tag, unclosed.line, tag),
									&DiagnosticQuickFix{Label: fmt.Sprintf("Insert </%s>", unclosed.tag), ReplaceStart: i, ReplaceEnd: i, Replacement: fmt.Sprintf("</%s>", unclosed.tag)},
								)
							}
							stack = stack[:len(stack)-1]
						} else {
							pushHTML(i, "warning", "MISMATCHED_HTML_TAG",
								fmt.Sprintf("Closing tag </%s> does not match <%s> (line %d).", tag, top.tag, top.line),
								&DiagnosticQuickFix{Label: fmt.Sprintf("Change to </%s>", top.tag), ReplaceStart: i, ReplaceEnd: tagEnd, Replacement: fmt.Sprintf("</%s>", top.tag)},
							)
							stack = stack[:len(stack)-1]
						}
					}
				}
				i = tagEnd
				continue
			}

			if m := htmlOpenTagRe.FindStringSubmatchIndex(text[i:]); m != nil {
				tag := strings.ToLower(text[i+m[2] : i+m[3]])
				isSelfClosing := m[6] != -1 && m[7] > m[6]
				tagEnd := i + m[1]

				if tag == "script" {
					pushHTML(i, "error", "FORBIDDEN_CONSTRUCT", "<script> tags are forbidden in notification templates.", nil)
				}
				if !isSelfClosing && (tag == "style" || tag == "script") {
					closeRe := regexp.MustCompile(`(?i)</` + tag + `\s*>`)
					if loc := closeRe.FindStringIndex(text[tagEnd:]); loc != nil {
						i = tagEnd + loc[1]
					} else {
						i = n
					}
					continue
				}
				if !isSelfClosing && !htmlVoidTags[tag] {
					line, _ := lineColAt(lineStarts, i)
					stack = append(stack, tagFrame{tag: tag, line: line, offsetStart: i, offsetEnd: tagEnd})
				}
				i = tagEnd
				continue
			}
		}
		i++
	}

	for len(stack) > 0 {
		unclosed := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		pushHTML(unclosed.offsetStart, "warning", "UNCLOSED_HTML_TAG",
			fmt.Sprintf("HTML tag <%s> opened at line %d is not closed.", unclosed.tag, unclosed.line),
			&DiagnosticQuickFix{Label: fmt.Sprintf("Append </%s>", unclosed.tag), ReplaceStart: n, ReplaceEnd: n, Replacement: fmt.Sprintf("</%s>", unclosed.tag)},
		)
	}
	return diags
}

func buildLineStarts(text string) []int {
	starts := []int{0}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

func lineColAt(lineStarts []int, offset int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	low, high := 0, len(lineStarts)-1
	for low <= high {
		mid := (low + high) >> 1
		if lineStarts[mid] <= offset {
			if mid == len(lineStarts)-1 || lineStarts[mid+1] > offset {
				return mid + 1, offset - lineStarts[mid] + 1
			}
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	return 1, 1
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
