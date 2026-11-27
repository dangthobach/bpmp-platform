export type DiagnosticSeverity = "error" | "warning";
export type TemplateSourceTarget = "subject" | "html" | "text";

export interface TemplateDiagnosticQuickFix {
  label: string;
  replaceStart: number;
  replaceEnd: number;
  replacement: string;
}

export type TemplateDiagnosticCode =
  | "UNCLOSED_MUSTACHE"
  | "UNEXPECTED_CLOSE_MUSTACHE"
  | "BRACE_COUNT_MISMATCH"
  | "EMPTY_EXPRESSION"
  | "UNCLOSED_BLOCK"
  | "UNEXPECTED_CLOSING_BLOCK"
  | "MISMATCHED_CLOSING_BLOCK"
  | "ORPHAN_ELSE"
  | "DUPLICATE_ELSE"
  | "INVALID_HELPER"
  | "MISSING_HELPER_ARG"
  | "INVALID_EXPRESSION_SYNTAX"
  | "UNCLOSED_HTML_TAG"
  | "MISMATCHED_HTML_TAG"
  | "UNEXPECTED_HTML_CLOSE_TAG"
  | "UNDECLARED_VARIABLE"
  | "FORBIDDEN_CONSTRUCT";

export interface TemplateDiagnostic {
  id: string;
  source: TemplateSourceTarget;
  sourceLabel: string;
  severity: DiagnosticSeverity;
  code: TemplateDiagnosticCode;
  message: string;
  line: number;
  column: number;
  offsetStart: number;
  offsetEnd: number;
  lineSnippet: string;
  tokenText: string;
  suggestion?: string;
  quickFix?: TemplateDiagnosticQuickFix;
  origin?: "client" | "server";
}

export interface TemplateDiagnosticReport {
  diagnostics: TemplateDiagnostic[];
  errorCount: number;
  warningCount: number;
  bySource: Record<TemplateSourceTarget, TemplateDiagnostic[]>;
}

const SUPPORTED_BLOCK_HELPERS = new Set(["if", "unless", "each", "with"]);
const SUPPORTED_INLINE_HELPERS = new Set([
  "lookup",
  "log",
  "eq",
  "ne",
  "gt",
  "lt",
  "gte",
  "lte",
  "and",
  "or",
  "not",
  "formatCurrency",
  "formatDate",
  "stripAccents",
]);

const BUILTIN_ROOT_PREFIXES = new Set(["recipient", "tenant", "brand", "system", "this"]);

const HTML_VOID_ELEMENTS = new Set([
  "area",
  "base",
  "br",
  "col",
  "embed",
  "hr",
  "img",
  "input",
  "link",
  "meta",
  "param",
  "source",
  "track",
  "wbr",
]);

function buildLineStarts(text: string): number[] {
  const starts = [0];
  for (let i = 0; i < text.length; i += 1) {
    if (text[i] === "\n") {
      starts.push(i + 1);
    }
  }
  return starts;
}

function getLineCol(
  text: string,
  lineStarts: number[],
  offset: number,
): { line: number; column: number; lineSnippet: string } {
  const clamped = Math.max(0, Math.min(offset, text.length));
  let low = 0;
  let high = lineStarts.length - 1;
  while (low <= high) {
    const mid = (low + high) >> 1;
    const start = lineStarts[mid] ?? 0;
    const nextStart = lineStarts[mid + 1];
    if (start <= clamped) {
      if (mid === lineStarts.length - 1 || (nextStart !== undefined && nextStart > clamped)) {
        const nextNewline = text.indexOf("\n", start);
        const lineEnd = nextNewline === -1 ? text.length : nextNewline;
        return {
          line: mid + 1,
          column: clamped - start + 1,
          lineSnippet: text.slice(start, lineEnd),
        };
      }
      low = mid + 1;
    } else {
      high = mid - 1;
    }
  }
  return { line: 1, column: 1, lineSnippet: "" };
}

