package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/crypto"
	"github.com/dangthobach/bpmp-platform/apps/go/notification-service/internal/renderer"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var (
	ErrInvalidSchema      = errors.New("variable_schema is invalid")
	ErrPayloadInvalid     = errors.New("notification data payload failed schema validation")
	ErrIncompatibleSchema = errors.New("variable_schema breaks backward compatibility within schema_major")
)

var decimalPattern = regexp.MustCompile(`^-?\d+(\.\d+)?$`)

type PropertyMeta struct {
	Name       string
	DomainType string
	Required   bool
	MaxLength  *int
	MaxItems   *int
	Enum       []string
	Pattern    *regexp.Regexp
	PII        bool
	Secret     bool
	HasDefault bool
	Example    any
	HasExample bool
	Properties map[string]*PropertyMeta
	Items      *PropertyMeta
}

type CompiledSchema struct {
	raw        []byte
	validator  *jsonschema.Schema
	rootProps  map[string]*PropertyMeta
	fieldSpecs map[string]renderer.FieldSpec
	hasSecrets bool
}

func Compile(raw []byte) (*CompiledSchema, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fmt.Errorf("%w: empty schema", ErrInvalidSchema)
	}
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON: %v", ErrInvalidSchema, err)
	}
	rootType, _ := doc["type"].(string)
	if rootType == "" {
		rootType = "object"
	}
	if rootType != "object" {
		return nil, fmt.Errorf("%w: root type must be object, got %q", ErrInvalidSchema, rootType)
	}

	rootProps, normalizedDoc, hasSecrets, err := parseObjectNode(doc, "$")
	if err != nil {
		return nil, err
	}
	normalizedDoc["$schema"] = "https://json-schema.org/draft/2020-12/schema"

	normBytes, err := json.Marshal(normalizedDoc)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal normalized schema: %v", ErrInvalidSchema, err)
	}
	parsedResource, err := jsonschema.UnmarshalJSON(bytes.NewReader(normBytes))
	if err != nil {
		return nil, fmt.Errorf("%w: unmarshal normalized schema: %v", ErrInvalidSchema, err)
	}
	compiler := jsonschema.NewCompiler()
	const resourceURL = "urn:bpmp:notification:variable-schema"
	if err := compiler.AddResource(resourceURL, parsedResource); err != nil {
		return nil, fmt.Errorf("%w: add resource: %v", ErrInvalidSchema, err)
	}
	validator, err := compiler.Compile(resourceURL)
	if err != nil {
		return nil, fmt.Errorf("%w: compile jsonschema: %v", ErrInvalidSchema, err)
	}

	specs := make(map[string]renderer.FieldSpec, len(rootProps))
	for name, meta := range rootProps {
		specs[name] = toFieldSpec(meta)
	}

	return &CompiledSchema{
		raw:        append([]byte(nil), raw...),
		validator:  validator,
		rootProps:  rootProps,
		fieldSpecs: specs,
		hasSecrets: hasSecrets,
	}, nil
}

func (s *CompiledSchema) FieldSpecs() map[string]renderer.FieldSpec {
	out := make(map[string]renderer.FieldSpec, len(s.fieldSpecs))
	for k, v := range s.fieldSpecs {
		out[k] = v
	}
	return out
}

func (s *CompiledSchema) HasSecrets() bool {
	return s.hasSecrets
}

func (s *CompiledSchema) Properties() map[string]*PropertyMeta {
	return s.rootProps
}

func (s *CompiledSchema) ValidatePayload(rawJSON []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(rawJSON)) == 0 {
		rawJSON = []byte(`{}`)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(rawJSON))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid JSON payload: %v", ErrPayloadInvalid, err)
	}
	obj, ok := inst.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: payload root must be a JSON object", ErrPayloadInvalid)
	}
	if err := s.validator.Validate(inst); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPayloadInvalid, err)
	}
	if err := validateDomainConstraints(obj, s.rootProps, "$"); err != nil {
		return nil, err
	}
	return obj, nil
}

