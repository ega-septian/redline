#!/usr/bin/env bash
# Replace the Redline Neo4j database with a dump from ./backups.
# Destructive: everything currently in the database is replaced.
#   ./scripts/restore.sh backups/redline-20261030-120000.dump
set -euo pipefail
cd "$(dirname "$0")/.."

dump="${1:-}"
if [ -z "$dump" ] || [ ! -f "$dump" ]; then
  echo "usage: $0 backups/<database>-<timestamp>.dump" >&2
  exit 1
fi

db="${NEO4J_DATABASE:-redline}"
mkdir -p backups
cp "$dump" "backups/${db}.dump"

docker compose stop redline >/dev/null 2>&1 || true
docker compose stop neo4j >/dev/null

docker compose run --rm -T neo4j \
  neo4j-admin database load "$db" --from-path=/backups --overwrite-destination=true

docker compose up -d neo4j >/dev/null

echo "restored $dump into database ${db}"
echo "start Redline again with: docker compose --profile full up -d"