function validateHandlebarsSource(
  text: string,
  source: TemplateSourceTarget,
  sourceLabel: string,
  declaredVariables?: ReadonlySet<string>,
): TemplateDiagnostic[] {
  if (!text) return [];

  const diagnostics: TemplateDiagnostic[] = [];
  const lineStarts = buildLineStarts(text);
  const blockStack: Array<{
    name: string;
    line: number;
    column: number;
    offsetStart: number;
    offsetEnd: number;
    hasElse: boolean;
  }> = [];

  let i = 0;
  const len = text.length;

  const pushDiag = (
    partial: Omit<TemplateDiagnostic, "id" | "source" | "sourceLabel" | "line" | "column" | "lineSnippet" | "origin">,
  ) => {
    const { line, column, lineSnippet } = getLineCol(text, lineStarts, partial.offsetStart);
    diagnostics.push({
      id: `${source}-${partial.code}-${partial.offsetStart}-${diagnostics.length}`,
      source,
      sourceLabel,
      line,
      column,
      lineSnippet,
      origin: "client",
      ...partial,
    });
  };

  while (i < len) {
    if (text.startsWith("{{!--", i)) {
      const closeIdx = text.indexOf("--}}", i + 5);
      if (closeIdx === -1) {
        pushDiag({
          severity: "error",
          code: "UNCLOSED_MUSTACHE",
          message: 'Handlebars comment "{{!--" is missing closing "--}}".',
          offsetStart: i,
          offsetEnd: Math.min(len, i + 5),
          tokenText: "{{!--",
          suggestion: 'Add "--}}" to close the Handlebars comment.',
          quickFix: {
            label: "Close comment --}}",
            replaceStart: len,
            replaceEnd: len,
            replacement: "--}}",
          },
        });
        break;
      }
      i = closeIdx + 4;
      continue;
    }

    if (text.startsWith("{{!", i)) {
      const closeIdx = text.indexOf("}}", i + 3);
      if (closeIdx === -1) {
        pushDiag({
          severity: "error",
          code: "UNCLOSED_MUSTACHE",
          message: 'Handlebars comment "{{!" is missing closing "}}".',
          offsetStart: i,
          offsetEnd: Math.min(len, i + 3),
          tokenText: "{{!",
          quickFix: {
            label: "Close comment }}",
            replaceStart: len,
            replaceEnd: len,
            replacement: "}}",
          },
        });
        break;
      }
      i = closeIdx + 2;
      continue;
    }

    if (text[i] === "{" && text[i + 1] === "{") {
      const isTripleOpen = text[i + 2] === "{";
      const openLen = isTripleOpen ? 3 : 2;
      const openStart = i;
      const exprStart = i + openLen;

      let j = exprStart;
      let foundClose = false;
      let isTripleClose = false;

      while (j < len) {
        if (text[j] === "}" && text[j + 1] === "}") {
          foundClose = true;
          isTripleClose = text[j + 2] === "}";
          break;
        }
        if (text[j] === "{" && text[j + 1] === "{") {
          break;
        }
        if (text[j] === "\n") {
          break;
        }
        j += 1;
      }

      if (!foundClose) {
        const snippetEnd = Math.min(j, openStart + 32);
        const tokenText = text.slice(openStart, snippetEnd);
        const closeNeeded = isTripleOpen ? "}}}" : "}}";
        pushDiag({
          severity: "error",
          code: "UNCLOSED_MUSTACHE",
          message: `Expression "${tokenText.trim()}" is missing closing "${closeNeeded}".`,
          offsetStart: openStart,
          offsetEnd: snippetEnd,
          tokenText,
          suggestion: `Append "${closeNeeded}" after the expression.`,
          quickFix: {
            label: `Add ${closeNeeded}`,
            replaceStart: j,
            replaceEnd: j,
            replacement: closeNeeded,
          },
        });
        i = Math.max(openStart + openLen, j);
        continue;
      }

      const closeLen = isTripleClose ? 3 : 2;
      const closeStart = j;
      const exprEnd = j + closeLen;
      const fullToken = text.slice(openStart, exprEnd);
      const rawExpr = text.slice(exprStart, closeStart);
      const trimmed = rawExpr.trim();

      if (isTripleOpen !== isTripleClose) {
        pushDiag({
          severity: "error",
          code: "BRACE_COUNT_MISMATCH",
          message: isTripleOpen
            ? `Opened with "{{{" but closed with "}}" at "${fullToken}".`
            : `Opened with "{{" but closed with "}}}" at "${fullToken}".`,
          offsetStart: openStart,
          offsetEnd: exprEnd,
          tokenText: fullToken,
          suggestion: isTripleOpen ? 'Change closing braces to "}}}".' : 'Remove extra "}" at the end.',
          quickFix: {
            label: isTripleOpen ? "Normalize to {{{...}}}" : "Normalize to {{...}}",
            replaceStart: openStart,
            replaceEnd: exprEnd,
            replacement: isTripleOpen ? `{{{${rawExpr}}}}` : `{{${rawExpr}}}`,
          },
        });
      }

      if (!trimmed) {
        pushDiag({
          severity: "error",
          code: "EMPTY_EXPRESSION",
          message: `Empty Handlebars expression "${fullToken}".`,
          offsetStart: openStart,
          offsetEnd: exprEnd,
          tokenText: fullToken,
          suggestion: "Enter a variable name or remove the empty expression.",
          quickFix: {
            label: "Remove empty expression",
            replaceStart: openStart,
            replaceEnd: exprEnd,
            replacement: "",
          },
        });
        i = exprEnd;
        continue;
      }

      if (trimmed.startsWith("#")) {
        const afterHash = trimmed.slice(1).trim();
        const match = afterHash.match(/^([a-zA-Z0-9_$-]+)(?:\s+([\s\S]*))?$/);
        const helperName = match?.[1] ?? "";
        const helperArgs = match?.[2]?.trim() ?? "";
        const lc = getLineCol(text, lineStarts, openStart);

        if (!helperName) {
          pushDiag({
            severity: "error",
            code: "INVALID_HELPER",
            message: `Block opener "${fullToken}" is missing a helper name after "#".`,
            offsetStart: openStart,
            offsetEnd: exprEnd,
            tokenText: fullToken,
            suggestion: "Example: {{#if branchName}}...{{/if}}.",
          });
        } else {
          if (!SUPPORTED_BLOCK_HELPERS.has(helperName)) {
            pushDiag({
              severity: "error",
              code: "INVALID_HELPER",
              message: `Block helper "#${helperName}" is not supported by the Notification renderer.`,
              offsetStart: openStart,
              offsetEnd: exprEnd,
              tokenText: fullToken,
              suggestion: "Supported block helpers: #if, #unless, #each, #with.",
            });
          }
          if (!helperArgs) {
            pushDiag({
              severity: "error",
              code: "MISSING_HELPER_ARG",
              message: `Block "{{#${helperName}}}" is missing its condition or target variable.`,
              offsetStart: openStart,
              offsetEnd: exprEnd,
              tokenText: fullToken,
              suggestion: `Provide an argument, e.g. {{#${helperName} branchName}}.`,
            });
          }
          blockStack.push({
            name: helperName,
            line: lc.line,
            column: lc.column,
            offsetStart: openStart,
            offsetEnd: exprEnd,
            hasElse: false,
          });
        }
      } else if (trimmed.startsWith("/")) {
        const closeName = trimmed.slice(1).trim();
        if (!closeName) {
          pushDiag({
            severity: "error",
            code: "INVALID_HELPER",
            message: `Closing block "${fullToken}" is missing a helper name after "/".`,
            offsetStart: openStart,
            offsetEnd: exprEnd,
            tokenText: fullToken,
          });
        } else if (blockStack.length === 0) {
          pushDiag({
            severity: "error",
            code: "UNEXPECTED_CLOSING_BLOCK",
            message: `Unexpected closing block "{{/${closeName}}}" without matching "{{#${closeName}}}".`,
            offsetStart: openStart,
            offsetEnd: exprEnd,
            tokenText: fullToken,
            suggestion: `Remove "{{/${closeName}}}" or open "{{#${closeName} ...}}" earlier.`,
            quickFix: {
              label: `Remove stray {{/${closeName}}}`,
              replaceStart: openStart,
              replaceEnd: exprEnd,
              replacement: "",
            },
          });
        } else {
          const top = blockStack[blockStack.length - 1]!;
          if (top.name === closeName) {
            blockStack.pop();
          } else {
            let matchIdx = -1;
            for (let k = blockStack.length - 2; k >= 0; k -= 1) {
              if (blockStack[k]?.name === closeName) {
                matchIdx = k;
                break;
              }
            }
            if (matchIdx !== -1) {
              while (blockStack.length - 1 > matchIdx) {
                const unclosed = blockStack.pop()!;
                pushDiag({
                  severity: "error",
                  code: "UNCLOSED_BLOCK",
                  message: `Block "{{#${unclosed.name}}}" (line ${unclosed.line}) was not closed before "{{/${closeName}}}".`,
                  offsetStart: unclosed.offsetStart,
                  offsetEnd: unclosed.offsetEnd,
                  tokenText: `{{#${unclosed.name}}}`,
                  suggestion: `Insert "{{/${unclosed.name}}}" before "{{/${closeName}}}".`,
                  quickFix: {
                    label: `Insert {{/${unclosed.name}}}`,
                    replaceStart: openStart,
                    replaceEnd: openStart,
                    replacement: `{{/${unclosed.name}}}`,
                  },
                });
              }
              blockStack.pop();
            } else {
              pushDiag({
                severity: "error",
                code: "MISMATCHED_CLOSING_BLOCK",
                message: `Closing block "{{/${closeName}}}" does not match open "{{#${top.name}}}" at line ${top.line}.`,
                offsetStart: openStart,
                offsetEnd: exprEnd,
                tokenText: fullToken,
                suggestion: `Change closing block to "{{/${top.name}}}".`,
                quickFix: {
                  label: `Change to {{/${top.name}}}`,
                  replaceStart: openStart,
                  replaceEnd: exprEnd,
                  replacement: `{{/${top.name}}}`,
                },
              });
              blockStack.pop();
            }
          }
        }
      } else if (trimmed === "else" || /^else\s+/.test(trimmed)) {
        if (blockStack.length === 0) {
          pushDiag({
            severity: "error",
            code: "ORPHAN_ELSE",
            message: `"${fullToken}" appears outside any {{#if}} or {{#unless}} block.`,
            offsetStart: openStart,
            offsetEnd: exprEnd,
            tokenText: fullToken,
            suggestion: "Use {{else}} only inside {{#if}}...{{/if}} or {{#unless}}...{{/unless}}.",
            quickFix: {
              label: "Remove orphan {{else}}",
              replaceStart: openStart,
              replaceEnd: exprEnd,
              replacement: "",
            },
          });
        } else {
          const top = blockStack[blockStack.length - 1]!;
          if (trimmed === "else") {
            if (top.hasElse) {
              pushDiag({
                severity: "error",
                code: "DUPLICATE_ELSE",
                message: `Block "{{#${top.name}}}" (line ${top.line}) already has an {{else}} branch.`,
                offsetStart: openStart,
                offsetEnd: exprEnd,
                tokenText: fullToken,
              });
            } else {
              top.hasElse = true;
            }
          } else {
            const elseIfMatch = trimmed.match(/^else\s+if(?:\s+(.*))?$/);
            if (!elseIfMatch) {
              pushDiag({
                severity: "error",
                code: "INVALID_HELPER",
                message: `Invalid syntax "${fullToken}" after "else".`,
                offsetStart: openStart,
                offsetEnd: exprEnd,
                tokenText: fullToken,
                suggestion: 'Use "{{else}}" or "{{else if conditionVar}}".',
              });
            } else if (!elseIfMatch[1] || !elseIfMatch[1].trim()) {
              pushDiag({
                severity: "error",
                code: "MISSING_HELPER_ARG",
                message: `Branch "${fullToken}" is missing a condition after "else if".`,
                offsetStart: openStart,
                offsetEnd: exprEnd,
                tokenText: fullToken,
              });
            }
          }
        }
      } else {
        const parts = trimmed.split(/\s+/);
        if (parts.length === 1) {
          const ident = parts[0] ?? "";
          if (SUPPORTED_BLOCK_HELPERS.has(ident)) {
            pushDiag({
              severity: "error",
              code: "INVALID_HELPER",
              message: `Helper "${ident}" is missing "#" prefix (write "{{#${ident} ...}}").`,
              offsetStart: openStart,
              offsetEnd: exprEnd,
              tokenText: fullToken,
              suggestion: `Change to "{{#${ident} conditionVar}}".`,
            });
          } else if (!/^[a-zA-Z_@][a-zA-Z0-9_.-]*$/.test(ident)) {
            pushDiag({
              severity: "error",
              code: "INVALID_EXPRESSION_SYNTAX",
              message: `Variable identifier "${ident}" in "${fullToken}" has invalid syntax.`,
              offsetStart: openStart,
              offsetEnd: exprEnd,
              tokenText: fullToken,
              suggestion: 'Use letters, numbers, "_", ".", or "-" starting with a letter or "_".',
            });
          } else if (declaredVariables && !ident.startsWith("@")) {
            const rootKey = ident.split(".")[0] ?? ident;
            if (!BUILTIN_ROOT_PREFIXES.has(rootKey) && !declaredVariables.has(rootKey)) {
              pushDiag({
                severity: "warning",
                code: "UNDECLARED_VARIABLE",
                message: `Variable "${ident}" is not declared in variable_schema.`,
                offsetStart: openStart,
                offsetEnd: exprEnd,
                tokenText: fullToken,
                suggestion: `Declare "${rootKey}" in the Variable Schema table before publishing.`,
              });
            }
          }
        } else {
          const head = parts[0] ?? "";
          if (SUPPORTED_BLOCK_HELPERS.has(head)) {
            pushDiag({
              severity: "error",
              code: "INVALID_HELPER",
              message: `Block helper "${head}" is missing "#" — write "{{#${trimmed}}}".`,
              offsetStart: openStart,
              offsetEnd: exprEnd,
              tokenText: fullToken,
              suggestion: `Add "#" before "${head}".`,
              quickFix: {
                label: `Fix to {{#${trimmed}}}`,
                replaceStart: openStart,
                replaceEnd: exprEnd,
                replacement: `{{#${trimmed}}}`,
              },
            });
          } else if (!SUPPORTED_INLINE_HELPERS.has(head)) {
            pushDiag({
              severity: "error",
              code: "INVALID_HELPER",
              message: `Unsupported helper "${head}" or whitespace inside variable "${trimmed}".`,
              offsetStart: openStart,
              offsetEnd: exprEnd,
              tokenText: fullToken,
              suggestion: "Use a camelCase variable (e.g. {{customerName}}) or an allowlisted helper (formatCurrency, formatDate, eq).",
            });
          }
        }
      }

      i = exprEnd;
      continue;
    }

    if (text[i] === "}" && text[i + 1] === "}") {
      const isTriple = text[i + 2] === "}";
      const tokLen = isTriple ? 3 : 2;
      const tokenText = text.slice(i, i + tokLen);
      pushDiag({
        severity: "error",
        code: "UNEXPECTED_CLOSE_MUSTACHE",
        message: `Stray closing "${tokenText}" without a matching opening "{{".`,
        offsetStart: i,
        offsetEnd: i + tokLen,
        tokenText,
        suggestion: `Remove stray "${tokenText}" or add "{{" before the variable.`,
        quickFix: {
          label: `Remove stray "${tokenText}"`,
          replaceStart: i,
          replaceEnd: i + tokLen,
          replacement: "",
        },
      });
      i += tokLen;
      continue;
    }

    i += 1;
  }

  while (blockStack.length > 0) {
    const unclosed = blockStack.pop()!;
    pushDiag({
      severity: "error",
      code: "UNCLOSED_BLOCK",
      message: `Block "{{#${unclosed.name}}}" opened at line ${unclosed.line} is missing closing "{{/${unclosed.name}}}".`,
      offsetStart: unclosed.offsetStart,
      offsetEnd: unclosed.offsetEnd,
      tokenText: `{{#${unclosed.name}}}`,
      suggestion: `Add "{{/${unclosed.name}}}" to close the block.`,
      quickFix: {
        label: `Append {{/${unclosed.name}}}`,
        replaceStart: len,
        replaceEnd: len,
        replacement: `{{/${unclosed.name}}}`,
      },
    });
  }

  if (source === "html" && /<[a-zA-Z]/.test(text)) {
    diagnostics.push(...validateHtmlTags(text, lineStarts));
  }

  return diagnostics;
}

