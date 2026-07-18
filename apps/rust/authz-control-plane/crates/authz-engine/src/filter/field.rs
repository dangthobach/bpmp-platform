//! Field masking engine — Layer E-3.
//!
//! Applies field filtering and masking to response data.
//! Called after the resource data is fetched from the backend.

use authz_core::{
    models::{filter::MaskedField, rbac::FieldFilterConfig},
    AuthzError,
};
use serde_json::Value as JsonValue;

/// Result of field filter application.
#[derive(Debug, Clone)]
pub struct FieldFilterResult {
    pub filtered_object: JsonValue,
    pub masked_fields: Vec<MaskedField>,
}

/// Applies field filters to a single resource object.
///
/// ## Logic
/// 1. If `allowed_fields` is non-empty, strip all fields not in the whitelist.
/// 2. For `masked_fields`, replace the value with the mask pattern.
///
/// Returns the filtered object and a list of which fields were masked.
pub fn apply_field_filter(
    object: &JsonValue,
    config: &FieldFilterConfig,
) -> Result<FieldFilterResult, AuthzError> {
    let mut result = match object.as_object() {
        Some(map) => map.clone(),
        None => {
            // Non-object value — return as-is
            return Ok(FieldFilterResult {
                filtered_object: object.clone(),
                masked_fields: vec![],
            });
        }
    };

    // Step 1: Strip fields not in the whitelist (if whitelist is configured)
    if !config.allowed_fields.is_empty() {
        result.retain(|key, _| config.allowed_fields.contains(key));
    }

    // Step 2: Mask sensitive fields
    let mask_pattern = config.mask_pattern.as_deref().unwrap_or("****");

    let mut masked = Vec::new();
    for field in &config.masked_fields {
        if result.contains_key(field.as_str()) {
            result.insert(field.clone(), JsonValue::String(mask_pattern.to_owned()));
            masked.push(MaskedField {
                field: field.clone(),
                pattern: mask_pattern.to_owned(),
            });
        }
    }

    Ok(FieldFilterResult {
        filtered_object: JsonValue::Object(result),
        masked_fields: masked,
    })
}

/// Applies field filters to a list of resource objects.
///
/// Processes each object independently. Errors in individual items are
/// propagated immediately (fail-fast).
pub fn apply_field_filter_to_list(
    objects: &[JsonValue],
    config: &FieldFilterConfig,
) -> Result<Vec<FieldFilterResult>, AuthzError> {
    objects
        .iter()
        .map(|obj| apply_field_filter(obj, config))
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    use authz_core::models::rbac::FieldFilterConfig;

    #[test]
    fn test_allowed_fields_strips_others() {
        let obj = serde_json::json!({
            "id": "abc",
            "branch_code": "HN01",
            "national_id": "123456789",
            "salary": 10000
        });

        let config = FieldFilterConfig {
            allowed_fields: vec!["id".to_owned(), "branch_code".to_owned()],
            masked_fields: vec![],
            mask_pattern: None,
        };

        let result = apply_field_filter(&obj, &config).unwrap();
        assert!(result.filtered_object.get("id").is_some());
        assert!(result.filtered_object.get("branch_code").is_some());
        assert!(result.filtered_object.get("national_id").is_none());
        assert!(result.filtered_object.get("salary").is_none());
    }

    #[test]
    fn test_masked_fields_replaced_with_pattern() {
        let obj = serde_json::json!({
            "id": "abc",
            "phone": "0901234567",
            "email": "user@bank.vn"
        });

        let config = FieldFilterConfig {
            allowed_fields: vec![],
            masked_fields: vec!["phone".to_owned(), "email".to_owned()],
            mask_pattern: Some("***-***-####".to_owned()),
        };

        let result = apply_field_filter(&obj, &config).unwrap();
        assert_eq!(
            result
                .filtered_object
                .get("phone")
                .unwrap()
                .as_str()
                .unwrap(),
            "***-***-####"
        );
        assert_eq!(result.masked_fields.len(), 2);
    }

    #[test]
    fn test_empty_config_returns_all_fields() {
        let obj = serde_json::json!({ "id": "abc", "name": "Test" });
        let config = FieldFilterConfig {
            allowed_fields: vec![],
            masked_fields: vec![],
            mask_pattern: None,
        };

        let result = apply_field_filter(&obj, &config).unwrap();
        assert!(result.filtered_object.get("id").is_some());
        assert!(result.filtered_object.get("name").is_some());
        assert!(result.masked_fields.is_empty());
    }
}
