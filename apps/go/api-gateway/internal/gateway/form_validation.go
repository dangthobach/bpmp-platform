package gateway

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
)

// validFormOutputs applies the same deliberately small schema profile as Authoring.
// The Gateway performs this check at the public command boundary; Authoring owns the schema.
func validFormOutputs(schemaJSON string, outputs map[string]json.RawMessage) bool {
	var schema struct {
		Type                 string                     `json:"type"`
		Properties           map[string]json.RawMessage `json:"properties"`
		Required             []string                   `json:"required"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
	}
	if json.Unmarshal([]byte(schemaJSON), &schema) != nil || schema.Type != "object" || len(schema.Properties) == 0 || schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		return false
	}
	if len(outputs) > len(schema.Properties) {
		return false
	}
	for name := range outputs {
		property, ok := schema.Properties[name]
		if !ok || !validFormFieldValue(property, outputs[name]) {
			return false
		}
	}
	for _, name := range schema.Required {
		if _, exists := outputs[name]; !exists {
			return false
		}
	}
	return true
}

func validFormFieldValue(propertyRaw, valueRaw json.RawMessage) bool {
	var property struct {
		Type     string          `json:"type"`
		Items    json.RawMessage `json:"items"`
		Enum     []any           `json:"enum"`
		Min      *float64        `json:"minimum"`
		Max      *float64        `json:"maximum"`
		MinLen   *int            `json:"minLength"`
		MaxLen   *int            `json:"maxLength"`
		MinItems *int            `json:"minItems"`
		MaxItems *int            `json:"maxItems"`
	}
	if json.Unmarshal(propertyRaw, &property) != nil {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(valueRaw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return false
	}
	switch property.Type {
	case "string":
		text, ok := value.(string)
		if !ok || (property.MinLen != nil && len([]rune(text)) < *property.MinLen) || (property.MaxLen != nil && len([]rune(text)) > *property.MaxLen) {
			return false
		}
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return false
		}
		integer, err := strconv.ParseInt(string(number), 10, 64)
		if err != nil {
			return false
		}
		if property.Min != nil && float64(integer) < *property.Min || property.Max != nil && float64(integer) > *property.Max || math.IsInf(float64(integer), 0) {
			return false
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return false
		}
	case "array":
		items, ok := value.([]any)
		if !ok || (property.MinItems != nil && len(items) < *property.MinItems) || (property.MaxItems != nil && len(items) > *property.MaxItems) {
			return false
		}
		var itemSchema struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(property.Items, &itemSchema) != nil {
			return false
		}
		for _, item := range items {
			if !matchesFormType(itemSchema.Type, item) {
				return false
			}
		}
	default:
		return false
	}
	if len(property.Enum) > 0 {
		matched := false
		for _, allowed := range property.Enum {
			if sameJSONValue(allowed, value) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func matchesFormType(kind string, value any) bool {
	switch kind {
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return false
		}
		_, err := strconv.ParseInt(string(number), 10, 64)
		return err == nil
	default:
		return false
	}
}

func sameJSONValue(left, right any) bool {
	a, errA := json.Marshal(left)
	b, errB := json.Marshal(right)
	return errA == nil && errB == nil && bytes.Equal(a, b)
}