// ValidateAndSplit validates rawJSON and separates `secret: true` fields from
// regular payload fields so secrets can be stored in `encrypted_secrets` and
// wiped immediately upon delivery completion/expiry.
func (s *CompiledSchema) ValidateAndSplit(rawJSON []byte) (cleanData map[string]any, secrets map[string]any, err error) {
	validated, err := s.ValidatePayload(rawJSON)
	if err != nil {
		return nil, nil, err
	}
	cleanData = make(map[string]any, len(validated))
	secrets = make(map[string]any)
	splitObject(validated, s.rootProps, cleanData, secrets)
	return cleanData, secrets, nil
}

func splitObject(src map[string]any, props map[string]*PropertyMeta, cleanDst, secretDst map[string]any) {
	for k, v := range src {
		meta := props[k]
		if meta != nil && meta.Secret {
			secretDst[k] = v
			continue
		}
		if meta != nil && meta.DomainType == "object" && len(meta.Properties) > 0 {
			if subMap, ok := v.(map[string]any); ok {
				subClean := make(map[string]any)
				subSec := make(map[string]any)
				splitObject(subMap, meta.Properties, subClean, subSec)
				cleanDst[k] = subClean
				if len(subSec) > 0 {
					secretDst[k] = subSec
				}
				continue
			}
		}
		cleanDst[k] = v
	}
}

func MergeDataAndSecrets(cleanData, secrets map[string]any) map[string]any {
	out := cloneMap(cleanData)
	mergeInto(out, secrets)
	return out
}

func mergeInto(dst, src map[string]any) {
	for k, v := range src {
		if subSrc, ok := v.(map[string]any); ok {
			if existing, ok := dst[k].(map[string]any); ok {
				mergeInto(existing, subSrc)
				continue
			}
		}
		dst[k] = v
	}
}

// MaskData produces a safe copy for preview/replay: secret fields are always
// replaced with "******" (including erased secrets), and pii fields are masked
// when maskPII is true.
func (s *CompiledSchema) MaskData(data map[string]any, maskPII bool) map[string]any {
	src := cloneMap(data)
	return maskObject(src, s.rootProps, maskPII)
}

func maskObject(src map[string]any, props map[string]*PropertyMeta, maskPII bool) map[string]any {
	out := cloneMap(src)
	for name, meta := range props {
		val, exists := out[name]
		if meta.Secret {
			out[name] = "******"
			continue
		}
		if !exists {
			continue
		}
		if meta.PII && maskPII {
			out[name] = crypto.MaskAddress("", fmt.Sprint(val))
			continue
		}
		if meta.DomainType == "object" && len(meta.Properties) > 0 {
			if sub, ok := val.(map[string]any); ok {
				out[name] = maskObject(sub, meta.Properties, maskPII)
			}
		}
	}
	return out
}

// BuildExampleData constructs a sample payload from declared `example` values
// (falling back to deterministic type-valid sample values when omitted).
func (s *CompiledSchema) BuildExampleData() map[string]any {
	return buildExampleObject(s.rootProps)
}

func buildExampleObject(props map[string]*PropertyMeta) map[string]any {
	out := make(map[string]any, len(props))
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out[k] = sampleForMeta(props[k])
	}
	return out
}

func sampleForMeta(meta *PropertyMeta) any {
	if meta.HasExample {
		return normalizeJSONNumbers(meta.Example)
	}
	switch meta.DomainType {
	case "string":
		if len(meta.Enum) > 0 {
			return meta.Enum[0]
		}
		return "sample-" + meta.Name
	case "integer":
		return 1
	case "number":
		return 1.0
	case "boolean":
		return true
	case "decimal":
		return "1000.00"
	case "money":
		return map[string]any{"amount": "1500000", "currency": "VND"}
	case "date":
		return "2026-11-26"
	case "datetime":
		return "2026-11-26T09:00:00Z"
	case "url":
		return "https://portal.bpmp.example.com/view"
	case "html_fragment":
		return "<b>Sample notice</b>"
	case "object":
		return buildExampleObject(meta.Properties)
	case "array":
		if meta.Items != nil {
			return []any{sampleForMeta(meta.Items)}
		}
		return []any{}
	default:
		return "sample"
	}
}

