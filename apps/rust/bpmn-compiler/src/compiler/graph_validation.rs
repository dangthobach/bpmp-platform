use std::collections::{BTreeMap, BTreeSet, VecDeque};

use super::{DiagnosticKind, ParsedProcess, RawNode, RawNodeKind, SourceLocations};

struct JoinAnalysis {
    distance_to_join: BTreeMap<String, usize>,
    guaranteed_to_reach_join: BTreeSet<String>,
}

#[allow(clippy::too_many_lines)]
pub(super) fn validate_gateway_structure(parsed: &mut ParsedProcess, locations: &SourceLocations) {
    let mut outgoing = BTreeMap::<String, Vec<String>>::new();
    let mut incoming = BTreeMap::<String, Vec<String>>::new();
    for flow in &parsed.flows {
        outgoing
            .entry(flow.source.clone())
            .or_default()
            .push(flow.target.clone());
        incoming
            .entry(flow.target.clone())
            .or_default()
            .push(flow.source.clone());
    }
    for flow in &parsed.flows {
        if parsed.nodes.get(&flow.source).is_some_and(|node| {
            node.kind == RawNodeKind::ParallelGateway
                && outgoing
                    .get(&flow.source)
                    .is_some_and(|targets| targets.len() >= 2)
        }) && (flow.condition.is_some() || flow.is_default)
        {
            parsed.diagnostics.push(locations.diagnostic(
                flow.offset,
                DiagnosticKind::InvalidGatewayFlow {
                    gateway_id: flow.source.clone(),
                    detail: "parallel split flows cannot declare guards or defaults".into(),
                },
            ));
        }
    }

    let topological_order = match topological_order(&parsed.nodes, &outgoing) {
        Ok(order) => order,
        Err(cycle_node) => {
            let offset = parsed.nodes.get(&cycle_node).map_or(0, |node| node.offset);
            parsed.diagnostics.push(locations.diagnostic(
                offset,
                DiagnosticKind::UnexpectedCycle {
                    node_id: cycle_node,
                },
            ));
            return;
        }
    };

    let gateways = parsed
        .nodes
        .iter()
        .filter(|(_, node)| {
            matches!(
                node.kind,
                RawNodeKind::ParallelGateway | RawNodeKind::InclusiveGateway
            )
        })
        .map(|(id, node)| (id.clone(), node.kind, node.offset))
        .collect::<Vec<_>>();
    let joins = gateways
        .iter()
        .filter(|(id, _, _)| incoming.get(id).map_or(0, Vec::len) >= 2)
        .collect::<Vec<_>>();
    let join_analyses = joins
        .iter()
        .map(|(join_id, _, _)| {
            (
                join_id.as_str(),
                analyze_join(join_id, &incoming, &outgoing, &topological_order),
            )
        })
        .collect::<BTreeMap<_, _>>();
    let mut paired_joins = BTreeSet::new();

    for (split_id, split_kind, split_offset) in gateways
        .iter()
        .filter(|(id, _, _)| outgoing.get(id).map_or(0, Vec::len) >= 2)
    {
        let branches = outgoing.get(split_id).map_or(&[][..], Vec::as_slice);
        let mut candidates = joins
            .iter()
            .filter(|(_, join_kind, _)| join_kind == split_kind)
            .filter_map(|(join_id, _, _)| {
                let analysis = join_analyses.get(join_id.as_str())?;
                let distance = branches
                    .iter()
                    .map(|branch| analysis.distance_to_join.get(branch).copied())
                    .collect::<Option<Vec<_>>>()?
                    .into_iter()
                    .max()
                    .unwrap_or_default();
                Some((distance, (*join_id).clone()))
            })
            .collect::<Vec<_>>();
        candidates.sort_unstable();
        let Some((_, join_id)) = candidates.first() else {
            parsed.diagnostics.push(locations.diagnostic(
                *split_offset,
                DiagnosticKind::UnbalancedGateway {
                    gateway_id: split_id.clone(),
                    detail: "split has no reachable matching join".into(),
                },
            ));
            continue;
        };
        let Some(analysis) = join_analyses.get(join_id.as_str()) else {
            parsed.diagnostics.push(locations.diagnostic(
                *split_offset,
                DiagnosticKind::InternalCompilerInvariant {
                    phase: "gateway-validation",
                    detail: format!("paired join {join_id} has no cached analysis"),
                },
            ));
            continue;
        };
        if !branches
            .iter()
            .all(|branch| analysis.guaranteed_to_reach_join.contains(branch))
        {
            parsed.diagnostics.push(locations.diagnostic(
                *split_offset,
                DiagnosticKind::UnbalancedGateway {
                    gateway_id: split_id.clone(),
                    detail: format!("a branch can escape before paired join {join_id}"),
                },
            ));
            continue;
        }
        if !paired_joins.insert(join_id.clone()) {
            parsed.diagnostics.push(locations.diagnostic(
                *split_offset,
                DiagnosticKind::UnbalancedGateway {
                    gateway_id: split_id.clone(),
                    detail: format!("join {join_id} is paired with more than one split"),
                },
            ));
            continue;
        }
        parsed
            .gateway_pairs
            .insert(split_id.clone(), join_id.clone());
        parsed
            .gateway_pairs
            .insert(join_id.clone(), split_id.clone());
    }

    for (join_id, _, offset) in joins {
        if !paired_joins.contains(join_id) {
            parsed.diagnostics.push(locations.diagnostic(
                *offset,
                DiagnosticKind::UnbalancedGateway {
                    gateway_id: join_id.clone(),
                    detail: "join has no matching split".into(),
                },
            ));
        }
    }
}

