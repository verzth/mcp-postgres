package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sort"
	"sync"

	_ "github.com/lib/pq"
	"github.com/verzth/mcp-postgres/internal/config"
)

// systemDatabases are excluded from DiscoverDatabases.
var systemDatabases = map[string]bool{
	"template0": true,
	"template1": true,
}

// Pool represents a single PostgreSQL host. Because each Postgres connection is
// bound to a specific database at connect time, Pool keeps one *sql.DB per
// database name, lazily opened on first use.
type Pool struct {
	cfg config.ConnectionConfig

	mu  sync.Mutex
	dbs map[string]*sql.DB // database name -> connection pool
}

func newPool(cfg config.ConnectionConfig) (*Pool, error) {
	p := &Pool{cfg: cfg, dbs: make(map[string]*sql.DB)}

	// Open a connection to the default database to verify reachability.
	if _, err := p.getDB(cfg.DefaultDatabase); err != nil {
		return nil, fmt.Errorf("open default database: %w", err)
	}

	return p, nil
}

// getDB returns the cached *sql.DB for the given database, opening one if needed.
// A failed ping invalidates and reopens the pool.
func (p *Pool) getDB(database string) (*sql.DB, error) {
	if database == "" {
		database = p.cfg.DefaultDatabase
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if db, ok := p.dbs[database]; ok {
		if err := db.Ping(); err == nil {
			return db, nil
		}
		_ = db.Close()
		delete(p.dbs, database)
	}

	db, err := sql.Open("postgres", p.cfg.DSN(database))
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres database %q: %w", database, err)
	}

	p.dbs[database] = db
	return db, nil
}

func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var firstErr error
	for name, db := range p.dbs {
		if err := db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(p.dbs, name)
	}
	return firstErr
}

// DiscoverDatabases lists all non-system, non-template databases on the host.
// It uses the default database for the query.
func (p *Pool) DiscoverDatabases(ctx context.Context) ([]string, error) {
	db, err := p.getDB(p.cfg.DefaultDatabase)
	if err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx,
		"SELECT datname FROM pg_database WHERE datistemplate = false AND datallowconn = true ORDER BY datname")
	if err != nil {
		return nil, fmt.Errorf("list databases: %w", err)
	}
	defer rows.Close()

	var databases []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan database: %w", err)
		}
		if !systemDatabases[name] {
			databases = append(databases, name)
		}
	}
	return databases, rows.Err()
}

// QueryContext runs a query against the given database and returns results as a
// slice of column-name -> value maps. Byte slices are converted to strings for
// JSON-friendliness.
func (p *Pool) QueryContext(ctx context.Context, database, query string, args ...any) ([]map[string]any, error) {
	db, err := p.getDB(database)
	if err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var result []map[string]any
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}

		row := make(map[string]any, len(cols))
		for i, col := range cols {
			val := values[i]
			if b, ok := val.([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = val
			}
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// ExecContext executes a statement against the given database.
func (p *Pool) ExecContext(ctx context.Context, database, query string, args ...any) (sql.Result, error) {
	db, err := p.getDB(database)
	if err != nil {
		return nil, err
	}
	return db.ExecContext(ctx, query, args...)
}

// hostEntry tracks connection state for a single host.
type hostEntry struct {
	cfg       config.ConnectionConfig
	pool      *Pool
	lastError error
}

// PoolManager manages multiple named PostgreSQL host pools with lazy reconnection.
// Hosts that fail to connect at startup are not fatal — they will be retried
// on the next Get() call.
type PoolManager struct {
	mu    sync.Mutex
	hosts map[string]*hostEntry
}

// NewManager creates a PoolManager. It attempts to connect to all hosts but
// does NOT fail if some are unreachable — those will be retried lazily.
// Only returns an error if zero hosts are configured.
func NewManager(hosts map[string]config.HostConfig) (*PoolManager, error) {
	if len(hosts) == 0 {
		return nil, fmt.Errorf("no hosts configured")
	}

	entries := make(map[string]*hostEntry, len(hosts))
	var connected, failed int

	for name, host := range hosts {
		entry := &hostEntry{cfg: host.Connection}
		pool, err := newPool(host.Connection)
		if err != nil {
			entry.lastError = err
			failed++
			log.Printf("[mcp-postgres] host %q: connection failed (will retry on use): %v", name, err)
		} else {
			entry.pool = pool
			connected++
		}
		entries[name] = entry
	}

	log.Printf("[mcp-postgres] %d/%d hosts connected, %d failed (will retry on use)", connected, connected+failed, failed)

	return &PoolManager{hosts: entries}, nil
}

// Get returns the pool for a host. If the host previously failed to connect,
// it attempts to reconnect. Returns an error only if the host is unknown or
// the connection (re)attempt fails.
func (m *PoolManager) Get(hostName string) (*Pool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.hosts[hostName]
	if !ok {
		return nil, fmt.Errorf("host %q not found, available hosts: %v", hostName, m.hostNamesLocked())
	}

	if entry.pool != nil {
		return entry.pool, nil
	}

	pool, err := newPool(entry.cfg)
	if err != nil {
		entry.lastError = err
		return nil, fmt.Errorf("host %q is not available: %w", hostName, err)
	}

	entry.pool = pool
	entry.lastError = nil
	log.Printf("[mcp-postgres] host %q: reconnected successfully", hostName)
	return pool, nil
}

// HostNames returns all configured host names (including disconnected ones).
func (m *PoolManager) HostNames() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hostNamesLocked()
}

func (m *PoolManager) hostNamesLocked() []string {
	names := make([]string, 0, len(m.hosts))
	for name := range m.hosts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// HostStatus returns the connection status of a host.
func (m *PoolManager) HostStatus(hostName string) (connected bool, lastErr error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.hosts[hostName]
	if !ok {
		return false, fmt.Errorf("host %q not found", hostName)
	}
	return entry.pool != nil, entry.lastError
}

func (m *PoolManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, entry := range m.hosts {
		if entry.pool != nil {
			_ = entry.pool.Close()
		}
	}
}