// CheckBackwardCompatible verifies that `nextJSON` accepts every valid payload
// accepted by `prevJSON` within the same `schema_major` (ADR-013 §4.2):
// - Adding optional fields is allowed.
// - Removing existing fields, changing field types, shrinking bounds/enums, or
//   adding new required fields is rejected.
func CheckBackwardCompatible(prevJSON, nextJSON []byte) error {
	prev, err := Compile(prevJSON)
	if err != nil {
		return fmt.Errorf("previous schema: %w", err)
	}
	next, err := Compile(nextJSON)
	if err != nil {
		return fmt.Errorf("next schema: %w", err)
	}
	return checkObjectCompat(prev.rootProps, next.rootProps, "$")
}

func checkObjectCompat(prevProps, nextProps map[string]*PropertyMeta, path string) error {
	for name, prevProp := range prevProps {
		nextProp, exists := nextProps[name]
		if !exists {
			return fmt.Errorf("%w: field %s.%s was removed", ErrIncompatibleSchema, path, name)
		}
		if err := checkPropCompat(prevProp, nextProp, path+"."+name); err != nil {
			return err
		}
	}
	for name, nextProp := range nextProps {
		prevProp, existed := prevProps[name]
		if !existed {
			if nextProp.Required {
				return fmt.Errorf("%w: newly added field %s.%s cannot be required", ErrIncompatibleSchema, path, name)
			}
			continue
		}
		if !prevProp.Required && nextProp.Required {
			return fmt.Errorf("%w: optional field %s.%s cannot become required", ErrIncompatibleSchema, path, name)
		}
	}
	return nil
}

func checkPropCompat(prev, next *PropertyMeta, path string) error {
	if prev.DomainType != next.DomainType {
		return fmt.Errorf("%w: field %s changed type from %s to %s", ErrIncompatibleSchema, path, prev.DomainType, next.DomainType)
	}
	if prev.MaxLength != nil && next.MaxLength != nil && *next.MaxLength < *prev.MaxLength {
		return fmt.Errorf("%w: field %s maxLength shrunk from %d to %d", ErrIncompatibleSchema, path, *prev.MaxLength, *next.MaxLength)
	}
	if prev.MaxLength == nil && next.MaxLength != nil {
		return fmt.Errorf("%w: field %s added stricter maxLength %d", ErrIncompatibleSchema, path, *next.MaxLength)
	}
	if prev.MaxItems != nil && next.MaxItems != nil && *next.MaxItems < *prev.MaxItems {
		return fmt.Errorf("%w: field %s maxItems shrunk from %d to %d", ErrIncompatibleSchema, path, *prev.MaxItems, *next.MaxItems)
	}
	if len(prev.Enum) > 0 && len(next.Enum) > 0 {
		nextSet := make(map[string]bool, len(next.Enum))
		for _, v := range next.Enum {
			nextSet[v] = true
		}
		for _, oldVal := range prev.Enum {
			if !nextSet[oldVal] {
				return fmt.Errorf("%w: field %s removed enum value %q", ErrIncompatibleSchema, path, oldVal)
			}
		}
	} else if len(prev.Enum) == 0 && len(next.Enum) > 0 {
		return fmt.Errorf("%w: field %s restricted unconstrained values to enum", ErrIncompatibleSchema, path)
	}
	if prev.DomainType == "object" {
		if err := checkObjectCompat(prev.Properties, next.Properties, path); err != nil {
			return err
		}
	}
	if prev.DomainType == "array" && prev.Items != nil && next.Items != nil {
		if err := checkPropCompat(prev.Items, next.Items, path+"[]"); err != nil {
			return err
		}
	}
	return nil
}

// --- Internal Schema Subset Parser & Semantic Validators ---

var supportedTypes = map[string]bool{
	"string":        true,
	"integer":       true,
	"number":        true,
	"boolean":       true,
	"decimal":       true,
	"money":         true,
	"date":          true,
	"datetime":      true,
	"url":           true,
	"html_fragment": true,
	"object":        true,
	"array":         true,
}

