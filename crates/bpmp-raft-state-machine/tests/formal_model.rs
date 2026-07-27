use stateright::{Checker, Model, Property};

const NODES: usize = 3;
const QUORUM: usize = 2;

#[derive(Clone, Debug, Eq, Hash, PartialEq)]
struct ClusterState {
    links: [u8; NODES],
    alive: [bool; NODES],
    terms: [u8; NODES],
    leaders: [Option<u8>; NODES],
    logs: [Vec<u8>; NODES],
    applied: [Vec<u8>; NODES],
    committed: Vec<u8>,
    commit_without_quorum: bool,
    history_prefix_violation: bool,
}

#[derive(Clone, Debug, Eq, Hash, PartialEq)]
enum Action {
    Elect(usize),
    Propose { node: usize, value: u8 },
    Isolate(usize),
    Heal,
    Crash(usize),
    Restart(usize),
    CatchUp { leader: usize, follower: usize },
}

struct RaftIntegrationModel;

impl Model for RaftIntegrationModel {
    type State = ClusterState;
    type Action = Action;

    fn init_states(&self) -> Vec<Self::State> {
        vec![ClusterState {
            links: [0b111; NODES],
            alive: [true; NODES],
            terms: [0; NODES],
            leaders: [None; NODES],
            logs: std::array::from_fn(|_| Vec::new()),
            applied: std::array::from_fn(|_| Vec::new()),
            committed: Vec::new(),
            commit_without_quorum: false,
            history_prefix_violation: false,
        }]
    }

    fn actions(&self, state: &Self::State, actions: &mut Vec<Self::Action>) {
        for node in 0..NODES {
            actions.push(Action::Elect(node));
            actions.push(Action::Isolate(node));
            if state.alive[node] {
                actions.push(Action::Crash(node));
            } else {
                actions.push(Action::Restart(node));
            }
            if state.committed.len() < 2 {
                actions.push(Action::Propose {
                    node,
                    value: u8::try_from(state.committed.len().saturating_add(1)).unwrap_or(u8::MAX),
                });
            }
            for follower in 0..NODES {
                if follower != node {
                    actions.push(Action::CatchUp {
                        leader: node,
                        follower,
                    });
                }
            }
        }
        actions.push(Action::Heal);
    }

    fn next_state(&self, state: &Self::State, action: Self::Action) -> Option<Self::State> {
        let mut next = state.clone();
        match action {
            Action::Elect(candidate) => {
                let voters = reachable_acceptors(state, candidate, u8::MAX);
                if voters.len() < QUORUM || !candidate_is_up_to_date(state, candidate, &voters) {
                    return None;
                }
                let term = state.terms.iter().copied().max()?.checked_add(1)?;
                for voter in voters {
                    next.terms[voter] = term;
                    next.leaders[voter] = None;
                }
                next.terms[candidate] = term;
                next.leaders[candidate] = Some(term);
            }
            Action::Propose { node, value } => {
                let term = state.leaders[node]?;
                if !state.alive[node] || state.terms[node] != term {
                    return None;
                }
                let acceptors = reachable_acceptors(state, node, term);
                next.logs[node].push(value);
                if acceptors.len() < QUORUM {
                    return Some(next);
                }
                let mut committed = state.committed.clone();
                committed.push(value);
                next.history_prefix_violation |= !committed.starts_with(&state.committed);
                for acceptor in &acceptors {
                    next.terms[*acceptor] = term;
                    next.logs[*acceptor].clone_from(&committed);
                    next.applied[*acceptor].clone_from(&committed);
                }
                next.committed = committed;
                next.commit_without_quorum |= acceptors.len() < QUORUM;
            }
            Action::Isolate(node) => {
                for peer in 0..NODES {
                    next.links[node] &= !(1 << peer);
                    next.links[peer] &= !(1 << node);
                }
                next.links[node] |= 1 << node;
            }
            Action::Heal => next.links = [0b111; NODES],
            Action::Crash(node) => {
                if state.alive.iter().filter(|alive| !**alive).count() >= 1 {
                    return None;
                }
                next.alive[node] = false;
                next.leaders[node] = None;
            }
            Action::Restart(node) => next.alive[node] = true,
            Action::CatchUp { leader, follower } => {
                let term = state.leaders[leader]?;
                if !connected(state, leader, follower)
                    || !state.alive[leader]
                    || !state.alive[follower]
                    || term < state.terms[follower]
                {
                    return None;
                }
                next.terms[follower] = term;
                next.logs[follower].clone_from(&state.committed);
                next.applied[follower].clone_from(&state.committed);
            }
        }
        Some(next)
    }

    fn properties(&self) -> Vec<Property<Self>> {
        vec![
            Property::<Self>::always("minority cannot commit", |_, state| {
                !state.commit_without_quorum
            }),
            Property::<Self>::always("committed prefix is never replaced", |_, state| {
                !state.history_prefix_violation
            }),
            Property::<Self>::always("applied state machines preserve one order", |_, state| {
                state.applied.iter().all(|applied| {
                    applied.len() <= state.committed.len()
                        && applied
                            .iter()
                            .zip(&state.committed)
                            .all(|(actual, expected)| actual == expected)
                })
            }),
            Property::<Self>::always("one crash cannot erase every committed copy", |_, state| {
                state.committed.is_empty()
                    || state.logs.iter().enumerate().any(|(node, log)| {
                        state.alive[node]
                            && log.len() >= state.committed.len()
                            && log[..state.committed.len()] == state.committed
                    })
            }),
            Property::<Self>::sometimes("two quorum commits are reachable", |_, state| {
                state.committed == [1, 2]
            }),
        ]
    }
}

fn connected(state: &ClusterState, left: usize, right: usize) -> bool {
    state.links[left] & (1 << right) != 0 && state.links[right] & (1 << left) != 0
}

fn reachable_acceptors(state: &ClusterState, leader: usize, term: u8) -> Vec<usize> {
    (0..NODES)
        .filter(|node| {
            state.alive[leader]
                && state.alive[*node]
                && connected(state, leader, *node)
                && (term == u8::MAX || state.terms[*node] <= term)
        })
        .collect()
}

fn candidate_is_up_to_date(state: &ClusterState, candidate: usize, voters: &[usize]) -> bool {
    voters
        .iter()
        .all(|voter| state.logs[candidate].len() >= state.logs[*voter].len())
}

#[test]
fn bounded_three_node_raft_integration_model_satisfies_safety_properties() {
    RaftIntegrationModel
        .checker()
        .threads(1)
        .target_max_depth(12)
        .spawn_bfs()
        .join()
        .assert_properties();
}
