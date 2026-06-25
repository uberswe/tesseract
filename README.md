# Tesseract

A **cross-server wireless storage block** for NeoForge (Minecraft 1.21.1), backed by a small
external sync service.

Place a **Tesseract**, and everything you pipe into it is instantly available from any *other*
Tesseract you own — even on a **different server**. There is no GUI: the block is pure automation
(hoppers, Create, pipes, …). Items live in the service, not in the world, so the same inventory is
shared everywhere.

This repo is a self-contained example of the pattern: *one logical inventory, many servers, an
external authority.*

> Running in production on **[gwsmp.com](https://gwsmp.com)**, where it shares storage across the
> network's NeoForge servers.

```
  Server A (mod)            Server B (mod)            Server C (mod)
      │  TCP 7600                │                        │
      └──────────────┬──────────┴────────────┬───────────┘
                     ▼                        ▼
              ┌──────────────── tesseract-service ───────────────┐
              │  in-memory authoritative inventory per owner UUID │
              │  validates every insert/extract, broadcasts diffs │
              └───────────────────────┬──────────────────────────┘
                                      ▼
                                 PostgreSQL   (periodic snapshot persistence)
```

## How it works

### The block (`mod/`)
- `TesseractBlock` / `TesseractBlockEntity` register an **`ItemHandler` capability** only — no
  menu. Automation reads/writes it like any container.
- When placed, the block stores its **owner UUID**. Every Tesseract a given player owns shares a
  single `ItemStackHandler` (`TesseractSyncManager.createSharedHandler`), so they're all the same
  inventory.
- That shared handler is **not** a normal inventory. Its `insertItem` / `extractItem` are overridden
  to talk to the service; a local `authoritativeCache` mirrors the last known state so reads
  (hopper "is there anything to pull?") are instant and don't hit the network.

### The sync flow
Every insert/extract becomes a tiny **batch operation** sent to the service and answered
synchronously (≤ 500 ms):

1. **Insert.** The mod validates locally first (item identity must match the slot, respect the
   item's real max-stack-size so enchanted books/tools can't be over-stacked, respect the global
   item cap), then sends an `INSERT(slot, count, itemNbt)` op.
2. The service applies it against the **authoritative** inventory and replies with a per-op status
   (`ACCEPTED` / `REJECTED_FULL` / `REJECTED_MISMATCH` / …) plus an optional fresh **snapshot**.
3. On `ACCEPTED`, the mod updates its cache and returns the remainder to the hopper. On reject, the
   item bounces. On **timeout**, the mod proceeds *optimistically* (to avoid item loss / retry
   dupes) and lets the next snapshot reconcile any drift.
4. **Extract** is the mirror image: `EXTRACT(slot, count)`, never handing out more than one
   max-stack at a time.

The service also **pushes snapshots** to every other connected server subscribed to that owner, so
a deposit on Server A shows up in the cache on Server B within a tick.

### The wire protocol (`mod/.../TesseractServiceClient.java` ↔ `service/internal/protocol`)
A minimal length-prefixed binary protocol over plain TCP:

```
[ int32 length ][ byte type ][ payload … ]
```

Handshake: client sends `HELLO{serverName, protocolVersion}`, server replies `HELLO_ACK`. Then:

| type            | direction | meaning                                   |
|-----------------|-----------|-------------------------------------------|
| `SUBSCRIBE`     | → service | start receiving this owner's updates      |
| `BATCH_OPS`     | → service | atomic insert/extract ops for an owner    |
| `BATCH_RESULT`  | ← service | per-op statuses + optional snapshot       |
| `INV_REQUEST`   | → service | "send me this owner's current inventory"  |
| `INV_UPDATE`    | ← service | pushed snapshot (someone else changed it) |
| `PING` / `PONG` | both      | keepalive                                 |

Inventory blobs are **gzipped NBT** (`Items` list of stacks). The mod connects with auto-reconnect
and exponential backoff; on reconnect it re-subscribes and re-requests every active owner, so the
service is the single source of truth and the mod holds no database.

### The service (`service/`)
A small Go TCP server:
- Keeps each owner's inventory **in memory** (`internal/inventory/store.go`) — that's the authority.
- Validates and applies batch ops, **broadcasts** resulting snapshots to other subscribers.
- A background `Persister` flushes dirty inventories to **Postgres** every 30 s (one row per owner
  UUID) and reloads on demand. Postgres is just durable storage; correctness lives in memory.
- Exposes a health/readiness endpoint for orchestration.

## Repository layout

```
tesseract/
├── docker-compose.yml          # Postgres + the service, ready to run
├── mod/                        # the NeoForge mod (Gradle, Java 21)
│   └── src/main/java/com/gearworks/tesseract/
│       ├── Tesseract.java                 # @Mod entry point + lifecycle
│       ├── TesseractBlock.java            # the block + ticker
│       ├── TesseractBlockEntity.java      # owner + local inventory + sync hook
│       ├── TesseractRegistration.java     # block/item/BE/creative-tab registration
│       ├── TesseractConfig.java           # service host/port/server-name (+ env overrides)
│       ├── TesseractServiceClient.java    # the binary TCP client
│       └── TesseractSyncManager.java      # the sync-backed shared inventory
└── service/                    # the Go sync service (own module + Dockerfile)
    ├── cmd/tesseract/                     # main
    ├── internal/{protocol,server,inventory,db,health,config}
    └── migrations/0001_init.sql           # the one table it needs
```

## Running it

**1. Start the service + database:**

```bash
docker compose up --build
```

This brings up Postgres (schema auto-applied) and the service on TCP `7600` (health on `8081`).

**2. Build the mod and drop it on your server(s):**

```bash
cd mod && ./gradlew build      # → build/libs/tesseract-1.0.0.jar
```

Put the jar in each server's `mods/` folder and point it at the service. Per server, set either
env vars or `config/tesseract-common.toml`:

```
TESSERACT_SERVICE_HOST=127.0.0.1     # where the service runs
TESSERACT_SERVICE_PORT=7600
TESSERACT_SERVER_NAME=survival-1     # a UNIQUE name per server
```

Craft a Tesseract (diamond blocks + ender chests + nether stars), place it, and pipe items in.
Place another one — on this server or another connected one — and the contents are shared.

## Building from source

- **Mod:** `cd mod && ./gradlew build` (NeoForge 21.1, Java 21).
- **Service:** `cd service && go build ./...` (Go 1.25), or `docker build ./service`.

## Notes & design choices

- **Automation-only by design.** No GUI keeps the example focused on the cross-server data flow;
  add a menu if you want one.
- **Optimistic on timeout.** Under a brief network blip the mod favors *not losing items* and
  reconciles from the next snapshot, rather than blocking the tick.
- **The service is authoritative.** The mod is stateless across restarts; persistence and conflict
  resolution all live in the service, which makes the multi-server story simple to reason about.

## License

MIT.
