#[cfg(test)]
mod tests {
    use crate::algorithms::iddfs::{GraphProvider, PermissionIddfsEngine};
    use async_trait::async_trait;
    use std::collections::HashMap;

    struct MockGraphProvider {
        // Map from "subject" to "targets"
        graph: HashMap<String, Vec<String>>,
    }

    #[async_trait]
    impl GraphProvider for MockGraphProvider {
        async fn get_neighbors(
            &self,
            _tenant_id: &str,
            subject: &str,
            _relation: &str,
        ) -> Vec<String> {
            self.graph.get(subject).cloned().unwrap_or_default()
        }
    }

    #[tokio::test]
    async fn test_iddfs_shortest_path() {
        let mut graph = HashMap::new();
        // CEO delegates to VP
        graph.insert("CEO".to_string(), vec!["VP".to_string()]);
        // VP delegates to Manager
        graph.insert("VP".to_string(), vec!["Manager".to_string()]);
        // Manager delegates to Staff
        graph.insert("Manager".to_string(), vec!["Staff".to_string()]);

        let provider = MockGraphProvider { graph };
        let engine = PermissionIddfsEngine::new(provider, 3);

        // Path exists within depth 3
        assert!(
            engine
                .check_permission("tenant1", "CEO", "delegate", "Staff")
                .await
        );
        // Direct path
        assert!(
            engine
                .check_permission("tenant1", "CEO", "delegate", "VP")
                .await
        );
        // Path doesn't exist
        assert!(
            !engine
                .check_permission("tenant1", "Staff", "delegate", "CEO")
                .await
        );
    }

    #[tokio::test]
    async fn test_iddfs_depth_limit_exceeded() {
        let mut graph = HashMap::new();
        graph.insert("A".to_string(), vec!["B".to_string()]);
        graph.insert("B".to_string(), vec!["C".to_string()]);
        graph.insert("C".to_string(), vec!["D".to_string()]);

        let provider = MockGraphProvider { graph };
        let engine = PermissionIddfsEngine::new(provider, 2);

        // A -> B -> C -> D is depth 3. Limit is 2.
        assert!(!engine.check_permission("tenant1", "A", "rel", "D").await);
    }

    #[tokio::test]
    async fn test_iddfs_cycle_prevention() {
        let mut graph = HashMap::new();
        graph.insert("A".to_string(), vec!["B".to_string()]);
        graph.insert("B".to_string(), vec!["C".to_string()]);
        graph.insert("C".to_string(), vec!["A".to_string()]); // Cycle!

        let provider = MockGraphProvider { graph };
        let engine = PermissionIddfsEngine::new(provider, 5);

        // Should safely return false without infinite loop
        assert!(!engine.check_permission("tenant1", "A", "rel", "D").await);
        // Should find path within cycle
        assert!(engine.check_permission("tenant1", "A", "rel", "C").await);
    }
}
