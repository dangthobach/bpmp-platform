#[cfg(test)]
mod tests {
    use crate::algorithms::bitmap::PermissionBitmapEngine;
    use authz_core::ids::UserId;
    use std::collections::HashMap;

    fn setup_engine() -> PermissionBitmapEngine {
        let mut index = HashMap::new();
        index.insert("doc:read".to_string(), 1);
        index.insert("doc:write".to_string(), 2);
        index.insert("admin:delete".to_string(), 3);
        PermissionBitmapEngine::new(index)
    }

    #[test]
    fn test_has_permission() {
        let engine = setup_engine();
        let user_id = UserId::new();

        engine.build_for_user(
            user_id,
            &["doc:read".to_string(), "admin:delete".to_string()],
        );

        assert!(engine.has_permission(user_id, "doc:read"));
        assert!(engine.has_permission(user_id, "admin:delete"));
        assert!(!engine.has_permission(user_id, "doc:write"));
        assert!(!engine.has_permission(user_id, "unknown:perm"));
    }

    #[test]
    fn test_has_all_permissions() {
        let engine = setup_engine();
        let user_id = UserId::new();

        engine.build_for_user(user_id, &["doc:read".to_string(), "doc:write".to_string()]);

        assert!(engine.has_all_permissions(user_id, &["doc:read", "doc:write"]));
        assert!(!engine.has_all_permissions(user_id, &["doc:read", "admin:delete"]));
    }

    #[test]
    fn test_has_any_permission() {
        let engine = setup_engine();
        let user_id = UserId::new();

        engine.build_for_user(user_id, &["doc:read".to_string()]);

        assert!(engine.has_any_permission(user_id, &["doc:read", "admin:delete"]));
        assert!(!engine.has_any_permission(user_id, &["doc:write", "admin:delete"]));
    }
}
