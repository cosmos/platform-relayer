#!/bin/sh

source ./scripts/db/creds.sh

export PGUSER=$(echo $DB_CREDS | jq -r .data.username)
export PGDATABASE="$DB_NAME"
export PGHOST="$DB_HOST"
export PGPORT="$DB_PORT"

# postgres login
echo ">"
echo "> Connecting to $ENVIRONMENT-db-$DB_LEVEL ($PGHOST:$PGPORT)"
echo ">"
echo
PGPASSWORD=$(echo $DB_CREDS | jq -r .data.password) psql
