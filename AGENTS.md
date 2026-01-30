# Eureka Relayer

IBC packet-first relayer. Discovers packets from transactions and relays across Cosmos/EVM chains.

**Note**: This directory has relaxed linting (excluded from golangci-lint).

## Structure

| Directory | Purpose |
|-----------|---------|
| `cmd/relayer/` | Main service entry point |
| `cmd/relay/` | Manual relay CLI tool |
| `cmd/transfer/` | Cross-chain transfer tool |
| `relayer/eureka/` | Core relay pipeline, processors |
| `relayerapi/` | gRPC API service |
| `shared/` | Bridges, signing, contracts, utils |
| `db/` | Migrations, sqlc queries |
| `gasmonitor/` | Chain gas balance monitoring |
| `proto/` | Relayer-specific protobufs |
| `mocks/` | Generated mocks (mockery) |

## Pipeline Architecture

`relayer/eureka/` implements a processing pipeline:
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
| Add relay processor | `relayer/eureka/` |
| Modify gRPC API | `relayerapi/services/` + `proto/` |
| Add contract binding | `shared/contracts/` |
| Add chain bridge | `shared/bridges/eureka/` |
| Add migration | `db/migrations/` |

## Local Development

```bash
# Start Postgres
docker-compose up -d

# Run relayer
make relayer-local
```

## Build

```bash
make build-local-image-relayer  # platform-relayer:local
```

## Code Generation

```bash
make generate-proto  # Includes relayer protos
```

Contracts in `shared/contracts/` are generated via abigen (Ethereum).