function validateHtmlTags(text: string, lineStarts: number[]): TemplateDiagnostic[] {
  const diagnostics: TemplateDiagnostic[] = [];
  const stack: Array<{ tag: string; line: number; offsetStart: number; offsetEnd: number }> = [];
  const len = text.length;
  let i = 0;

  const pushHtmlDiag = (
    partial: Omit<TemplateDiagnostic, "id" | "source" | "sourceLabel" | "line" | "column" | "lineSnippet" | "origin">,
  ) => {
    const { line, column, lineSnippet } = getLineCol(text, lineStarts, partial.offsetStart);
    diagnostics.push({
      id: `html-${partial.code}-${partial.offsetStart}-${diagnostics.length}`,
      source: "html",
      sourceLabel: "HTML / Handlebars Body",
      line,
      column,
      lineSnippet,
      origin: "client",
      ...partial,
    });
  };

  while (i < len) {
    if (text[i] === "{" && text[i + 1] === "{") {
      const closeIdx = text.indexOf("}}", i + 2);
      i = closeIdx === -1 ? len : closeIdx + 2;
      continue;
    }

    if (text.startsWith("<!--", i)) {
      const endComment = text.indexOf("-->", i + 4);
      i = endComment === -1 ? len : endComment + 3;
      continue;
    }

    if (text.startsWith("<!", i)) {
      const endDecl = text.indexOf(">", i + 2);
      i = endDecl === -1 ? len : endDecl + 1;
      continue;
    }

    if (text[i] === "<") {
      const closeMatch = text.slice(i).match(/^<\/([a-zA-Z][a-zA-Z0-9-]*)\s*>/);
      if (closeMatch) {
        const tag = (closeMatch[1] ?? "").toLowerCase();
        const tagEnd = i + closeMatch[0].length;
        if (HTML_VOID_ELEMENTS.has(tag)) {
          i = tagEnd;
          continue;
        }
        if (stack.length === 0) {
          pushHtmlDiag({
            severity: "warning",
            code: "UNEXPECTED_HTML_CLOSE_TAG",
            message: `Closing HTML tag "</${tag}>" has no matching "<${tag}>".`,
            offsetStart: i,
            offsetEnd: tagEnd,
            tokenText: closeMatch[0],
            quickFix: {
              label: `Remove stray </${tag}>`,
              replaceStart: i,
              replaceEnd: tagEnd,
              replacement: "",
            },
          });
        } else {
          const top = stack[stack.length - 1]!;
          if (top.tag === tag) {
            stack.pop();
          } else {
            let matchIdx = -1;
            for (let k = stack.length - 2; k >= 0; k -= 1) {
              if (stack[k]?.tag === tag) {
                matchIdx = k;
                break;
              }
            }
            if (matchIdx !== -1) {
              while (blockDepthMatches(stack.length, matchIdx)) {
                const unclosed = stack.pop()!;
                pushHtmlDiag({
                  severity: "warning",
                  code: "UNCLOSED_HTML_TAG",
                  message: `HTML tag "<${unclosed.tag}>" (line ${unclosed.line}) is not closed before "</${tag}>".`,
                  offsetStart: unclosed.offsetStart,
                  offsetEnd: unclosed.offsetEnd,
                  tokenText: `<${unclosed.tag}>`,
                  quickFix: {
                    label: `Insert </${unclosed.tag}>`,
                    replaceStart: i,
                    replaceEnd: i,
                    replacement: `</${unclosed.tag}>`,
                  },
                });
              }
              stack.pop();
            } else {
              pushHtmlDiag({
                severity: "warning",
                code: "MISMATCHED_HTML_TAG",
                message: `Closing tag "</${tag}>" does not match "<${top.tag}>" (line ${top.line}).`,
                offsetStart: i,
                offsetEnd: tagEnd,
                tokenText: closeMatch[0],
                quickFix: {
                  label: `Change to </${top.tag}>`,
                  replaceStart: i,
                  replaceEnd: tagEnd,
                  replacement: `</${top.tag}>`,
                },
              });
              stack.pop();
            }
          }
        }
        i = tagEnd;
        continue;
      }

      const openMatch = text.slice(i).match(/^<([a-zA-Z][a-zA-Z0-9-]*)(\s[^>]*?)?(\/?)>/);
      if (openMatch) {
        const tag = (openMatch[1] ?? "").toLowerCase();
        const isSelfClosing = openMatch[3] === "/";
        const tagEnd = i + openMatch[0].length;

        if (!isSelfClosing && (tag === "style" || tag === "script")) {
          if (tag === "script") {
            pushHtmlDiag({
              severity: "error",
              code: "FORBIDDEN_CONSTRUCT",
              message: "Embedded <script> tags are forbidden in notification templates.",
              offsetStart: i,
              offsetEnd: tagEnd,
              tokenText: openMatch[0],
              suggestion: "Remove <script> elements; templates are rendered in a strict sandbox.",
            });
          }
          const closeTagRegex = new RegExp(`</${tag}\\s*>`, "i");
          const rest = text.slice(tagEnd);
          const endMatch = closeTagRegex.exec(rest);
          i = endMatch ? tagEnd + endMatch.index + endMatch[0].length : len;
          continue;
        }

        if (!isSelfClosing && !HTML_VOID_ELEMENTS.has(tag)) {
          const lc = getLineCol(text, lineStarts, i);
          stack.push({
            tag,
            line: lc.line,
            offsetStart: i,
            offsetEnd: tagEnd,
          });
        }
        i = tagEnd;
        continue;
      }
    }
    i += 1;
  }

  while (stack.length > 0) {
    const unclosed = stack.pop()!;
    pushHtmlDiag({
      severity: "warning",
      code: "UNCLOSED_HTML_TAG",
      message: `HTML tag "<${unclosed.tag}>" opened at line ${unclosed.line} is missing "</${unclosed.tag}>".`,
      offsetStart: unclosed.offsetStart,
      offsetEnd: unclosed.offsetEnd,
      tokenText: `<${unclosed.tag}>`,
      quickFix: {
        label: `Append </${unclosed.tag}>`,
        replaceStart: len,
        replaceEnd: len,
        replacement: `</${unclosed.tag}>`,
      },
    });
  }

  return diagnostics;
}

