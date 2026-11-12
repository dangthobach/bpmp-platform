package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"

	enginev1 "github.com/dangthobach/bpmp-platform/go/contracts/gen/bpmp/engine/v1"
)

func mapInitialWorkflowVariables(input map[string]json.RawMessage) ([]*enginev1.WorkflowVariable, error) {
	if len(input) > 128 {
		return nil, errors.New("too many workflow variables")
	}
	names := make([]string, 0, len(input))
	for name := range input {
		if strings.TrimSpace(name) == "" || len(name) > 128 {
			return nil, errors.New("workflow variable name is invalid")
		}
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]*enginev1.WorkflowVariable, 0, len(names))
	for _, name := range names {
		decoder := json.NewDecoder(bytes.NewReader(input[name]))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			return nil, errors.New("workflow variable contains trailing JSON")
		}
		mapped, err := mapWorkflowVariableValue(name, value, 0)
		if err != nil {
			return nil, err
		}
		result = append(result, mapped)
	}
	return result, nil
}

func mapWorkflowVariableValue(name string, value any, depth int) (*enginev1.WorkflowVariable, error) {
	if depth > 16 {
		return nil, errors.New("workflow variable list nesting exceeds limit")
	}
	switch typed := value.(type) {
	case bool:
		return &enginev1.WorkflowVariable{Name: name, Value: &enginev1.WorkflowVariable_BooleanValue{BooleanValue: typed}}, nil
	case string:
		return &enginev1.WorkflowVariable{Name: name, Value: &enginev1.WorkflowVariable_StringValue{StringValue: typed}}, nil
	case json.Number:
		integer, err := strconv.ParseInt(string(typed), 10, 64)
		if err != nil {
			return nil, errors.New("workflow variables support integer numbers only")
		}
		return &enginev1.WorkflowVariable{Name: name, Value: &enginev1.WorkflowVariable_IntegerValue{IntegerValue: integer}}, nil
	case []any:
		if len(typed) > 256 {
			return nil, errors.New("workflow variable list exceeds item limit")
		}
		items := make([]*enginev1.WorkflowValueItem, 0, len(typed))
		for _, item := range typed {
			mapped, err := mapWorkflowVariableItem(item, depth+1)
			if err != nil {
				return nil, err
			}
			items = append(items, mapped)
		}
		return &enginev1.WorkflowVariable{Name: name, Value: &enginev1.WorkflowVariable_ListValue{ListValue: &enginev1.WorkflowValueList{Items: items}}}, nil
	default:
		return nil, errors.New("workflow variable type is unsupported")
	}
}

func mapWorkflowVariableItem(value any, depth int) (*enginev1.WorkflowValueItem, error) {
	if depth > 16 {
		return nil, errors.New("workflow variable list nesting exceeds limit")
	}
	switch typed := value.(type) {
	case bool:
		return &enginev1.WorkflowValueItem{Value: &enginev1.WorkflowValueItem_BooleanValue{BooleanValue: typed}}, nil
	case string:
		return &enginev1.WorkflowValueItem{Value: &enginev1.WorkflowValueItem_StringValue{StringValue: typed}}, nil
	case json.Number:
		integer, err := strconv.ParseInt(string(typed), 10, 64)
		if err != nil {
			return nil, errors.New("workflow variables support integer numbers only")
		}
		return &enginev1.WorkflowValueItem{Value: &enginev1.WorkflowValueItem_IntegerValue{IntegerValue: integer}}, nil
	case []any:
		if len(typed) > 256 {
			return nil, errors.New("workflow variable list exceeds item limit")
		}
		items := make([]*enginev1.WorkflowValueItem, 0, len(typed))
		for _, item := range typed {
			mapped, err := mapWorkflowVariableItem(item, depth+1)
			if err != nil {
				return nil, err
			}
			items = append(items, mapped)
		}
		return &enginev1.WorkflowValueItem{Value: &enginev1.WorkflowValueItem_ListValue{ListValue: &enginev1.WorkflowValueList{Items: items}}}, nil
	default:
		return nil, errors.New("workflow variable type is unsupported")
	}
}
