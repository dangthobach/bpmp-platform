use std::cmp;

/// Represents a time window [from, until].
/// Time is measured in Unix epoch seconds.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct TemporalInterval {
    pub from: i64,
    pub until: i64,
}

impl TemporalInterval {
    pub fn new(from: i64, until: i64) -> Self {
        Self { from, until }
    }

    /// Check if two intervals overlap.
    pub fn overlaps_with(&self, other: &TemporalInterval) -> bool {
        self.from <= other.until && other.from <= self.until
    }
}

/// A node in the augmented Binary Search Tree (Interval Tree).
#[derive(Clone, Debug)]
pub struct Node {
    pub interval: TemporalInterval,
    pub max_until: i64,
    pub left: Option<Box<Node>>,
    pub right: Option<Box<Node>>,
}

impl Node {
    pub fn new(interval: TemporalInterval) -> Self {
        Self {
            max_until: interval.until,
            interval,
            left: None,
            right: None,
        }
    }
}

/// A standard Interval Tree designed to index Temporal Policies.
/// Finding overlaps is O(log N).
#[derive(Default, Clone, Debug)]
pub struct TemporalIntervalTree {
    root: Option<Box<Node>>,
}

impl TemporalIntervalTree {
    pub fn new() -> Self {
        Self { root: None }
    }

    /// Inserts a new temporal interval into the tree.
    pub fn insert(&mut self, interval: TemporalInterval) {
        self.root = Some(Self::insert_node(self.root.take(), interval));
    }

    fn insert_node(node: Option<Box<Node>>, interval: TemporalInterval) -> Box<Node> {
        match node {
            None => Box::new(Node::new(interval)),
            Some(mut n) => {
                // Standard BST insert by `from` time
                if interval.from < n.interval.from {
                    n.left = Some(Self::insert_node(n.left.take(), interval));
                } else {
                    n.right = Some(Self::insert_node(n.right.take(), interval));
                }

                // Update `max_until` augmented value
                n.max_until = cmp::max(n.max_until, interval.until);
                n
            }
        }
    }

    /// Finds all intervals that overlap with the query interval.
    pub fn find_overlapping(&self, query: TemporalInterval) -> Vec<TemporalInterval> {
        let mut results = Vec::new();
        Self::find_overlapping_recursive(&self.root, &query, &mut results);
        results
    }

    fn find_overlapping_recursive(
        node: &Option<Box<Node>>,
        query: &TemporalInterval,
        results: &mut Vec<TemporalInterval>,
    ) {
        if let Some(n) = node {
            // Prune the branch if the maximum `until` in this subtree is less than the query's `from`.
            // Because intervals are sorted by `from`, if `n.max_until < query.from`,
            // no interval in this subtree can overlap.
            if n.max_until < query.from {
                return;
            }

            // If the node's interval overlaps, add it.
            if n.interval.overlaps_with(query) {
                results.push(n.interval);
            }

            // Always search the left child because it might have a valid `max_until`.
            if n.left.is_some() {
                Self::find_overlapping_recursive(&n.left, query, results);
            }

            // Search the right child only if the query's `until` is greater than or equal to
            // the node's `from`. Because the right subtree contains intervals with `from >= n.from`.
            // If `query.until < n.from`, the right subtree cannot possibly overlap.
            if n.right.is_some() && query.until >= n.interval.from {
                Self::find_overlapping_recursive(&n.right, query, results);
            }
        }
    }
}
