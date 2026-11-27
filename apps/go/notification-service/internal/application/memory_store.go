package application

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type MemoryStore struct {
	mu                 sync.RWMutex
	receipts           map[string]CommandReceipt
	audits             []AuditRecord
	outbox             []OutboxMessage
	inbox              map[string]string
	categories         map[string]Category
	applications       map[string]Application
	layouts            map[string]LayoutVersion
	templates          map[string]Template
	templateVersions   map[string]TemplateVersion
	templateReleases   map[string][]TemplateRelease
	providers          map[string]ProviderProfile
	routingPolicies    map[string]RoutingPolicy
	breakerOverrides   map[string][]BreakerOverrideRecord
	breakerTransitions map[string][]BreakerTransition
	triggerRules       map[string]TriggerRule
	triggerRevisions   map[string][]TriggerRuleRevision
	contacts           map[string]SubjectContact
	preferences        map[string]Preference
	suppressions       map[string]Suppression
	inappMessages      map[string]InAppMessage
	requests           map[string]NotificationRequestRecord
	requestsByIdemp    map[string]string
	deliveries         map[string]Delivery
	deliveriesByReq    map[string][]string
	payloadsByDelivery map[string]DeliveryPayload
	attemptsByDelivery map[string][]DeliveryAttempt
	eventsByDelivery   map[string][]DeliveryEvent
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		receipts:           make(map[string]CommandReceipt),
		inbox:              make(map[string]string),
		categories:         make(map[string]Category),
		applications:       make(map[string]Application),
		layouts:            make(map[string]LayoutVersion),
		templates:          make(map[string]Template),
		templateVersions:   make(map[string]TemplateVersion),
		templateReleases:   make(map[string][]TemplateRelease),
		providers:          make(map[string]ProviderProfile),
		routingPolicies:    make(map[string]RoutingPolicy),
		breakerOverrides:   make(map[string][]BreakerOverrideRecord),
		breakerTransitions: make(map[string][]BreakerTransition),
		triggerRules:       make(map[string]TriggerRule),
		triggerRevisions:   make(map[string][]TriggerRuleRevision),
		contacts:           make(map[string]SubjectContact),
		preferences:        make(map[string]Preference),
		suppressions:       make(map[string]Suppression),
		inappMessages:      make(map[string]InAppMessage),
		requests:           make(map[string]NotificationRequestRecord),
		requestsByIdemp:    make(map[string]string),
		deliveries:         make(map[string]Delivery),
		deliveriesByReq:    make(map[string][]string),
		payloadsByDelivery: make(map[string]DeliveryPayload),
		attemptsByDelivery: make(map[string][]DeliveryAttempt),
		eventsByDelivery:   make(map[string][]DeliveryEvent),
	}
}

func tkey(tenantID, id string) string { return tenantID + ":" + id }

func isImmutableStatus(st string) bool {
	return st == "PUBLISHED" || st == "DEPRECATED" || st == "RETIRED"
}

func (m *MemoryStore) GetCommandReceipt(_ context.Context, tenantID, commandID string) (*CommandReceipt, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rec, ok := m.receipts[tkey(tenantID, commandID)]
	if !ok {
		return nil, false, nil
	}
	cp := rec
	cp.ResultJSON = append([]byte(nil), rec.ResultJSON...)
	return &cp, true, nil
}

func (m *MemoryStore) SaveCommandReceipt(_ context.Context, rec CommandReceipt) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := tkey(rec.TenantID, rec.CommandID)
	if existing, ok := m.receipts[key]; ok {
		if existing.IntentDigest != rec.IntentDigest {
			return ErrConflict
		}
		return nil
	}
	cp := rec
	cp.ResultJSON = append([]byte(nil), rec.ResultJSON...)
	m.receipts[key] = cp
	return nil
}

func (m *MemoryStore) AppendAudit(_ context.Context, rec AuditRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audits = append(m.audits, rec)
	return nil
}

func (m *MemoryStore) ListAudit(ctx context.Context, tenantID string, limit int) ([]AuditRecord, error) {
	return m.ListAuditFiltered(ctx, AuditFilter{TenantID: tenantID, Limit: limit})
}