fn analyze_join(
    join: &str,
    incoming: &BTreeMap<String, Vec<String>>,
    outgoing: &BTreeMap<String, Vec<String>>,
    topological_order: &[String],
) -> JoinAnalysis {
    let mut distance_to_join = BTreeMap::from([(join.to_owned(), 0_usize)]);
    let mut pending = VecDeque::from([(join, 0_usize)]);
    while let Some((node, distance)) = pending.pop_front() {
        for predecessor in incoming.get(node).into_iter().flatten() {
            if distance_to_join.contains_key(predecessor) {
                continue;
            }
            let predecessor_distance = distance.saturating_add(1);
            distance_to_join.insert(predecessor.clone(), predecessor_distance);
            pending.push_back((predecessor, predecessor_distance));
        }
    }

    let mut guaranteed_to_reach_join = BTreeSet::from([join.to_owned()]);
    for node in topological_order.iter().rev() {
        if node == join {
            continue;
        }
        if outgoing.get(node).is_some_and(|targets| {
            !targets.is_empty()
                && targets
                    .iter()
                    .all(|target| guaranteed_to_reach_join.contains(target))
        }) {
            guaranteed_to_reach_join.insert(node.clone());
        }
    }
    JoinAnalysis {
        distance_to_join,
        guaranteed_to_reach_join,
    }
}

fn topological_order(
    nodes: &BTreeMap<String, RawNode>,
    outgoing: &BTreeMap<String, Vec<String>>,
) -> Result<Vec<String>, String> {
    let mut indegree = nodes
        .keys()
        .map(|node| (node.clone(), 0_usize))
        .collect::<BTreeMap<_, _>>();
    for targets in outgoing.values() {
        for target in targets {
            if let Some(count) = indegree.get_mut(target) {
                *count = count.saturating_add(1);
            }
        }
    }
    let mut ready = indegree
        .iter()
        .filter(|(_, count)| **count == 0)
        .map(|(node, _)| node.clone())
        .collect::<VecDeque<_>>();
    let mut order = Vec::with_capacity(nodes.len());
    while let Some(node) = ready.pop_front() {
        if let Some(targets) = outgoing.get(&node) {
            for target in targets {
                let Some(count) = indegree.get_mut(target) else {
                    continue;
                };
                *count = count.saturating_sub(1);
                if *count == 0 {
                    ready.push_back(target.clone());
                }
            }
        }
        indegree.remove(&node);
        order.push(node);
    }
    match indegree.into_keys().next() {
        Some(cycle_node) => Err(cycle_node),
        None => Ok(order),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn join_analysis_computes_distances_once_and_rejects_escaping_paths() {
        let incoming = BTreeMap::from([
            ("a".into(), vec!["split".into()]),
            ("b".into(), vec!["split".into()]),
            ("join".into(), vec!["a".into(), "b".into()]),
            ("escape".into(), vec!["b".into()]),
        ]);
        let outgoing = BTreeMap::from([
            ("split".into(), vec!["a".into(), "b".into()]),
            ("a".into(), vec!["join".into()]),
            ("b".into(), vec!["join".into(), "escape".into()]),
            ("join".into(), vec!["end".into()]),
        ]);
        let order = ["split", "a", "b", "join", "escape", "end"].map(str::to_owned);

        let analysis = analyze_join("join", &incoming, &outgoing, &order);

        assert_eq!(analysis.distance_to_join["split"], 2);
        assert!(analysis.guaranteed_to_reach_join.contains("a"));
        assert!(!analysis.guaranteed_to_reach_join.contains("b"));
        assert!(!analysis.guaranteed_to_reach_join.contains("split"));
    }
}
