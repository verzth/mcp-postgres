# mcp-postgres

A Model Context Protocol (MCP) server for PostgreSQL that supports **multiple hosts**, **auto-discovery of databases**, and **granular per-database/table access control**. Built for AI agents working with microservice architectures.

## Features

- **Multi-host support** — connect to multiple PostgreSQL hosts from a single config
- **Auto-discovery** — automatically discovers all databases on each host (no need to define them one by one)
- **Granular access control** — per-database and per-table permissions (none/read/write/admin) via YAML config
- **Mandatory dry-run** — all write/DDL operations must be previewed before execution
- **Backward compatible** — single-host config format still works

## Installation

### From source

```bash
go install github.com/verzth/mcp-postgres/cmd/mcp-postgres@latest
```

### Build from repo

```bash
git clone https://github.com/verzth/mcp-postgres.git
cd mcp-postgres
go build -o mcp-postgres ./cmd/mcp-postgres/
```

## Configuration

Copy the example config and edit it:

```bash
cp config.example.yaml config.yaml
```

### Multi-host config (recommended)

```yaml
defaults:
  access: read

dry_run:
  timeout_seconds: 300

hosts:
  prod-users:
    connection:
      host: users-db.internal
      port: 5432
      user: readonly
      password: secret1
      sslmode: require
    databases:
      users_db:
        access: write
        tables:
          sensitive_users:
            access: read

  prod-orders:
    connection:
      host: orders-db.internal
      port: 5432
      user: admin_user
      password: secret2
      sslmode: require
    databases:
      orders_db:
        access: admin

  staging:
    connection:
      host: staging-db.internal
      port: 5432
      user: dev
      password: devpass
```

### Single-host config (backward compatible)

```yaml
defaults:
  access: read

dry_run:
  timeout_seconds: 300

connection:
  host: localhost
  port: 5432
  user: postgres
  password: secret
  sslmode: disable

databases:
  users_db:
    access: write
  orders_db:
    access: admin
  internal_db:
    access: none
```

### Connection options

| Field | Default | Notes |
|-------|---------|-------|
| `host` | required | hostname or IP |
| `port` | `5432` | PostgreSQL default |
| `user` | required | postgres user |
| `password` |  | postgres password |
| `sslmode` | `disable` | `disable` \| `require` \| `verify-ca` \| `verify-full` |
| `default_database` | `postgres` | DB used for cross-database discovery (must exist & be reachable by the user) |

### Access Levels

| Level | Permissions |
|-------|------------|
| `none` | Database is completely hidden from the agent |
| `read` | SELECT queries only |
| `write` | SELECT + INSERT/UPDATE/DELETE (via dry-run) |
| `admin` | All above + DDL: ALTER/CREATE/DROP (via dry-run) |

Resolution order: **table override > database config > defaults.access**

## Usage with Claude Desktop

Add to your Claude Desktop config (`~/Library/Application Support/Claude/claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "postgres": {
      "command": "/path/to/mcp-postgres",
      "args": ["-config", "/path/to/config.yaml"]
    }
  }
}
```

Or if installed via `go install`:

```json
{
  "mcpServers": {
    "postgres": {
      "command": "mcp-postgres",
      "args": ["-config", "/path/to/config.yaml"]
    }
  }
}
```

### Environment variable

You can also set the config path via environment variable:

```bash
export MCP_POSTGRES_CONFIG=/path/to/config.yaml
```

## MCP Tools

| Tool | Description |
|------|-------------|
| `list_hosts` | List all configured PostgreSQL hosts |
| `list_databases` | List accessible databases on a host with access levels |
| `list_tables` | List tables in a database (optionally filtered by schema) |
| `describe_table` | Show columns, indexes, foreign keys, and constraints (defaults to `public` schema) |
| `query` | Execute SELECT/WITH queries (auto-limited to 1000 rows) |
| `explain_query` | Run EXPLAIN on any query |
| `dry_run` | Preview a write/DDL query without executing (returns `dry_run_id`) |
| `confirm_execute` | Execute a previously previewed query by `dry_run_id` |

All tools accept an optional `host` parameter. If omitted:
- Auto-selects if only one host is configured
- Falls back to host named `"default"` (used by single-host config)

`list_tables` and `describe_table` accept an optional `schema` parameter (PostgreSQL schemas like `public`, `audit`, etc.). `describe_table` defaults to `public`; `list_tables` defaults to all user schemas.

### Write operation flow

All write operations are **mandatory two-step**:

```
1. dry_run(host, database, sql)    → returns dry_run_id + EXPLAIN plan + estimated rows
2. confirm_execute(dry_run_id)     → actually executes the query
```

Dry-run IDs expire after the configured timeout (default: 5 minutes).

## MCP Resources

| Resource | Description |
|----------|-------------|
| `postgres://hosts` | List of all configured hosts |
| `postgres://{host}/databases` | Databases on a specific host |
| `postgres://{host}/{database}/schema` | All tables grouped by schema with column metadata |

## Architecture

```
cmd/mcp-postgres/main.go         # Entry point
internal/config/config.go        # YAML config parsing + backward compat
internal/postgres/pool.go        # PoolManager (multi-host, per-database connection pools)
internal/access/checker.go       # Per-host permission resolution + SQL classification
internal/dryrun/store.go         # In-memory dry-run state with TTL
internal/tools/                  # MCP tool implementations
internal/resources/              # MCP resource implementations
```

Key design decisions:
- **One `*sql.DB` per (host, database)** — PostgreSQL connections are bound to a database at connect time, so the pool lazily opens a new `*sql.DB` per database. The `default_database` (default: `postgres`) is used for cross-DB queries like `pg_database`.
- **SQL classification via regex** — lightweight prefix matching for access control with support for double-quoted PostgreSQL identifiers and schema-qualified names (`schema.table`). Not a security boundary; PostgreSQL `GRANT`/`REVOKE` are the real enforcement.
- **In-memory dry-run store** — sufficient for single-process stdio MCP server; store lifetime matches the session.
- **EXPLAIN-based row estimation** — `dry_run` parses the first `rows=N` hint from the EXPLAIN output for write statements; DDL statements skip EXPLAIN.

## License

MIT
