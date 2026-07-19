use std::collections::BTreeSet;

use super::{
    DiagnosticKind, ParsedProcess, RawBoundaryTrigger, RawNodeKind, SourceLocations, TimerKind,
};

pub(super) fn normalize_sub_processes(parsed: &mut ParsedProcess, locations: &SourceLocations) {
    let mut subprocess_ids = parsed
        .nodes
        .iter()
        .filter(|(_, node)| node.kind == RawNodeKind::SubProcess)
        .map(|(id, node)| (scope_depth(parsed, id), id.clone(), node.offset))
        .collect::<Vec<_>>();
    subprocess_ids.sort_unstable_by(|left, right| right.0.cmp(&left.0));
    for (_, subprocess_id, offset) in subprocess_ids {
        let Some(shape) = validate_sub_process_shape(parsed, &subprocess_id, offset, locations)
        else {
            continue;
        };
        let Some(subprocess) = parsed.nodes.get(&subprocess_id).cloned() else {
            continue;
        };
        if should_retain_sub_process(parsed, &subprocess_id, &subprocess) {
            continue;
        }
        for flow in &mut parsed.flows {
            if flow.target == subprocess_id {
                flow.target.clone_from(&shape.entry_target);
            }
            if flow.target == shape.end_id {
                flow.target.clone_from(&shape.exit_target);
            }
        }
        parsed
            .flows
            .retain(|flow| flow.source != subprocess_id && flow.source != shape.start_id);
        parsed.nodes.remove(&subprocess_id);
        parsed.nodes.remove(&shape.start_id);
        parsed.nodes.remove(&shape.end_id);
        for node in parsed.nodes.values_mut() {
            if node.scope_id.as_deref() == Some(subprocess_id.as_str()) {
                node.scope_id.clone_from(&subprocess.scope_id);
            }
        }
        if let Some(entry) = parsed.nodes.get_mut(&shape.entry_target) {
            entry.properties.extend(subprocess.properties);
            entry.sla_milliseconds = match (entry.sla_milliseconds, subprocess.sla_milliseconds) {
                (None, outer) => outer,
                (inner, None) => inner,
                (Some(inner), Some(outer)) => Some(inner.min(outer)),
            };
        }
    }
}

struct SubProcessShape {
    start_id: String,
    end_id: String,
    entry_target: String,
    exit_target: String,
}

fn validate_sub_process_shape(
    parsed: &mut ParsedProcess,
    subprocess_id: &str,
    offset: usize,
    locations: &SourceLocations,
) -> Option<SubProcessShape> {
    let children = parsed
        .nodes
        .iter()
        .filter(|(_, node)| node.scope_id.as_deref() == Some(subprocess_id))
        .map(|(id, node)| (id.clone(), node.kind))
        .collect::<Vec<_>>();
    let starts = children
        .iter()
        .filter(|(_, kind)| *kind == RawNodeKind::Start)
        .map(|(id, _)| id.clone())
        .collect::<Vec<_>>();
    let ends = children
        .iter()
        .filter(|(_, kind)| *kind == RawNodeKind::End)
        .map(|(id, _)| id.clone())
        .collect::<Vec<_>>();
    let incoming = parsed
        .flows
        .iter()
        .filter(|flow| flow.target == subprocess_id)
        .count();
    let outgoing = parsed
        .flows
        .iter()
        .filter(|flow| flow.source == subprocess_id)
        .collect::<Vec<_>>();
    let start_outgoing = starts.first().map_or(0, |start| {
        parsed
            .flows
            .iter()
            .filter(|flow| flow.source == *start)
            .count()
    });
    let end_incoming = ends.first().map_or(0, |end| {
        parsed
            .flows
            .iter()
            .filter(|flow| flow.target == *end)
            .count()
    });
    if starts.len() != 1
        || ends.len() != 1
        || incoming != 1
        || outgoing.len() != 1
        || start_outgoing != 1
        || end_incoming == 0
    {
        parsed.diagnostics.push(locations.diagnostic(
            offset,
            DiagnosticKind::InvalidSubProcess {
                subprocess_id: subprocess_id.to_owned(),
                detail: format!(
                    "inline normalization requires one outer entry/exit and one inner start/end; found starts={}, ends={}, incoming={incoming}, outgoing={}, start outgoing={start_outgoing}, end incoming={end_incoming}",
                    starts.len(),
                    ends.len(),
                    outgoing.len()
                ),
            },
        ));
        return None;
    }
    let start_id = starts.into_iter().next()?;
    let end_id = ends.into_iter().next()?;
    let entry_target = parsed
        .flows
        .iter()
        .find(|flow| flow.source == start_id)
        .map(|flow| flow.target.clone());
    let Some(entry_target) = entry_target else {
        parsed.diagnostics.push(locations.diagnostic(
            offset,
            DiagnosticKind::InvalidSubProcess {
                subprocess_id: subprocess_id.to_owned(),
                detail: "inner start event has no resolvable outgoing sequence flow".into(),
            },
        ));
        return None;
    };
    Some(SubProcessShape {
        start_id,
        end_id,
        entry_target,
        exit_target: outgoing.into_iter().next()?.target.clone(),
    })
}

