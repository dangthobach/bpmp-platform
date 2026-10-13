package application

// FieldNames is the versioned read-model vocabulary, not SQL column names.
var FieldNames = []string{"tenant_id", "instance_id", "workflow_type", "workflow_version", "status", "active_node_id", "last_event_sequence", "started_at_epoch_ms", "updated_at_epoch_ms", "completed_at_epoch_ms", "duration_ms", "event_count", "config_version", "policy_version", "is_deleted", "version", "initiator_actor_id", "event_type", "event_node_id", "event_actor_id", "event_sequence", "event_occurred_at", "event_config_version", "event_policy_version"}

func ProcessFieldName(field ProcessField) string {
	names := map[ProcessField]string{ProcessInstanceID: "instance_id", ProcessWorkflowType: "workflow_type", ProcessWorkflowVersion: "workflow_version", ProcessStatus: "status", ProcessActiveNodeID: "active_node_id", ProcessLastEventSequence: "last_event_sequence", ProcessStartedAt: "started_at_epoch_ms", ProcessUpdatedAt: "updated_at_epoch_ms", ProcessCompletedAt: "completed_at_epoch_ms", ProcessDurationMS: "duration_ms", ProcessConfigVersion: "config_version", ProcessPolicyVersion: "policy_version", ProcessVersion: "version", ProcessInitiatorActorID: "initiator_actor_id"}
	return names[field]
}

func (f *FieldAuthorization) CanRead(name string) bool {
	if f == nil {
		return true
	}
	for _, v := range f.ReadFields {
		if v == name {
			return true
		}
	}
	return false
}
func validateFieldAccess(query ProcessQuery) error {
	if query.Fields == nil {
		return nil
	}
	allowed := make(map[string]bool, len(query.Fields.QueryFields))
	for _, f := range query.Fields.QueryFields {
		allowed[f] = true
	}
	if query.IncludeDeleted && !allowed["is_deleted"] {
		return ErrQueryForbidden
	}
	for _, sort := range query.Sort {
		if !allowed[ProcessFieldName(sort.Field)] {
			return ErrQueryForbidden
		}
	}
	// Stable keyset pagination also exposes the instance ID, including on Get.
	if !allowed["instance_id"] {
		return ErrQueryForbidden
	}
	if len(query.Sort) == 0 && !allowed["completed_at_epoch_ms"] {
		return ErrQueryForbidden
	}
	var visit func(*ProcessExpression) error
	visit = func(e *ProcessExpression) error {
		if e == nil {
			return nil
		}
		if e.Predicate != nil && !allowed[ProcessFieldName(e.Predicate.Field)] {
			return ErrQueryForbidden
		}
		if e.EventRelation != nil {
			if err := validateEventFieldAccess(&e.EventRelation.Expression, allowed); err != nil {
				return err
			}
		}
		for i := range e.Children {
			if err := visit(&e.Children[i]); err != nil {
				return err
			}
		}
		return visit(e.Negated)
	}
	return visit(query.Filter)
}

func validateEventFieldAccess(e *EventExpression, allowed map[string]bool) error {
	if e == nil {
		return nil
	}
	names := map[EventField]string{EventType: "event_type", EventNodeID: "event_node_id", EventActorID: "event_actor_id", EventSequence: "event_sequence", EventOccurredAt: "event_occurred_at", EventConfigVersion: "event_config_version", EventPolicyVersion: "event_policy_version"}
	if e.Predicate != nil && !allowed[names[e.Predicate.Field]] {
		return ErrQueryForbidden
	}
	for i := range e.Children {
		if err := validateEventFieldAccess(&e.Children[i], allowed); err != nil {
			return err
		}
	}
	return validateEventFieldAccess(e.Negated, allowed)
}
