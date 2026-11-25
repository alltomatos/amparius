#!/bin/sh
set -eu

cd "$(dirname "$0")"
set -a
. ./.env
set +a

compose() {
  docker compose --env-file .env "$@"
}

compose up -d postgres redis rabbitmq minio
until compose exec -T postgres pg_isready -U "$POSTGRES_ADMIN_USER" -d amparius >/dev/null 2>&1; do
  sleep 2
done

compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_ADMIN_USER" -d amparius <<SQL
DO \$do\$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '$POSTGRES_APP_USER') THEN
    CREATE ROLE $POSTGRES_APP_USER LOGIN PASSWORD '$POSTGRES_APP_PASSWORD' NOSUPERUSER NOBYPASSRLS;
  END IF;
END
\$do\$;
SQL

docker run --rm --network staging_default \
  -v "$(cd ../.. && pwd)/migrations:/migrations:ro" \
  golang:1.24-alpine sh -c \
  "go run github.com/pressly/goose/v3/cmd/goose@v3.24.3 -dir /migrations postgres 'postgres://$POSTGRES_ADMIN_USER:$POSTGRES_ADMIN_PASSWORD@postgres:5432/amparius?sslmode=disable' up"

docker run --rm --network staging_default \
  -v "$(cd ../.. && pwd)/migrations/control:/migrations:ro" \
  golang:1.24-alpine sh -c \
  "go run github.com/pressly/goose/v3/cmd/goose@v3.24.3 -dir /migrations postgres 'postgres://$POSTGRES_ADMIN_USER:$POSTGRES_ADMIN_PASSWORD@postgres:5432/amparius_control?sslmode=disable' up"

compose build api
compose up -d api
compose ps
