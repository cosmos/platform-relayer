# Relayer

IBC packet-first relayer for Cosmos/EVM chains. See [root AGENTS.md](../../AGENTS.md) for shared conventions.

## Note

This directory has **relaxed linting** (excluded from golangci-lint).

## Structure

| Directory | Purpose |
|-----------|---------|
| `cmd/relayer/` | Main service entry point |
| `cmd/relay/` | Manual relay CLI tool |
| `cmd/transfer/` | Cross-chain transfer tool |
| `relayer/ibcv2/` | Core relay pipeline, processors |
| `relayerapi/` | gRPC API service |
| `shared/` | Bridges, signing, contracts, utils |
| `db/` | Migrations, sqlc queries |
| `gasmonitor/` | Chain gas balance monitoring |
| `proto/` | Relayer-specific protobufs |
| `mocks/` | Generated mocks (mockery) |

## Pipeline Architecture

`relayer/ibcv2/` implements a processing pipeline:
```
Transaction → Check Packet → Batch → Sign → Submit → Verify
```

Key processors:
- `check_packet_commitment.go` - Verify packet exists
- `batch_recv_packet.go` - Batch for gas efficiency
- `retry_*.go` - Retry logic per stage
- `pipeline.go` - Pipeline orchestration

## Where to Look

| Task | Location |
|------|----------|
| Add relay processor | `relayer/ibcv2/` |
| Modify gRPC API | `relayerapi/services/` + `proto/` |
| Add contract binding | `shared/contracts/` |
| Add chain bridge | `shared/bridges/ibcv2/` |
| Add migration | `db/migrations/` |

## Local Development

Run `docker compose up -d && make relayer-local` (or `docker-compose up -d && make relayer-local` if you are using the legacy Docker Compose binary) to start Postgres and the relayer.
