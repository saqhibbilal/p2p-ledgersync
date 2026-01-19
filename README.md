# ledger-sync

A mini distributed-ledger synchronization project for learning how blockchain-style nodes keep data in sync without a central server. Each node maintains its own copy of a simple block chain (height, hashes, payload) and uses a gossip-style protocol to learn what peers have. When a node is behind, it requests only the missing blocks and applies them locally.

### Architecture (simple)
- **Node process (`cmd/node`)**: runs a gRPC server and connects to peers.
  - **Gossip summaries**: nodes exchange “tip” summaries (latest height + hash).
  - **Targeted fetch**: if a peer is ahead, the node streams the missing blocks via `GetBlocks`.
  - **Peer discovery**: nodes share peer lists so new nodes can find the rest of the network.
  - **Observer API**: nodes expose a read-only event stream for dashboards.
- **Observer/dashboard (`cmd/observer`)**: connects to nodes, aggregates state (nodes, edges, tip/lag, blocks sent/received), and serves a web UI with live updates (SSE).

### Prerequisites
- Go installed (1.22+ recommended), or Docker Desktop.

### Run (Windows / PowerShell)
From the repo root:

```powershell
go test ./...

go run ./cmd/node -id n1 -listen 127.0.0.1:50051 -init-blocks 25
go run ./cmd/node -id n2 -listen 127.0.0.1:50052 -seeds 127.0.0.1:50051
go run ./cmd/node -id n3 -listen 127.0.0.1:50053 -seeds 127.0.0.1:50052

go run ./cmd/observer -http 127.0.0.1:8080 -seeds 127.0.0.1:50051
```

Open:
- `http://127.0.0.1:8080`

### Run (Docker Compose)
From the repo root:

```powershell
docker compose up --build
```

Open:
- `http://localhost:8080`

Stop:

```powershell
docker compose down
```

### Protobuf codegen (optional)
Generated stubs are already in `gen/go/`. If you edit `proto/`, regenerate with Buf:

```powershell
go install github.com/bufbuild/buf/cmd/buf@latest
buf generate
```