func parseObjectNode(node map[string]any, path string) (map[string]*PropertyMeta, map[string]any, bool, error) {
	reqSet := make(map[string]bool)
	if rawReq, ok := node["required"]; ok {
		arr, ok := rawReq.([]any)
		if !ok {
			return nil, nil, false, fmt.Errorf("%w: %s.required must be an array of strings", ErrInvalidSchema, path)
		}
		for _, item := range arr {
			s, ok := item.(string)
			if !ok || s == "" {
				return nil, nil, false, fmt.Errorf("%w: %s.required contains invalid property name", ErrInvalidSchema, path)
			}
			reqSet[s] = true
		}
	}

	propsOut := make(map[string]*PropertyMeta)
	normProps := make(map[string]any)
	hasSecrets := false

	if rawProps, ok := node["properties"]; ok {
		propMap, ok := rawProps.(map[string]any)
		if !ok {
			return nil, nil, false, fmt.Errorf("%w: %s.properties must be an object", ErrInvalidSchema, propMap)
		}
		for propName, rawSub := range propMap {
			subMap, ok := rawSub.(map[string]any)
			if !ok {
				return nil, nil, false, fmt.Errorf("%w: %s.%s must be a schema object", ErrInvalidSchema, path, propName)
			}
			meta, normSub, subSec, err := parsePropertyNode(propName, subMap, reqSet[propName], path+"."+propName)
			if err != nil {
				return nil, nil, false, err
			}
			if subSec {
				hasSecrets = true
			}
			propsOut[propName] = meta
			normProps[propName] = normSub
		}
	}

	for reqName := range reqSet {
		if _, exists := propsOut[reqName]; !exists {
			return nil, nil, false, fmt.Errorf("%w: %s.required references undeclared property %q", ErrInvalidSchema, path, reqName)
		}
	}

	normObj := map[string]any{
		"type":       "object",
		"properties": normProps,
	}
	if len(reqSet) > 0 {
		reqList := make([]string, 0, len(reqSet))
		for k := range reqSet {
			reqList = append(reqList, k)
		}
		sort.Strings(reqList)
		normObj["required"] = reqList
	}
	if addl, ok := node["additionalProperties"]; ok {
		normObj["additionalProperties"] = addl
	}
	return propsOut, normObj, hasSecrets, nil
}

