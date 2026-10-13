package queryauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dangthobach/bpmp-platform/apps/go/projection-service/internal/application"
	"github.com/dangthobach/bpmp-platform/go/platform/requestmeta"
)

type PDP interface {
	Evaluate(context.Context, application.QueryAuthorizationRequest, string) (application.QueryAuthorization, error)
}
type PDPConfig struct {
	URL               string `json:"url"`
	WorkloadTokenPath string `json:"workload_token_path"`
	TimeoutMS         int64  `json:"timeout_ms"`
	MaxResponseBytes  int64  `json:"max_response_bytes"`
	AllowInsecure     bool   `json:"allow_insecure"`
}
type HTTPPDP struct {
	config PDPConfig
	client *http.Client
	token  func() (string, error)
}

func NewHTTPPDP(config PDPConfig, token func() (string, error)) (*HTTPPDP, error) {
	endpoint, err := url.Parse(config.URL)
	if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Scheme != "https" && !(config.AllowInsecure && endpoint.Scheme == "http")) || config.TimeoutMS <= 0 || config.TimeoutMS > int64(time.Hour/time.Millisecond) || config.MaxResponseBytes <= 0 || config.MaxResponseBytes > 16<<20 || token == nil {
		return nil, errors.New("invalid query PDP transport configuration")
	}
	return &HTTPPDP{config: config, token: token, client: &http.Client{Timeout: time.Duration(config.TimeoutMS) * time.Millisecond, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("PDP redirects forbidden") }}}, nil
}

