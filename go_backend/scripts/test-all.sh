#!/usr/bin/env bash
# Run Go backend tests against both SQLite and PostgreSQL.
#
# Usage:
#   ./scripts/test-all.sh           # starts PG if needed, runs all tests
#   ./scripts/test-all.sh --sqlite  # SQLite only (no PG)
#
# Requires podman (or set CONTAINER_CLI=docker).

set -euo pipefail

CLI="${CONTAINER_CLI:-podman}"
PG_CONTAINER="narrafork-pg"
PG_PORT=5433
PG_TEST_DB="narrafork_test"
PG_DSN="postgres://narrafork:narrafork@localhost:${PG_PORT}/${PG_TEST_DB}?sslmode=disable"

cd "$(dirname "$0")/.."

if [[ "${1:-}" == "--sqlite" ]]; then
	echo ">> Running SQLite-only tests"
	go test ./... "${@:2}"
	exit 0
fi

# Ensure PG container is running
if ! $CLI ps --format '{{.Names}}' | grep -q "^${PG_CONTAINER}$"; then
	if $CLI ps -a --format '{{.Names}}' | grep -q "^${PG_CONTAINER}$"; then
		echo ">> Starting existing ${PG_CONTAINER}"
		$CLI start "$PG_CONTAINER"
	else
		echo ">> Creating ${PG_CONTAINER} (postgres:17-alpine)"
		$CLI run -d \
			--name "$PG_CONTAINER" \
			-e POSTGRES_USER=narrafork \
			-e POSTGRES_PASSWORD=narrafork \
			-e POSTGRES_DB=narrafork \
			-p "${PG_PORT}:5432" \
			docker.io/library/postgres:17-alpine
	fi
fi

# Wait for readiness
echo ">> Waiting for PostgreSQL..."
for _ in $(seq 1 30); do
	if $CLI exec "$PG_CONTAINER" pg_isready -U narrafork >/dev/null 2>&1; then
		break
	fi
	sleep 1
done

# Ensure test database exists (drop+create for clean state)
$CLI exec "$PG_CONTAINER" psql -U narrafork -d narrafork -c "DROP DATABASE IF EXISTS ${PG_TEST_DB};" >/dev/null 2>&1 || true
$CLI exec "$PG_CONTAINER" psql -U narrafork -d narrafork -c "CREATE DATABASE ${PG_TEST_DB};" >/dev/null 2>&1 || true

echo ">> Running dual-dialect tests (SQLite + PostgreSQL)"
NARRAFORK_TEST_PG_DSN="$PG_DSN" go test ./... "$@"