func parsePropertyNode(name string, node map[string]any, required bool, path string) (*PropertyMeta, map[string]any, bool, error) {
	rawType, _ := node["type"].(string)
	format, _ := node["format"].(string)
	domainType := rawType
	if rawType == "string" && format != "" {
		switch format {
		case "date":
			domainType = "date"
		case "date-time", "datetime":
			domainType = "datetime"
		case "uri", "url":
			domainType = "url"
		case "decimal":
			domainType = "decimal"
		case "html_fragment":
			domainType = "html_fragment"
		}
	}
	if !supportedTypes[domainType] {
		return nil, nil, false, fmt.Errorf("%w: %s has unsupported type %q", ErrInvalidSchema, path, domainType)
	}

	meta := &PropertyMeta{
		Name:       name,
		DomainType: domainType,
		Required:   required,
	}
	if pii, ok := node["pii"].(bool); ok {
		meta.PII = pii
	}
	if sec, ok := node["secret"].(bool); ok {
		meta.Secret = sec
	}
	if _, ok := node["default"]; ok {
		meta.HasDefault = true
	}
	if ex, ok := node["example"]; ok {
		meta.Example = ex
		meta.HasExample = true
	}

	hasSecrets := meta.Secret
	norm := make(map[string]any)

	switch domainType {
	case "string", "decimal", "date", "datetime", "url", "html_fragment":
		norm["type"] = "string"
		if ml, ok, err := readOptionalPositiveInt(node, "maxLength", path); err != nil {
			return nil, nil, false, err
		} else if ok {
			meta.MaxLength = &ml
			norm["maxLength"] = ml
		}
		if minL, ok, err := readOptionalNonNegInt(node, "minLength", path); err != nil {
			return nil, nil, false, err
		} else if ok {
			norm["minLength"] = minL
		}
		if rawPat, ok := node["pattern"]; ok {
			patStr, ok := rawPat.(string)
			if !ok {
				return nil, nil, false, fmt.Errorf("%w: %s.pattern must be string", ErrInvalidSchema, path)
			}
			compiledPat, err := regexp.Compile(patStr)
			if err != nil {
				return nil, nil, false, fmt.Errorf("%w: %s.pattern invalid RE2 regex: %v", ErrInvalidSchema, path, err)
			}
			meta.Pattern = compiledPat
			norm["pattern"] = patStr
		}
		if rawEnum, ok := node["enum"]; ok {
			arr, ok := rawEnum.([]any)
			if !ok || len(arr) == 0 {
				return nil, nil, false, fmt.Errorf("%w: %s.enum must be a non-empty array", ErrInvalidSchema, path)
			}
			enums := make([]string, 0, len(arr))
			for _, e := range arr {
				s, ok := e.(string)
				if !ok {
					return nil, nil, false, fmt.Errorf("%w: %s.enum values must be strings", ErrInvalidSchema, path)
				}
				enums = append(enums, s)
			}
			meta.Enum = enums
			norm["enum"] = arr
		}

	case "integer", "number":
		norm["type"] = domainType
		for _, bound := range []string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum"} {
			if v, ok := node[bound]; ok {
				norm[bound] = v
			}
		}

	case "boolean":
		norm["type"] = "boolean"

	case "money":
		norm["type"] = "object"
		norm["required"] = []string{"amount", "currency"}
		norm["properties"] = map[string]any{
			"amount":   map[string]any{"type": "string"},
			"currency": map[string]any{"type": "string", "minLength": 3, "maxLength": 3},
		}
		norm["additionalProperties"] = false

	case "object":
		subProps, normObj, subSec, err := parseObjectNode(node, path)
		if err != nil {
			return nil, nil, false, err
		}
		if subSec {
			hasSecrets = true
		}
		meta.Properties = subProps
		norm = normObj

	case "array":
		maxItems, ok, err := readOptionalPositiveInt(node, "maxItems", path)
		if err != nil {
			return nil, nil, false, err
		}
		if !ok || maxItems <= 0 {
			return nil, nil, false, fmt.Errorf("%w: array %s must declare positive maxItems", ErrInvalidSchema, path)
		}
		meta.MaxItems = &maxItems
		rawItems, ok := node["items"].(map[string]any)
		if !ok {
			return nil, nil, false, fmt.Errorf("%w: array %s must declare an items schema object", ErrInvalidSchema, path)
		}
		itemMeta, normItem, itemSec, err := parsePropertyNode("item", rawItems, false, path+"[]")
		if err != nil {
			return nil, nil, false, err
		}
		if itemSec {
			hasSecrets = true
		}
		meta.Items = itemMeta
		norm["type"] = "array"
		norm["maxItems"] = maxItems
		norm["items"] = normItem
		if minI, ok, err := readOptionalNonNegInt(node, "minItems", path); err != nil {
			return nil, nil, false, err
		} else if ok {
			norm["minItems"] = minI
		}
	}

	return meta, norm, hasSecrets, nil
}

func validateDomainConstraints(obj map[string]any, props map[string]*PropertyMeta, path string) error {
	for name, val := range obj {
		meta := props[name]
		if meta == nil || val == nil {
			continue
		}
		if err := validateValueDomain(val, meta, path+"."+name); err != nil {
			return err
		}
	}
	return nil
}

