#!/bin/sh

DB_LEVEL="writer"
source ./scripts/db/creds.sh

set -x
docker run \
    -it \
    --rm \
    -e PGUSER=$(echo $DB_CREDS | jq -r .data.username) \
    -e PGPASSWORD=$(echo $DB_CREDS | jq -r .data.password) \
    -v ./db/migrations:/migrations \
    migrate/migrate \
    -path=/migrations/ \
    -database postgres://$DB_HOST:$DB_PORT/$DB_NAME?sslmode=disable \
    "$@"
