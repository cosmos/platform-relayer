#!/bin/sh

# check dependencies
if !(command -v vault > /dev/null 2>&1); then echo "vault must be installed"; exit 1; fi
if !(command -v jq > /dev/null 2>&1); then echo "jq must be installed"; exit 1; fi

# read credentials from vault
if [[ -z "$ENVIRONMENT" ]]; then ENVIRONMENT="local"; fi # local, dev, prod
if [[ -z "$DB_LEVEL" ]]; then DB_LEVEL="reader"; fi # reader, writer

if [[ $ENVIRONMENT != "local" ]]; then
    # authenticate with vault
    vault token lookup > /dev/null 2>&1 || vault login --method oidc
    DB_CREDS=$(vault read -format json -ns admin/engineering postgres/creds/skip-solve-$ENVIRONMENT-db-$DB_LEVEL)
else
    DB_LEVEL="writer" # there is no reader in local
    DB_CREDS='{"data": {"username": "relayer", "password": "relayer"}}'
fi

# postgres environment variables
if [[ -z "$DB_NAME" ]]; then DB_NAME="relayer"; fi
if [[ -z "$DB_HOST" ]]; then
    DEFAULT_DEV_HOST="skip-solve-dev-db.cjnjsixwfjz7.us-east-2.rds.amazonaws.com"
    DEFAULT_PROD_HOST="skip-solve-db.cjnjsixwfjz7.us-east-2.rds.amazonaws.com"
    case $ENVIRONMENT in
        prod)
            DB_HOST="$DEFAULT_PROD_HOST"
            ;;
        dev)
            DB_HOST="$DEFAULT_DEV_HOST"
            ;;
        *)
            DB_HOST="localhost"
            ;;
    esac
fi

if [[ -z "$DB_PORT" ]]; then
    case $ENVIRONMENT in
        local)
            DB_PORT="42500"
            ;;
        *)
            DB_PORT="5432"
            ;;
    esac
fi