fn should_retain_sub_process(
    parsed: &ParsedProcess,
    subprocess_id: &str,
    subprocess: &super::RawNode,
) -> bool {
    subprocess.multi_instance.is_some()
        || subprocess.requires_compensation
        || subprocess.compensation_handler_id.is_some()
        || parsed
            .boundary_events
            .iter()
            .any(|event| event.attached_to == subprocess_id)
}

fn scope_depth(parsed: &ParsedProcess, node_id: &str) -> usize {
    let mut depth = 0_usize;
    let mut scope = parsed
        .nodes
        .get(node_id)
        .and_then(|node| node.scope_id.as_deref());
    while let Some(scope_id) = scope {
        depth = depth.saturating_add(1);
        scope = parsed
            .nodes
            .get(scope_id)
            .and_then(|node| node.scope_id.as_deref());
    }
    depth
}

pub(super) fn resolve_boundary_targets(parsed: &mut ParsedProcess, locations: &SourceLocations) {
    for boundary in &mut parsed.boundary_events {
        if boundary.trigger_count != 1 {
            parsed.diagnostics.push(locations.diagnostic(
                boundary.offset,
                DiagnosticKind::InvalidBoundaryEvent {
                    boundary_id: boundary.id.clone(),
                    detail: format!(
                        "must declare exactly one event definition, found {}",
                        boundary.trigger_count
                    ),
                },
            ));
            continue;
        }
        if boundary.is_compensation {
            continue;
        }
        if !parsed.nodes.contains_key(&boundary.attached_to) {
            parsed.diagnostics.push(locations.diagnostic(
                boundary.offset,
                DiagnosticKind::InvalidBoundaryEvent {
                    boundary_id: boundary.id.clone(),
                    detail: format!("attached activity {} does not exist", boundary.attached_to),
                },
            ));
            continue;
        }
        let outgoing = parsed
            .flows
            .iter()
            .filter(|flow| flow.source == boundary.id)
            .collect::<Vec<_>>();
        if outgoing.len() != 1 {
            parsed.diagnostics.push(locations.diagnostic(
                boundary.offset,
                DiagnosticKind::InvalidBoundaryEvent {
                    boundary_id: boundary.id.clone(),
                    detail: format!(
                        "must have exactly one outgoing sequence flow, found {}",
                        outgoing.len()
                    ),
                },
            ));
            continue;
        }
        if boundary.trigger.is_none() {
            parsed.diagnostics.push(locations.diagnostic(
                boundary.offset,
                DiagnosticKind::InvalidBoundaryEvent {
                    boundary_id: boundary.id.clone(),
                    detail: "must declare timer, error, message, or compensation definition".into(),
                },
            ));
            continue;
        }
        match boundary.trigger.as_ref() {
            Some(RawBoundaryTrigger::Timer { kind, expression })
                if *kind == TimerKind::Unspecified || expression.trim().is_empty() =>
            {
                parsed.diagnostics.push(
                    locations.diagnostic(
                        boundary.offset,
                        DiagnosticKind::InvalidBoundaryEvent {
                            boundary_id: boundary.id.clone(),
                            detail:
                                "timer requires one non-empty timeDate, timeDuration, or timeCycle"
                                    .into(),
                        },
                    ),
                );
                continue;
            }
            Some(RawBoundaryTrigger::Message { message_ref }) if message_ref.trim().is_empty() => {
                parsed.diagnostics.push(locations.diagnostic(
                    boundary.offset,
                    DiagnosticKind::InvalidBoundaryEvent {
                        boundary_id: boundary.id.clone(),
                        detail: "message event requires messageRef".into(),
                    },
                ));
                continue;
            }
            _ => {}
        }
        let Some(target) = outgoing.first().map(|flow| flow.target.clone()) else {
            continue;
        };
        boundary.target = Some(target);
    }
    let boundary_ids = parsed
        .boundary_events
        .iter()
        .filter(|boundary| !boundary.is_compensation && boundary.target.is_some())
        .map(|boundary| boundary.id.as_str())
        .collect::<BTreeSet<_>>();
    parsed
        .flows
        .retain(|flow| !boundary_ids.contains(flow.source.as_str()));
}