func validateValueDomain(val any, meta *PropertyMeta, path string) error {
	switch meta.DomainType {
	case "decimal":
		s, _ := val.(string)
		if !decimalPattern.MatchString(s) {
			return fmt.Errorf("%w: %s must be an exact decimal string", ErrPayloadInvalid, path)
		}
	case "money":
		m, ok := val.(map[string]any)
		if !ok {
			return fmt.Errorf("%w: %s must be a money object", ErrPayloadInvalid, path)
		}
		amt, _ := m["amount"].(string)
		cur, _ := m["currency"].(string)
		if !decimalPattern.MatchString(amt) || len(strings.TrimSpace(cur)) != 3 {
			return fmt.Errorf("%w: %s must have valid decimal amount and 3-letter currency", ErrPayloadInvalid, path)
		}
	case "date":
		s, _ := val.(string)
		if len(s) != 10 {
			return fmt.Errorf("%w: %s must be YYYY-MM-DD", ErrPayloadInvalid, path)
		}
		if _, err := time.Parse("2006-01-02", s); err != nil {
			return fmt.Errorf("%w: %s invalid date: %v", ErrPayloadInvalid, path, err)
		}
	case "datetime":
		s, _ := val.(string)
		if _, err := time.Parse(time.RFC3339Nano, s); err != nil {
			return fmt.Errorf("%w: %s must be RFC3339 datetime: %v", ErrPayloadInvalid, path, err)
		}
	case "url":
		s, _ := val.(string)
		u, err := url.Parse(s)
		if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" {
			return fmt.Errorf("%w: %s must be an https:// URL", ErrPayloadInvalid, path)
		}
	case "object":
		if m, ok := val.(map[string]any); ok {
			return validateDomainConstraints(m, meta.Properties, path)
		}
	case "array":
		if arr, ok := val.([]any); ok && meta.Items != nil {
			for idx, elem := range arr {
				if err := validateValueDomain(elem, meta.Items, fmt.Sprintf("%s[%d]", path, idx)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func toFieldSpec(meta *PropertyMeta) renderer.FieldSpec {
	spec := renderer.FieldSpec{
		Type:   meta.DomainType,
		PII:    meta.PII,
		Secret: meta.Secret,
	}
	if meta.MaxItems != nil {
		spec.MaxItems = *meta.MaxItems
	}
	if len(meta.Properties) > 0 {
		spec.Properties = make(map[string]renderer.FieldSpec, len(meta.Properties))
		for k, sub := range meta.Properties {
			spec.Properties[k] = toFieldSpec(sub)
		}
	}
	if meta.Items != nil {
		itemSpec := toFieldSpec(meta.Items)
		spec.Items = &itemSpec
	}
	return spec
}

func readOptionalPositiveInt(node map[string]any, key, path string) (int, bool, error) {
	raw, exists := node[key]
	if !exists {
		return 0, false, nil
	}
	n, err := toInt(raw)
	if err != nil || n <= 0 {
		return 0, false, fmt.Errorf("%w: %s.%s must be a positive integer", ErrInvalidSchema, path, key)
	}
	return n, true, nil
}

func readOptionalNonNegInt(node map[string]any, key, path string) (int, bool, error) {
	raw, exists := node[key]
	if !exists {
		return 0, false, nil
	}
	n, err := toInt(raw)
	if err != nil || n < 0 {
		return 0, false, fmt.Errorf("%w: %s.%s must be a non-negative integer", ErrInvalidSchema, path, key)
	}
	return n, true, nil
}

func toInt(raw any) (int, error) {
	switch v := raw.(type) {
	case json.Number:
		i64, err := v.Int64()
		return int(i64), err
	case float64:
		if float64(int(v)) != v {
			return 0, errors.New("not an integer")
		}
		return int(v), nil
	case int:
		return v, nil
	default:
		return 0, errors.New("not a number")
	}
}

func normalizeJSONNumbers(v any) any {
	switch val := v.(type) {
	case json.Number:
		if i, err := val.Int64(); err == nil {
			return i
		}
		if f, err := val.Float64(); err == nil {
			return f
		}
		return val.String()
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, sub := range val {
			out[k] = normalizeJSONNumbers(sub)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, sub := range val {
			out[i] = normalizeJSONNumbers(sub)
		}
		return out
	default:
		return val
	}
}

func cloneMap(src map[string]any) map[string]any {
	if src == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		switch sub := v.(type) {
		case map[string]any:
			out[k] = cloneMap(sub)
		case []any:
			cp := make([]any, len(sub))
			copy(cp, sub)
			out[k] = cp
		default:
			out[k] = v
		}
	}
	return out
}
