//! Batch helper to fan-out N independent checks concurrently.
//!
//! Use this from list / projection paths where you must decide on
//! many objects: it caps in-flight requests so the PEP does not flood
//! the PDP, and it deduplicates identical requests within a batch
//! (same key → 1 outbound call, N callers awaited).

use std::collections::HashMap;
use std::sync::Arc;

use futures::future::join_all;
use tokio::sync::Semaphore;

use crate::client::AuthzClient;
use crate::error::SdkError;
use crate::types::{CheckRequest, CheckResponse};

pub struct BatchExecutor<C: AuthzClient> {
    client: Arc<C>,
    permits: Arc<Semaphore>,
}

impl<C: AuthzClient + 'static> BatchExecutor<C> {
    pub fn new(client: Arc<C>, max_in_flight: usize) -> Self {
        Self {
            client,
            permits: Arc::new(Semaphore::new(max_in_flight.max(1))),
        }
    }

    /// Run `requests` concurrently and return responses in the **same order**.
    ///
    /// Complexity: O(N) deduplication via hashmap + O(K) network calls where
    /// K ≤ N is the number of distinct requests in the batch.
    pub async fn check_all(
        &self,
        requests: Vec<CheckRequest>,
    ) -> Vec<Result<CheckResponse, SdkError>> {
        if requests.is_empty() {
            return Vec::new();
        }

        // Deduplicate by structural key — callers asking the same question
        // share the same outbound call.
        let mut dedup: HashMap<String, Vec<usize>> = HashMap::with_capacity(requests.len());
        for (idx, req) in requests.iter().enumerate() {
            let k = format!(
                "{}|{}|{}|{}|{}|{}",
                req.tenant_id,
                req.user_id,
                req.user_attributes_version,
                req.action,
                req.resource_type,
                req.resource_ref.as_deref().unwrap_or("-")
            );
            dedup.entry(k).or_default().push(idx);
        }

        let mut out: Vec<Option<Result<CheckResponse, SdkError>>> =
            (0..requests.len()).map(|_| None).collect();

        let tasks = dedup.into_values().map(|indices| {
            let first = indices[0];
            let req = requests[first].clone();
            let client = Arc::clone(&self.client);
            let permits = Arc::clone(&self.permits);
            async move {
                let _permit = permits.acquire().await.expect("semaphore closed");
                let res = client.check(&req).await;
                (indices, res)
            }
        });

        for (indices, res) in join_all(tasks).await {
            for i in indices {
                out[i] = Some(clone_result(&res));
            }
        }

        out.into_iter()
            .map(|o| o.expect("every index filled"))
            .collect()
    }
}

fn clone_result(r: &Result<CheckResponse, SdkError>) -> Result<CheckResponse, SdkError> {
    match r {
        Ok(v) => Ok(v.clone()),
        Err(e) => Err(SdkError::PdpError {
            status: 0,
            code: e.code().to_owned(),
            message: e.to_string(),
        }),
    }
}
