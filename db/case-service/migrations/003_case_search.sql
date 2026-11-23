CREATE INDEX customer_cases_tenant_customer_ref_created_idx
  ON customer_cases (tenant_id, customer_ref, created_at DESC, id DESC)
  WHERE NOT is_deleted;
