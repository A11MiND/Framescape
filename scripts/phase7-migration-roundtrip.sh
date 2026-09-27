#!/usr/bin/env bash
set -euo pipefail

# Disposable local migration rehearsal. The guard intentionally accepts only
# loopback MySQL and a database name containing "migration" or "phase7".
host=${MYSQL_TEST_HOST:-127.0.0.1}
port=${MYSQL_TEST_PORT:-13316}
user=${MYSQL_TEST_USER:-root}
password=${MYSQL_TEST_PASSWORD:-test-only}
db=${MYSQL_TEST_DB:-aigc_phase7_migration}
if [[ "$host" != "127.0.0.1" && "$host" != "localhost" ]] || [[ "$db" != *migration* && "$db" != *phase7* ]]; then
  echo "refusing non-disposable migration target: $host:$port/$db" >&2
  exit 2
fi

mysql_cmd=(mysql -h"$host" -P"$port" -u"$user")
export MYSQL_PWD="$password"
"${mysql_cmd[@]}" -e "DROP DATABASE IF EXISTS \`$db\`; CREATE DATABASE \`$db\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"
cleanup() { "${mysql_cmd[@]}" -e "DROP DATABASE IF EXISTS \`$db\`;" >/dev/null; }
trap cleanup EXIT

export MYSQL_DSN="$user:$password@tcp($host:$port)/$db?parseTime=true&loc=UTC&charset=utf8mb4"
export GOCACHE="${GOCACHE:-/tmp/framescape-go-cache}"
go run ./cmd/migrate -command=up
go run ./cmd/migrate -command=down
go run ./cmd/migrate -command=up
go run ./cmd/migrate -command=down-to -version=22
go run ./cmd/migrate -command=up
go run ./cmd/migrate -command=version
echo "phase7 migration roundtrip: ok"