func (m *MemoryStore) ListAuditFiltered(_ context.Context, filter AuditFilter) ([]AuditRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	q := strings.ToLower(strings.TrimSpace(filter.Query))
	var out []AuditRecord
	for i := len(m.audits) - 1; i >= 0; i-- {
		a := m.audits[i]
		if a.TenantID != filter.TenantID {
			continue
		}
		if filter.ResourceType != "" && !strings.EqualFold(a.ResourceType, filter.ResourceType) {
			continue
		}
		if filter.Severity != "" && !strings.EqualFold(a.Severity, filter.Severity) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(a.Action), q) && !strings.Contains(strings.ToLower(a.ResourceRef), q) {
			continue
		}
		out = append(out, a)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (m *MemoryStore) AppendOutbox(_ context.Context, msg OutboxMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := msg
	cp.PayloadJSON = append([]byte(nil), msg.PayloadJSON...)
	m.outbox = append(m.outbox, cp)
	return nil
}

func (m *MemoryStore) ClaimPendingOutbox(_ context.Context, limit int) ([]OutboxMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 {
		limit = 100
	}
	var out []OutboxMessage
	for _, item := range m.outbox {
		if item.PublishedAt.IsZero() {
			cp := item
			cp.PayloadJSON = append([]byte(nil), item.PayloadJSON...)
			out = append(out, cp)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (m *MemoryStore) MarkOutboxPublished(_ context.Context, tenantID, id string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for idx := range m.outbox {
		if m.outbox[idx].TenantID == tenantID && m.outbox[idx].ID == id {
			m.outbox[idx].PublishedAt = now.UTC()
			return nil
		}
	}
	return ErrNotFound
}

func (m *MemoryStore) RecordInboxEvent(_ context.Context, tenantID, source, eventID, digest string, _ time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := tenantID + ":" + source + ":" + eventID
	if prev, ok := m.inbox[k]; ok {
		if prev != digest {
			return false, ErrConflict
		}
		return true, nil
	}
	m.inbox[k] = digest
	return false, nil
}

func (m *MemoryStore) SaveCategory(_ context.Context, cat Category) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.categories {
		if existing.TenantID == cat.TenantID && existing.CategoryKey == cat.CategoryKey && existing.ID != cat.ID {
			return ErrConflict
		}
	}
	if cat.RowVersion == 0 {
		cat.RowVersion = 1
	}
	m.categories[tkey(cat.TenantID, cat.ID)] = cat
	return nil
}

func (m *MemoryStore) GetCategory(_ context.Context, tenantID, id string) (*Category, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.categories[tkey(tenantID, id)]
	if !ok {
		return nil, ErrNotFound
	}
	cp := c
	return &cp, nil
}

func (m *MemoryStore) GetCategoryByKey(_ context.Context, tenantID, key string) (*Category, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, c := range m.categories {
		if c.TenantID == tenantID && c.CategoryKey == key {
			cp := c
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

func (m *MemoryStore) ListCategories(_ context.Context, tenantID string) ([]Category, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Category
	for _, c := range m.categories {
		if c.TenantID == tenantID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CategoryKey < out[j].CategoryKey })
	return out, nil
}

func (m *MemoryStore) SaveApplication(_ context.Context, app Application) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.applications {
		if existing.TenantID == app.TenantID && existing.AppKey == app.AppKey && existing.ID != app.ID {
			return ErrConflict
		}
	}
	if app.RowVersion == 0 {
		app.RowVersion = 1
	}
	cp := app
	cp.AllowedNamespaces = append([]string(nil), app.AllowedNamespaces...)
	cp.AllowedChannels = append([]string(nil), app.AllowedChannels...)
	m.applications[tkey(app.TenantID, app.ID)] = cp
	return nil
}

func (m *MemoryStore) GetApplication(_ context.Context, tenantID, id string) (*Application, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.applications[tkey(tenantID, id)]
	if !ok {
		return nil, ErrNotFound
	}
	cp := a
	cp.AllowedNamespaces = append([]string(nil), a.AllowedNamespaces...)
	cp.AllowedChannels = append([]string(nil), a.AllowedChannels...)
	return &cp, nil
}

func (m *MemoryStore) GetApplicationByKey(_ context.Context, tenantID, appKey string) (*Application, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, a := range m.applications {
		if a.TenantID == tenantID && a.AppKey == appKey {
			cp := a
			cp.AllowedNamespaces = append([]string(nil), a.AllowedNamespaces...)
			cp.AllowedChannels = append([]string(nil), a.AllowedChannels...)
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

func (m *MemoryStore) ListApplications(_ context.Context, tenantID string) ([]Application, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Application
	for _, a := range m.applications {
		if a.TenantID == tenantID {
			cp := a
			cp.AllowedNamespaces = append([]string(nil), a.AllowedNamespaces...)
			cp.AllowedChannels = append([]string(nil), a.AllowedChannels...)
			out = append(out, cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AppKey < out[j].AppKey })
	return out, nil
}

func (m *MemoryStore) CountApplicationRequestsSince(_ context.Context, tenantID, appID string, since time.Time) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, req := range m.requests {
		if req.TenantID == tenantID && req.ApplicationID == appID && !req.IsTest && !req.CreatedAt.Before(since) {
			count++
		}
	}
	return count, nil
}

func (m *MemoryStore) SaveLayoutVersion(_ context.Context, lv LayoutVersion) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := tkey(lv.TenantID, lv.ID)
	if existing, ok := m.layouts[k]; ok && isImmutableStatus(existing.Status) {
		if existing.ContentHash != lv.ContentHash || existing.HTMLTemplate != lv.HTMLTemplate || !bytes.Equal(existing.VariableSchema, lv.VariableSchema) {
			return ErrImmutable
		}
	}
	cp := lv
	cp.VariableSchema = append([]byte(nil), lv.VariableSchema...)
	m.layouts[k] = cp
	return nil
}

func (m *MemoryStore) GetLayoutVersion(_ context.Context, tenantID, id string) (*LayoutVersion, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	lv, ok := m.layouts[tkey(tenantID, id)]
	if !ok {
		return nil, ErrNotFound
	}
	cp := lv
	cp.VariableSchema = append([]byte(nil), lv.VariableSchema...)
	return &cp, nil
}

func cloneTemplate(t Template) Template {
	cp := t
	cp.Tags = append([]string(nil), t.Tags...)
	cp.Channels = append([]string(nil), t.Channels...)
	return cp
}

func (m *MemoryStore) CreateTemplate(_ context.Context, tmpl Template) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.templates {
		if existing.TenantID == tmpl.TenantID && existing.TemplateKey == tmpl.TemplateKey && !existing.IsDeleted {
			return ErrConflict
		}
	}
	if tmpl.RowVersion == 0 {
		tmpl.RowVersion = 1
	}
	m.templates[tkey(tmpl.TenantID, tmpl.ID)] = cloneTemplate(tmpl)
	return nil
}

func (m *MemoryStore) UpdateTemplate(_ context.Context, tmpl Template) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := tkey(tmpl.TenantID, tmpl.ID)
	existing, ok := m.templates[k]
	if !ok {
		return ErrNotFound
	}
	tmpl.RowVersion = existing.RowVersion + 1
	m.templates[k] = cloneTemplate(tmpl)
	return nil
}

func (m *MemoryStore) GetTemplate(_ context.Context, tenantID, id string) (*Template, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.templates[tkey(tenantID, id)]
	if !ok || t.IsDeleted {
		return nil, ErrNotFound
	}
	cp := cloneTemplate(t)
	return &cp, nil
}

func (m *MemoryStore) GetTemplateIncludingDeleted(_ context.Context, tenantID, id string) (*Template, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.templates[tkey(tenantID, id)]
	if !ok {
		return nil, ErrNotFound
	}
	cp := cloneTemplate(t)
	return &cp, nil
}

func (m *MemoryStore) GetTemplateByKey(_ context.Context, tenantID, key string) (*Template, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, t := range m.templates {
		if t.TenantID == tenantID && t.TemplateKey == key && !t.IsDeleted {
			cp := cloneTemplate(t)
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

func (m *MemoryStore) ListTemplates(_ context.Context, tenantID, appID string, pageSize int, pageToken string) ([]Template, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var all []Template
	for _, t := range m.templates {
		if t.TenantID != tenantID || t.IsDeleted {
			continue
		}
		if appID != "" && t.ApplicationID != appID {
			continue
		}
		all = append(all, cloneTemplate(t))
	}
	sort.Slice(all, func(i, j int) bool { return all[i].TemplateKey < all[j].TemplateKey })
	if pageSize <= 0 {
		pageSize = 50
	}
	offset := 0
	if pageToken != "" {
		if parsed, err := strconv.Atoi(pageToken); err == nil && parsed >= 0 {
			offset = parsed
		}
	}
	if offset >= len(all) {
		return nil, "", nil
	}
	end := offset + pageSize
	next := ""
	if end < len(all) {
		next = strconv.Itoa(end)
	} else {
		end = len(all)
	}
	return all[offset:end], next, nil
}

func (m *MemoryStore) ListTemplateTags(_ context.Context, tenantID string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	seen := make(map[string]struct{})
	for _, t := range m.templates {
		if t.TenantID != tenantID || t.IsDeleted {
			continue
		}
		for _, tag := range t.Tags {
			if strings.TrimSpace(tag) != "" {
				seen[tag] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for tag := range seen {
		out = append(out, tag)
	}
	sort.Strings(out)
	return out, nil
}

func (m *MemoryStore) CountTemplateDeliveriesSince(_ context.Context, tenantID, templateID string, since time.Time) (uint64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var cnt uint64
	for _, d := range m.deliveries {
		if d.TenantID == tenantID && d.TemplateID == templateID && !d.CreatedAt.Before(since) {
			cnt++
		}
	}
	return cnt, nil
}

func cloneTemplateVersion(tv TemplateVersion) TemplateVersion {
	cp := tv
	cp.VariableSchemaJSON = append([]byte(nil), tv.VariableSchemaJSON...)
	cp.Variants = make([]TemplateVariant, len(tv.Variants))
	for i, v := range tv.Variants {
		vcp := v
		vcp.ParamMappingJSON = append([]byte(nil), v.ParamMappingJSON...)
		cp.Variants[i] = vcp
	}
	return cp
}

func (m *MemoryStore) SaveTemplateVersion(_ context.Context, tv TemplateVersion, expectedRowVersion uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := tkey(tv.TenantID, tv.ID)
	if existing, exists := m.templateVersions[k]; exists {
		if isImmutableStatus(existing.Status) {
			// Mirror DB trigger: published content, schema, and variants are immutable!
			if !isImmutableStatus(tv.Status) || existing.ContentHash != tv.ContentHash ||
				!bytes.Equal(existing.VariableSchemaJSON, tv.VariableSchemaJSON) ||
				len(existing.Variants) != len(tv.Variants) {
				return ErrImmutable
			}
			for i := range existing.Variants {
				if existing.Variants[i].VariantHash != tv.Variants[i].VariantHash {
					return ErrImmutable
				}
			}
		}
		if expectedRowVersion > 0 && existing.RowVersion != expectedRowVersion {
			return ErrConflict
		}
		tv.RowVersion = existing.RowVersion + 1
	} else {
		if tv.RowVersion == 0 {
			tv.RowVersion = 1
		}
	}
	m.templateVersions[k] = cloneTemplateVersion(tv)
	return nil
}

func (m *MemoryStore) GetTemplateVersion(_ context.Context, tenantID, versionID string) (*TemplateVersion, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tv, ok := m.templateVersions[tkey(tenantID, versionID)]
	if !ok {
		return nil, ErrNotFound
	}
	cp := cloneTemplateVersion(tv)
	return &cp, nil
}

func (m *MemoryStore) ListTemplateVersions(_ context.Context, tenantID, templateID string) ([]TemplateVersion, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []TemplateVersion
	for _, tv := range m.templateVersions {
		if tv.TenantID == tenantID && tv.TemplateID == templateID {
			out = append(out, cloneTemplateVersion(tv))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

func (m *MemoryStore) SaveTemplateRelease(_ context.Context, rel TemplateRelease) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := tkey(rel.TenantID, rel.TemplateID)
	list := m.templateReleases[k]
	if rel.Status == "ACTIVE" {
		for idx := range list {
			if list[idx].SchemaMajor == rel.SchemaMajor && list[idx].Status == "ACTIVE" {
				list[idx].Status = "SUPERSEDED"
				list[idx].UpdatedAt = rel.UpdatedAt
			}
		}
	}
	if rel.RowVersion == 0 {
		rel.RowVersion = 1
	}
	list = append(list, rel)
	m.templateReleases[k] = list
	return nil
}

func (m *MemoryStore) GetActiveTemplateRelease(_ context.Context, tenantID, templateID string, schemaMajor uint32, now time.Time) (*TemplateRelease, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	list := m.templateReleases[tkey(tenantID, templateID)]
	var best *TemplateRelease
	for i := range list {
		r := list[i]
		if r.SchemaMajor != schemaMajor {
			continue
		}
		if r.Status == "ACTIVE" && !r.ActivateAt.After(now) {
			if best == nil || r.Ordinal > best.Ordinal {
				cp := r
				best = &cp
			}
		}
	}
	if best == nil {
		return nil, ErrNotFound
	}
	return best, nil
}

func (m *MemoryStore) ListTemplateReleases(_ context.Context, tenantID, templateID string) ([]TemplateRelease, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	list := m.templateReleases[tkey(tenantID, templateID)]
	out := append([]TemplateRelease(nil), list...)
	sort.Slice(out, func(i, j int) bool { return out[i].Ordinal > out[j].Ordinal })
	return out, nil
}

func (m *MemoryStore) ActivateDueScheduledReleases(_ context.Context, now time.Time) ([]TemplateRelease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var activated []TemplateRelease
	for k, list := range m.templateReleases {
		for idx := range list {
			if list[idx].Status == "SCHEDULED" && !list[idx].ActivateAt.After(now) {
				major := list[idx].SchemaMajor
				for j := range list {
					if j != idx && list[j].SchemaMajor == major && list[j].Status == "ACTIVE" {
						list[j].Status = "SUPERSEDED"
						list[j].UpdatedAt = now
					}
				}
				list[idx].Status = "ACTIVE"
				list[idx].ActivatedAt = now
				list[idx].UpdatedAt = now
				activated = append(activated, list[idx])
			}
		}
		m.templateReleases[k] = list
	}
	return activated, nil
}

func (m *MemoryStore) SaveProviderProfile(_ context.Context, p ProviderProfile) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := p
	if p.ActiveRevision != nil {
		rev := *p.ActiveRevision
		rev.ConfigJSON = append([]byte(nil), p.ActiveRevision.ConfigJSON...)
		cp.ActiveRevision = &rev
	}
	if cp.RowVersion == 0 {
		cp.RowVersion = 1
	}
	m.providers[tkey(p.TenantID, p.ID)] = cp
	return nil
}

func (m *MemoryStore) GetProviderProfile(_ context.Context, tenantID, id string) (*ProviderProfile, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.providers[tkey(tenantID, id)]
	if !ok {
		return nil, ErrNotFound
	}
	cp := p
	if p.ActiveRevision != nil {
		rev := *p.ActiveRevision
		rev.ConfigJSON = append([]byte(nil), p.ActiveRevision.ConfigJSON...)
		cp.ActiveRevision = &rev
	}
	return &cp, nil
}

func (m *MemoryStore) ListProviderProfiles(_ context.Context, tenantID string) ([]ProviderProfile, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []ProviderProfile
	for _, p := range m.providers {
		if p.TenantID == tenantID {
			cp := p
			if p.ActiveRevision != nil {
				rev := *p.ActiveRevision
				rev.ConfigJSON = append([]byte(nil), p.ActiveRevision.ConfigJSON...)
				cp.ActiveRevision = &rev
			}
			out = append(out, cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ProviderKey < out[j].ProviderKey })
	return out, nil
}

func (m *MemoryStore) SaveRoutingPolicy(_ context.Context, rp RoutingPolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, _ := json.Marshal(rp)
	var cp RoutingPolicy
	_ = json.Unmarshal(raw, &cp)
	m.routingPolicies[tkey(rp.TenantID, rp.ID)] = cp
	return nil
}

func (m *MemoryStore) ListRoutingPolicies(_ context.Context, tenantID, channel string) ([]RoutingPolicy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []RoutingPolicy
	for _, rp := range m.routingPolicies {
		if rp.TenantID == tenantID && (channel == "" || rp.Channel == channel) {
			out = append(out, rp)
		}
	}
	return out, nil
}

func (m *MemoryStore) SaveBreakerOverride(_ context.Context, ov BreakerOverrideRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := tkey(ov.TenantID, ov.ProviderID)
	list := m.breakerOverrides[k]
	for idx := range list {
		if list[idx].RevokedAt.IsZero() {
			list[idx].RevokedAt = ov.CreatedAt
			list[idx].Status = "SUPERSEDED"
		}
	}
	list = append(list, ov)
	m.breakerOverrides[k] = list
	return nil
}

func (m *MemoryStore) GetActiveBreakerOverride(_ context.Context, tenantID, providerID string, now time.Time) (*BreakerOverrideRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	list := m.breakerOverrides[tkey(tenantID, providerID)]
	for i := len(list) - 1; i >= 0; i-- {
		ov := list[i]
		if ov.RevokedAt.IsZero() && now.Before(ov.ExpiresAt) {
			cp := ov
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *MemoryStore) SaveSubjectContact(_ context.Context, c SubjectContact) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := c
	cp.EncryptedAddress = append([]byte(nil), c.EncryptedAddress...)
	m.contacts[c.TenantID+":"+c.SubjectRef+":"+c.Channel] = cp
	return nil
}

func (m *MemoryStore) GetSubjectContact(_ context.Context, tenantID, subjectRef, channel string) (*SubjectContact, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.contacts[tenantID+":"+subjectRef+":"+channel]
	if !ok {
		return nil, ErrNotFound
	}
	cp := c
	cp.EncryptedAddress = append([]byte(nil), c.EncryptedAddress...)
	return &cp, nil
}

func (m *MemoryStore) ListPreferences(_ context.Context, tenantID, subjectRef string) ([]Preference, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Preference
	for _, p := range m.preferences {
		if p.TenantID == tenantID && p.SubjectRef == subjectRef {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CategoryKey != out[j].CategoryKey {
			return out[i].CategoryKey < out[j].CategoryKey
		}
		return out[i].Channel < out[j].Channel
	})
	return out, nil
}

func (m *MemoryStore) UpsertPreference(_ context.Context, p Preference) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := p.TenantID + ":" + p.SubjectRef + ":" + p.CategoryID + ":" + p.Channel
	m.preferences[k] = p
	return nil
}

func (m *MemoryStore) IsSuppressed(_ context.Context, tenantID, channel, addressHash string, now time.Time) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.suppressions[tenantID+":"+channel+":"+addressHash]
	if !ok || s.Status != "ACTIVE" {
		return false, nil
	}
	if !s.ExpiresAt.IsZero() && !now.Before(s.ExpiresAt) {
		return false, nil
	}
	return true, nil
}

func (m *MemoryStore) SaveSuppression(_ context.Context, s Suppression) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.suppressions[s.TenantID+":"+s.Channel+":"+s.AddressHash] = s
	return nil
}

func (m *MemoryStore) SaveInAppMessage(_ context.Context, msg InAppMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inappMessages[tkey(msg.TenantID, msg.ID)] = msg
	return nil
}

func (m *MemoryStore) ListInAppMessages(_ context.Context, tenantID, subjectRef string, unreadOnly bool, pageSize int, pageToken string) ([]InAppMessage, string, uint32, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var matching []InAppMessage
	var unread uint32
	for _, msg := range m.inappMessages {
		if msg.TenantID != tenantID || msg.SubjectRef != subjectRef || !msg.ArchivedAt.IsZero() {
			continue
		}
		if msg.ReadAt.IsZero() {
			unread++
		}
		if unreadOnly && !msg.ReadAt.IsZero() {
			continue
		}
		matching = append(matching, msg)
	}
	sort.Slice(matching, func(i, j int) bool {
		if !matching[i].CreatedAt.Equal(matching[j].CreatedAt) {
			return matching[i].CreatedAt.After(matching[j].CreatedAt)
		}
		return matching[i].ID > matching[j].ID
	})
	if pageSize <= 0 {
		pageSize = 25
	}
	offset := 0
	if pageToken != "" {
		if parsed, err := strconv.Atoi(pageToken); err == nil && parsed >= 0 {
			offset = parsed
		}
	}
	if offset >= len(matching) {
		return nil, "", unread, nil
	}
	end := offset + pageSize
	next := ""
	if end < len(matching) {
		next = strconv.Itoa(end)
	} else {
		end = len(matching)
	}
	return matching[offset:end], next, unread, nil
}

func (m *MemoryStore) MarkInAppMessagesRead(_ context.Context, tenantID, subjectRef string, ids []string, now time.Time) (uint32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var updated uint32
	for _, id := range ids {
		k := tkey(tenantID, id)
		msg, ok := m.inappMessages[k]
		if !ok || msg.SubjectRef != subjectRef {
			continue
		}
		if msg.ReadAt.IsZero() {
			msg.ReadAt = now.UTC()
			m.inappMessages[k] = msg
			updated++
		}
	}
	return updated, nil
}

func (m *MemoryStore) FindRequestByIdempotency(_ context.Context, tenantID, appID, idempotencyKey string) (*NotificationRequestRecord, []Delivery, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	reqID, ok := m.requestsByIdemp[tenantID+":"+appID+":"+idempotencyKey]
	if !ok {
		return nil, nil, false, nil
	}
	req := m.requests[tkey(tenantID, reqID)]
	var dels []Delivery
	for _, dID := range m.deliveriesByReq[tkey(tenantID, reqID)] {
		dels = append(dels, m.deliveries[tkey(tenantID, dID)])
	}
	return &req, dels, true, nil
}

func (m *MemoryStore) CreateRequestWithDeliveries(_ context.Context, req NotificationRequestRecord, deliveries []Delivery, payloads []DeliveryPayload, outbox []OutboxMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if req.ID != "" && req.IdempotencyKey != "" {
		idempKey := req.TenantID + ":" + req.ApplicationID + ":" + req.IdempotencyKey
		if existingReqID, exists := m.requestsByIdemp[idempKey]; exists && existingReqID != req.ID {
			return ErrConflict
		}
		m.requests[tkey(req.TenantID, req.ID)] = req
		m.requestsByIdemp[idempKey] = req.ID
	}
	for _, d := range deliveries {
		m.deliveries[tkey(d.TenantID, d.ID)] = d
		if d.RequestID != "" {
			rk := tkey(d.TenantID, d.RequestID)
			m.deliveriesByReq[rk] = append(m.deliveriesByReq[rk], d.ID)
		}
	}
	for _, p := range payloads {
		cp := p
		cp.EncryptedAddress = append([]byte(nil), p.EncryptedAddress...)
		cp.EncryptedData = append([]byte(nil), p.EncryptedData...)
		cp.EncryptedSecrets = append([]byte(nil), p.EncryptedSecrets...)
		cp.RenderedCopyEncrypted = append([]byte(nil), p.RenderedCopyEncrypted...)
		m.payloadsByDelivery[tkey(p.TenantID, p.DeliveryID)] = cp
	}
	for _, o := range outbox {
		cp := o
		cp.PayloadJSON = append([]byte(nil), o.PayloadJSON...)
		m.outbox = append(m.outbox, cp)
	}
	return nil
}

func (m *MemoryStore) GetNotificationRequest(_ context.Context, tenantID, requestID string) (*NotificationRequestRecord, []Delivery, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	req, ok := m.requests[tkey(tenantID, requestID)]
	if !ok {
		return nil, nil, ErrNotFound
	}
	var dels []Delivery
	for _, dID := range m.deliveriesByReq[tkey(tenantID, requestID)] {
		dels = append(dels, m.deliveries[tkey(tenantID, dID)])
	}
	return &req, dels, nil
}

func (m *MemoryStore) GetDelivery(_ context.Context, tenantID, deliveryID string) (*Delivery, *DeliveryPayload, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	k := tkey(tenantID, deliveryID)
	d, ok := m.deliveries[k]
	if !ok {
		return nil, nil, ErrNotFound
	}
	dcp := d
	if p, pok := m.payloadsByDelivery[k]; pok {
		pcp := p
		pcp.EncryptedAddress = append([]byte(nil), p.EncryptedAddress...)
		pcp.EncryptedData = append([]byte(nil), p.EncryptedData...)
		pcp.EncryptedSecrets = append([]byte(nil), p.EncryptedSecrets...)
		pcp.RenderedCopyEncrypted = append([]byte(nil), p.RenderedCopyEncrypted...)
		return &dcp, &pcp, nil
	}
	return &dcp, nil, nil
}

func (m *MemoryStore) UpdateDeliveryOutcome(_ context.Context, d Delivery, eraseSecrets bool, erasedAt time.Time, attempts []DeliveryAttempt, outbox []OutboxMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := tkey(d.TenantID, d.ID)
	if _, ok := m.deliveries[k]; !ok {
		return ErrNotFound
	}
	m.deliveries[k] = d
	if eraseSecrets {
		if p, pok := m.payloadsByDelivery[k]; pok {
			p.EncryptedSecrets = nil
			p.SecretsErasedAt = erasedAt.UTC()
			m.payloadsByDelivery[k] = p
		}
	}
	if len(attempts) > 0 {
		m.attemptsByDelivery[k] = append(m.attemptsByDelivery[k], attempts...)
	}
	for _, o := range outbox {
		cp := o
		cp.PayloadJSON = append([]byte(nil), o.PayloadJSON...)
		m.outbox = append(m.outbox, cp)
	}
	return nil
}

func (m *MemoryStore) QueryDeliveries(_ context.Context, q DeliveryQuery) ([]Delivery, time.Time, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var items []Delivery
	for _, d := range m.deliveries {
		if d.TenantID != q.TenantID {
			continue
		}
		if q.ApplicationID != "" && d.ApplicationID != q.ApplicationID {
			continue
		}
		if q.TemplateID != "" && d.TemplateID != q.TemplateID {
			continue
		}
		if q.Channel != "" && d.Channel != q.Channel {
			continue
		}
		if q.Status != "" && d.Status != q.Status {
			continue
		}
		if q.CorrelationID != "" && d.CorrelationID != q.CorrelationID {
			continue
		}
		if q.RecipientSubjectRef != "" && d.RecipientSubjectRef != q.RecipientSubjectRef {
			continue
		}
		if !q.BeforeCreatedAt.IsZero() {
			if d.CreatedAt.After(q.BeforeCreatedAt) {
				continue
			}
			if d.CreatedAt.Equal(q.BeforeCreatedAt) && q.BeforeID != "" && d.ID >= q.BeforeID {
				continue
			}
		}
		items = append(items, d)
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CreatedAt.After(items[j].CreatedAt)
		}
		return items[i].ID > items[j].ID
	})
	limit := q.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if len(items) <= limit {
		return items, time.Time{}, "", nil
	}
	page := items[:limit]
	last := page[len(page)-1]
	return page, last.CreatedAt, last.ID, nil
}

func (m *MemoryStore) AppendBreakerTransition(_ context.Context, tr BreakerTransition) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := tkey(tr.TenantID, tr.ProviderID)
	m.breakerTransitions[k] = append(m.breakerTransitions[k], tr)
	return nil
}

func (m *MemoryStore) ListBreakerTransitions(_ context.Context, tenantID, providerID string, limit int) ([]BreakerTransition, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 {
		limit = 50
	}
	list := m.breakerTransitions[tkey(tenantID, providerID)]
	var out []BreakerTransition
	for i := len(list) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, list[i])
	}
	return out, nil
}

func (m *MemoryStore) CountProviderDeliveriesSince(_ context.Context, tenantID, providerID string, since time.Time) (uint64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var cnt uint64
	for _, d := range m.deliveries {
		if d.TenantID == tenantID && d.SelectedProviderID == providerID && !d.CreatedAt.Before(since) {
			cnt++
		}
	}
	return cnt, nil
}

func (m *MemoryStore) ListDeliveryAttempts(_ context.Context, tenantID, deliveryID string) ([]DeliveryAttempt, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	list := m.attemptsByDelivery[tkey(tenantID, deliveryID)]
	out := append([]DeliveryAttempt(nil), list...)
	return out, nil
}

func (m *MemoryStore) AppendDeliveryEvent(_ context.Context, ev DeliveryEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := tkey(ev.TenantID, ev.DeliveryID)
	m.eventsByDelivery[k] = append(m.eventsByDelivery[k], ev)
	return nil
}

func (m *MemoryStore) ListDeliveryEvents(_ context.Context, tenantID, deliveryID string) ([]DeliveryEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	list := m.eventsByDelivery[tkey(tenantID, deliveryID)]
	out := append([]DeliveryEvent(nil), list...)
	return out, nil
}

func (m *MemoryStore) SaveTriggerRule(_ context.Context, rule TriggerRule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ex := range m.triggerRules {
		if ex.TenantID == rule.TenantID && ex.RuleKey == rule.RuleKey && ex.ID != rule.ID {
			return ErrConflict
		}
	}
	m.triggerRules[tkey(rule.TenantID, rule.ID)] = rule
	return nil
}

func (m *MemoryStore) GetTriggerRule(_ context.Context, tenantID, id string) (*TriggerRule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.triggerRules[tkey(tenantID, id)]
	if !ok {
		return nil, ErrNotFound
	}
	cp := r
	return &cp, nil
}

func (m *MemoryStore) ListTriggerRules(_ context.Context, tenantID, workflowType string) ([]TriggerRule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []TriggerRule
	for _, r := range m.triggerRules {
		if r.TenantID != tenantID {
			continue
		}
		if workflowType != "" && r.WorkflowType != workflowType {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RuleKey < out[j].RuleKey })
	return out, nil
}

func (m *MemoryStore) SaveTriggerRuleRevision(_ context.Context, rev TriggerRuleRevision) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := tkey(rev.TenantID, rev.RuleID)
	list := m.triggerRevisions[k]
	updated := false
	for i := range list {
		if list[i].ID == rev.ID || list[i].Revision == rev.Revision {
			list[i] = rev
			updated = true
			break
		}
	}
	if !updated {
		list = append(list, rev)
	}
	m.triggerRevisions[k] = list
	return nil
}

func (m *MemoryStore) ListTriggerRuleRevisions(_ context.Context, tenantID, ruleID string) ([]TriggerRuleRevision, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	list := m.triggerRevisions[tkey(tenantID, ruleID)]
	out := append([]TriggerRuleRevision(nil), list...)
	sort.Slice(out, func(i, j int) bool { return out[i].Revision < out[j].Revision })
	return out, nil
}
