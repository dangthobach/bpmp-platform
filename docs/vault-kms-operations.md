# Vault KMS Operations

BPMP's production payload-key adapter uses Vault Transit for DEK wrapping and
Vault KV v2 for durable wrapped-DEK records. The engine never stores a plaintext
DEK. It holds decrypted DEKs only in a bounded, expiring, zeroizing memory cache.

## Trust split

- Engine identity: read `/<kv>/data/*` and update
  `/<transit>/decrypt/<master-key>` only.
- Governance identity: update `/<transit>/datakey/plaintext/<master-key>`,
  create/update `/<kv>/data/*`, and delete `/<kv>/metadata/*`.
- Operators create the Transit master key ahead of deployment and disable Transit
  key upsert globally. Neither identity should have Transit key export or backup
  permissions.

The KV record key is URL-safe base64 of the complete `KeyScope`. Each version
contains `key_scope`, monotonic `key_epoch`, and `wrapped_dek`. KV CAS is required:
zero for initial creation and the current KV version for rotation.

## Engine configuration

Use exactly one payload-key source. Production must configure `payload_kms` and
leave `payload_keys` empty. File keys are rejected unless the explicit
development-only opt-in is true.

Pin the Governance Service leaf certificate in the engine's `authorization`
section. The revocation RPC rejects every other mTLS workload even when its
certificate chains to the internal CA:

```json
{
  "authorization": {
    "governance_client_certificate_sha256": [
      "<64 lowercase hex characters>"
    ]
  }
}
```

```json
{
  "payload_keys": [],
  "allow_development_file_keys": false,
  "payload_kms": {
    "address": "https://vault.internal:8200",
    "token_path": "/run/secrets/vault-token",
    "namespace": "bpmp/production",
    "transit_mount": "transit",
    "kv_mount": "bpmp-wrapped-deks",
    "master_key_name": "bpmp-payload-root",
    "request_timeout_ms": 1500,
    "cache_capacity": 4096,
    "cache_ttl_ms": 30000
  }
}
```

The token file should be supplied by Vault Agent and rotated atomically. The
adapter rereads it for every Vault request, so rotation does not require an
engine restart. Do not place a token in JSON or an environment variable.

## Rotation and shredding protocol

1. Governance creates the next epoch using KV CAS.
2. New engine writes discover it no later than the configured cache TTL.
3. Before shredding epoch `N`, governance obtains a revocation-barrier
   acknowledgement from every serving engine. Each engine invalidates all cached
   entries for the scope at or below `N` and fences later lookups.
4. Only after barrier acknowledgement may governance delete KV metadata for the
   scope. Deleting KV metadata destroys all wrapped-DEK versions and is
   irreversible from the live Vault cluster.

Backups and snapshots containing wrapped DEKs are part of the cryptographic
erasure boundary. Retention and destruction policy for Vault backups must be at
least as strict as the BPMP data-retention policy.

Vault failure, timeout, malformed data, missing scope, or unwrap failure aborts
the engine write. There is no plaintext fallback.
