#!/usr/bin/env bash
set -Eeuo pipefail

for variable in \
  KAFKA_BROKERS \
  ENGINE_COMMITTED_EVENTS_TOPIC \
  CONFIGURATION_PUBLICATIONS_TOPIC \
  HUMAN_ESCALATIONS_TOPIC \
  KAFKA_TOPIC_PARTITIONS \
  KAFKA_TOPIC_REPLICATION_FACTOR \
  KAFKA_RETENTION_MS; do
  if [[ -z "${!variable:-}" ]]; then
    echo "$variable is required" >&2
    exit 2
  fi
done

if [[ ! "$KAFKA_TOPIC_PARTITIONS" =~ ^[1-9][0-9]*$ ]] ||
   [[ ! "$KAFKA_TOPIC_REPLICATION_FACTOR" =~ ^[1-9][0-9]*$ ]] ||
   [[ ! "$KAFKA_RETENTION_MS" =~ ^[1-9][0-9]*$ ]]; then
  echo "Kafka partition, replication and retention values must be positive integers" >&2
  exit 2
fi

validate_name() {
  local name=$1
  if [[ ! "$name" =~ ^bpmp\.[a-z0-9-]+\.[a-z0-9.-]+\.v[1-9][0-9]*\.[a-z0-9-]+$ ]]; then
    echo "Kafka topic does not follow the BPMP naming contract: $name" >&2
    exit 2
  fi
}

topics=(
  "$ENGINE_COMMITTED_EVENTS_TOPIC"
  "$CONFIGURATION_PUBLICATIONS_TOPIC"
  "$HUMAN_ESCALATIONS_TOPIC"
)

for topic in "${topics[@]}"; do
  validate_name "$topic"
  if rpk topic describe "$topic" -X "brokers=$KAFKA_BROKERS" >/dev/null 2>&1; then
    echo "skip existing Kafka topic: $topic"
    continue
  fi
  rpk topic create "$topic" \
    -X "brokers=$KAFKA_BROKERS" \
    --partitions "$KAFKA_TOPIC_PARTITIONS" \
    --replicas "$KAFKA_TOPIC_REPLICATION_FACTOR" \
    --topic-config "retention.ms=$KAFKA_RETENTION_MS"
done

echo "Kafka topic provisioning complete"
