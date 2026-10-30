#!/usr/bin/env bash
# Dump the Redline Neo4j database to ./backups/<database>-<timestamp>.dump.
# The server and database are stopped while the dump runs (Neo4j requires it).
set -euo pipefail
cd "$(dirname "$0")/.."

db="${NEO4J_DATABASE:-redline}"
stamp="$(date +%Y%m%d-%H%M%S)"
mkdir -p backups

docker compose stop redline >/dev/null 2>&1 || true
docker compose stop neo4j >/dev/null

docker compose run --rm -T neo4j \
  neo4j-admin database dump "$db" --to-path=/backups

mv "backups/${db}.dump" "backups/${db}-${stamp}.dump"
docker compose up -d neo4j >/dev/null

echo "backup written: backups/${db}-${stamp}.dump"
echo "start Redline again with: docker compose --profile full up -d"
