use std::collections::HashSet;
use std::future::Future;
use std::pin::Pin;
use std::time::Duration;

use async_trait::async_trait;
use moka::future::Cache;

/// Trait to provide neighbor nodes in a ReBAC relation graph.
/// This allows the IDDFS algorithm to work independently of the underlying storage (DB, Redis, In-Memory).
#[async_trait]
pub trait GraphProvider: Send + Sync {
    /// Fetch all targets that the `subject` is connected to via the given `relation`
    /// within the context of a specific tenant.
    async fn get_neighbors(&self, tenant_id: &str, subject: &str, relation: &str) -> Vec<String>;
}

/// Cache key for a single reachability decision.
/// Components are stored as owned strings to avoid lifetime entanglement
/// with the borrowed `&str` arguments on the hot path.
type ReachabilityKey = (String, String, String, String);

/// An engine that evaluates ReBAC queries using Iterative Deepening DFS (IDDFS).
///
/// IDDFS provides the shortest-path guarantees of BFS (Breadth-First Search)
/// while maintaining the low memory overhead of DFS (O(depth) instead of O(width)).
/// This is critical in authorization graphs to avoid out-of-memory errors on "Big Nodes".
///
/// A short-lived `moka` cache memoizes the final `(tenant, subject, relation, object) → bool`
/// outcome so that repeated checks inside a single request (e.g. row-filter expansion
/// that touches the same tuple many times) do not re-traverse the graph.
pub struct PermissionIddfsEngine<P: GraphProvider> {
    provider: P,
    max_depth: u32,
    memo: Cache<ReachabilityKey, bool>,
}

impl<P: GraphProvider> PermissionIddfsEngine<P> {
    pub fn new(provider: P, max_depth: u32) -> Self {
        Self::with_memo_config(provider, max_depth, 50_000, Duration::from_secs(5))
    }

    /// Creates the engine with explicit memo-cache sizing.
    pub fn with_memo_config(
        provider: P,
        max_depth: u32,
        memo_capacity: u64,
        memo_ttl: Duration,
    ) -> Self {
        let memo = Cache::builder()
            .max_capacity(memo_capacity)
            .time_to_live(memo_ttl)
            .build();
        Self {
            provider,
            max_depth,
            memo,
        }
    }

    /// Checks if a path exists from `subject` to `target` via `relation` using IDDFS.
    pub async fn check_permission(
        &self,
        tenant_id: &str,
        subject: &str,
        relation: &str,
        target: &str,
    ) -> bool {
        let key: ReachabilityKey = (
            tenant_id.to_owned(),
            subject.to_owned(),
            relation.to_owned(),
            target.to_owned(),
        );
        if let Some(cached) = self.memo.get(&key).await {
            return cached;
        }

        // Incrementally deepen the search to find the shortest path first
        let mut found = false;
        for limit in 1..=self.max_depth {
            let mut visited = HashSet::new();
            if self
                .dfs_with_limit(tenant_id, subject, relation, target, limit, &mut visited)
                .await
            {
                found = true;
                break;
            }
        }
        self.memo.insert(key, found).await;
        found
    }

    /// Recursive bounded DFS.
    /// Uses `Box::pin` to handle async recursion safely in Rust.
    fn dfs_with_limit<'a>(
        &'a self,
        tenant_id: &'a str,
        current: &'a str,
        relation: &'a str,
        target: &'a str,
        limit: u32,
        visited: &'a mut HashSet<String>,
    ) -> Pin<Box<dyn Future<Output = bool> + Send + 'a>> {
        Box::pin(async move {
            if current == target {
                return true;
            }
            if limit == 0 {
                return false;
            }
            if visited.contains(current) {
                return false; // Prevent cycles
            }

            // Mark node as visited for this current path exploration
            visited.insert(current.to_string());

            let neighbors = self
                .provider
                .get_neighbors(tenant_id, current, relation)
                .await;

            for next in neighbors {
                if self
                    .dfs_with_limit(tenant_id, &next, relation, target, limit - 1, visited)
                    .await
                {
                    return true;
                }
            }

            // BACKTRACK: Remove current node to allow other paths to reach it.
            // If we don't backtrack, we might falsely reject a valid longer path
            // just because a shorter dead-end path visited this node first.
            visited.remove(current);
            false
        })
    }
}
