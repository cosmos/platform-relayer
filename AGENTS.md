# Eureka Relayer

IBC packet-first relayer for Cosmos/EVM chains. See [root AGENTS.md](../../AGENTS.md) for shared conventions.

## Note

This directory has **relaxed linting** (excluded from golangci-lint).

## Local Development

Run `docker compose up -d && make relayer-local` (or `docker-compose up -d && make relayer-local` if you are using the legacy Docker Compose binary) to start Postgres and the relayer.