type decision struct {
	SchemaVersion  uint32     `json:"schema_version"`
	TenantID       string     `json:"tenant_id"`
	ActorID        string     `json:"actor_id"`
	Decision       string     `json:"decision"`
	PolicyVersion  string     `json:"policy_version"`
	RevokeEpoch    uint64     `json:"revoke_epoch"`
	RevokeSequence uint64     `json:"revoke_sequence"`
	AllRows        bool       `json:"all_rows"`
	RowFilter      *condition `json:"row_filter"`
	AllowedFields  []string   `json:"allowed_fields"`
	MaskedFields   []string   `json:"masked_fields"`
}
type condition struct {
	Operator   string      `json:"operator"`
	Conditions []condition `json:"conditions"`
	Left       source      `json:"left"`
	Op         string      `json:"op"`
	Right      source      `json:"right"`
}
type source struct {
	Type  string          `json:"type"`
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

func (p *HTTPPDP) Evaluate(ctx context.Context, request application.QueryAuthorizationRequest, subject string) (application.QueryAuthorization, error) {
	deny := application.ErrQueryForbidden
	if request.Policy.QueryPDPResourceType == "" || request.Policy.QueryPDPAction == "" {
		return application.QueryAuthorization{}, deny
	}
	body, err := json.Marshal(struct {
		TenantID     string `json:"tenant_id"`
		ActorJWT     string `json:"actor_jwt"`
		ResourceType string `json:"resource_type"`
		Action       string `json:"action"`
	}{request.TenantID, string(request.Credential.SignedProof), request.Policy.QueryPDPResourceType, request.Policy.QueryPDPAction})
	if err != nil {
		return application.QueryAuthorization{}, deny
	}
	token, err := p.token()
	if err != nil || strings.TrimSpace(token) == "" {
		return application.QueryAuthorization{}, deny
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.config.URL, bytes.NewReader(body))
	if err != nil {
		return application.QueryAuthorization{}, deny
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	requestmeta.InjectHTTP(ctx, req.Header)
	response, err := p.client.Do(req)
	if err != nil {
		return application.QueryAuthorization{}, deny
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return application.QueryAuthorization{}, deny
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, p.config.MaxResponseBytes+1))
	if err != nil || int64(len(raw)) > p.config.MaxResponseBytes {
		return application.QueryAuthorization{}, deny
	}
	var value decision
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF || value.SchemaVersion != 1 || value.Decision != "ALLOW" || value.TenantID != request.TenantID || value.ActorID != subject || value.PolicyVersion == "" || value.RevokeSequence == 0 || value.AllowedFields == nil || value.MaskedFields == nil || value.AllRows == (value.RowFilter != nil) {
		return application.QueryAuthorization{}, deny
	}
	filter, err := lower(value.RowFilter, 0)
	if err != nil {
		return application.QueryAuthorization{}, deny
	}
	fields, err := fieldPolicy(value.AllowedFields, value.MaskedFields)
	if err != nil {
		return application.QueryAuthorization{}, deny
	}
	// Cursor identity includes durable revoke head even if row constraints do not change.
	version := fmt.Sprintf("%s/%s/%d/%d", request.Policy.PolicyVersion, value.PolicyVersion, value.RevokeSequence, value.RevokeEpoch)
	return application.QueryAuthorization{SubjectID: subject, PolicyVersion: version, Filter: filter, Fields: fields}, nil
}
func lower(node *condition, depth int) (*application.ProcessExpression, error) {
	if node == nil {
		return nil, nil
	}
	if depth > 32 {
		return nil, application.ErrQueryForbidden
	}
	result := &application.ProcessExpression{}
	switch node.Operator {
	case "AND", "OR":
		if len(node.Conditions) == 0 || len(node.Conditions) > 256 {
			return nil, application.ErrQueryForbidden
		}
		result.Logical = application.LogicalAnd
		if node.Operator == "OR" {
			result.Logical = application.LogicalOr
		}
		for i := range node.Conditions {
			child, err := lower(&node.Conditions[i], depth+1)
			if err != nil {
				return nil, err
			}
			result.Children = append(result.Children, *child)
		}
	case "LEAF":
		if node.Left.Type != "resource_field" || node.Right.Type != "literal" {
			return nil, application.ErrQueryForbidden
		}
		var field application.ProcessField
		for f := application.ProcessInstanceID; f <= application.ProcessInitiatorActorID; f++ {
			if application.ProcessFieldName(f) == node.Left.Key {
				field = f
				break
			}
		}
		if field == 0 {
			return nil, application.ErrQueryForbidden
		}
		operators := map[string]application.Comparison{"eq": application.CompareEqual, "neq": application.CompareNotEqual, "in": application.CompareIn, "not_in": application.CompareNotIn, "gte": application.CompareGreaterThanOrEqual, "lte": application.CompareLessThanOrEqual}
		op, ok := operators[node.Op]
		if !ok {
			return nil, application.ErrQueryForbidden
		}
		raw := []json.RawMessage{node.Right.Value}
		if op == application.CompareIn || op == application.CompareNotIn {
			if json.Unmarshal(node.Right.Value, &raw) != nil || len(raw) == 0 || len(raw) > 1000 {
				return nil, application.ErrQueryForbidden
			}
		}
		values := make([]application.QueryValue, 0, len(raw))
		for _, r := range raw {
			if len(r) == 0 || bytes.Equal(bytes.TrimSpace(r), []byte("null")) {
				return nil, application.ErrQueryForbidden
			}
			var str string
			var integer int64
			if json.Unmarshal(r, &str) == nil {
				if field == application.ProcessStartedAt || field == application.ProcessUpdatedAt || field == application.ProcessCompletedAt {
					timestamp, err := time.Parse(time.RFC3339Nano, str)
					if err != nil {
						return nil, err
					}
					values = append(values, application.QueryValue{Kind: application.ValueTimestamp, Time: timestamp})
				} else {
					values = append(values, application.QueryValue{Kind: application.ValueString, String: str})
				}
			} else if json.Unmarshal(r, &integer) == nil {
				values = append(values, application.QueryValue{Kind: application.ValueInteger, Integer: integer})
			} else {
				return nil, application.ErrQueryForbidden
			}
		}
		result.Predicate = &application.ProcessPredicate{Field: field, Operator: op, Values: values}
	default:
		return nil, application.ErrQueryForbidden
	}
	return result, nil
}
func fieldPolicy(allowed, masked []string) (*application.FieldAuthorization, error) {
	vocabulary := make(map[string]bool, len(application.FieldNames))
	for _, f := range application.FieldNames {
		vocabulary[f] = true
	}
	read := make(map[string]bool, len(vocabulary))
	if len(allowed) == 0 {
		for f := range vocabulary {
			read[f] = true
		}
	}
	for _, f := range allowed {
		if !vocabulary[f] || read[f] {
			return nil, application.ErrQueryForbidden
		}
		read[f] = true
	}
	for _, f := range masked {
		if !vocabulary[f] {
			return nil, application.ErrQueryForbidden
		}
		delete(read, f)
	}
	fields := &application.FieldAuthorization{ReadFields: []string{}, QueryFields: []string{}}
	for _, f := range application.FieldNames {
		if read[f] {
			fields.ReadFields = append(fields.ReadFields, f)
			fields.QueryFields = append(fields.QueryFields, f)
		}
	}
	return fields, nil
}