function blockDepthMatches(length: number, matchIdx: number): boolean {
  return length - 1 > matchIdx;
}

export function diagnoseTemplateSources(sources: {
  subjectOrTitle: string;
  subjectLabel?: string;
  htmlBody: string;
  textBody: string;
  declaredVariables?: ReadonlySet<string>;
}): TemplateDiagnosticReport {
  const subjectDiags = validateHandlebarsSource(
    sources.subjectOrTitle,
    "subject",
    sources.subjectLabel ?? "Subject / Title",
    sources.declaredVariables,
  );
  const htmlDiags = validateHandlebarsSource(
    sources.htmlBody,
    "html",
    "HTML / Handlebars Body",
    sources.declaredVariables,
  );
  const textDiags = validateHandlebarsSource(
    sources.textBody,
    "text",
    "Plain Text Body",
    sources.declaredVariables,
  );

  const diagnostics = [...subjectDiags, ...htmlDiags, ...textDiags];

  return {
    diagnostics,
    errorCount: diagnostics.filter((d) => d.severity === "error").length,
    warningCount: diagnostics.filter((d) => d.severity === "warning").length,
    bySource: {
      subject: subjectDiags,
      html: htmlDiags,
      text: textDiags,
    },
  };
}

export function applyDiagnosticQuickFix(source: string, quickFix: TemplateDiagnosticQuickFix): string {
  const start = Math.max(0, Math.min(quickFix.replaceStart, source.length));
  const end = Math.max(start, Math.min(quickFix.replaceEnd, source.length));
  return `${source.slice(0, start)}${quickFix.replacement}${source.slice(end)}`;
}
