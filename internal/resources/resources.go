package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/verzth/mcp-postgres/internal/access"
	"github.com/verzth/mcp-postgres/internal/config"
	pgpool "github.com/verzth/mcp-postgres/internal/postgres"
)

type Deps struct {
	Pools  *pgpool.PoolManager
	Access *access.Checker
	Config *config.Config
}

func RegisterAll(server *mcp.Server, deps *Deps) {
	registerHostsResource(server, deps)
	registerDatabasesResourceTemplate(server, deps)
	registerSchemaResourceTemplate(server, deps)
}

type HostEntry struct {
	Name string `json:"name"`
	Host string `json:"host"`
	Port int    `json:"port"`
}

type DatabaseEntry struct {
	Name   string `json:"name"`
	Access string `json:"access"`
}

func registerHostsResource(server *mcp.Server, deps *Deps) {
	server.AddResource(&mcp.Resource{
		URI:         "postgres://hosts",
		Name:        "hosts",
		MIMEType:    "application/json",
		Description: "List of all configured PostgreSQL hosts",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		var entries []HostEntry
		for _, name := range deps.Config.HostNames() {
			host := deps.Config.Hosts[name]
			entries = append(entries, HostEntry{
				Name: name,
				Host: host.Connection.Host,
				Port: host.Connection.Port,
			})
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      "postgres://hosts",
					MIMEType: "application/json",
					Text:     string(data),
				},
			},
		}, nil
	})
}

func registerDatabasesResourceTemplate(server *mcp.Server, deps *Deps) {
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "postgres://{host}/databases",
		Name:        "host_databases",
		MIMEType:    "application/json",
		Description: "List of all accessible databases on a PostgreSQL host",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		uri := req.Params.URI
		hostName := strings.TrimPrefix(uri, "postgres://")
		hostName = strings.TrimSuffix(hostName, "/databases")

		pool, err := deps.Pools.Get(hostName)
		if err != nil {
			return nil, fmt.Errorf("host not found: %w", err)
		}

		checker, err := deps.Access.ForHost(hostName)
		if err != nil {
			return nil, fmt.Errorf("access config not found: %w", err)
		}

		allDBs, err := pool.DiscoverDatabases(ctx)
		if err != nil {
			return nil, fmt.Errorf("discover databases: %w", err)
		}

		visible := checker.FilterDatabases(allDBs)
		var entries []DatabaseEntry
		for _, db := range visible {
			entries = append(entries, DatabaseEntry{
				Name:   db,
				Access: checker.DatabaseAccess(db).String(),
			})
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      uri,
					MIMEType: "application/json",
					Text:     string(data),
				},
			},
		}, nil
	})
}

const schemaTablesSQL = `SELECT table_schema, table_name
FROM information_schema.tables
WHERE table_schema NOT IN ('pg_catalog', 'information_schema')
  AND table_schema NOT LIKE 'pg_toast%'
  AND table_schema NOT LIKE 'pg_temp_%'
  AND table_type = 'BASE TABLE'
ORDER BY table_schema, table_name`

const schemaColumnsSQL = `SELECT
  column_name,
  data_type,
  udt_name,
  character_maximum_length,
  is_nullable,
  column_default
FROM information_schema.columns
WHERE table_schema = $1 AND table_name = $2
ORDER BY ordinal_position`

func registerSchemaResourceTemplate(server *mcp.Server, deps *Deps) {
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "postgres://{host}/{database}/schema",
		Name:        "database_schema",
		MIMEType:    "application/json",
		Description: "Full schema (columns per table) for a PostgreSQL database on a specific host",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		uri := req.Params.URI
		// Parse: postgres://<host>/<database>/schema
		trimmed := strings.TrimPrefix(uri, "postgres://")
		trimmed = strings.TrimSuffix(trimmed, "/schema")
		parts := strings.SplitN(trimmed, "/", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid URI format, expected postgres://{host}/{database}/schema")
		}
		hostName, database := parts[0], parts[1]

		pool, err := deps.Pools.Get(hostName)
		if err != nil {
			return nil, fmt.Errorf("host not found: %w", err)
		}

		checker, err := deps.Access.ForHost(hostName)
		if err != nil {
			return nil, fmt.Errorf("access config not found: %w", err)
		}

		if !checker.IsVisible(database) {
			return nil, fmt.Errorf("database %s is not accessible", database)
		}

		tableRows, err := pool.QueryContext(ctx, database, schemaTablesSQL)
		if err != nil {
			return nil, fmt.Errorf("list tables: %w", err)
		}

		schema := make(map[string]map[string][]map[string]any)
		for _, row := range tableRows {
			schemaName := fmt.Sprintf("%v", row["table_schema"])
			tableName := fmt.Sprintf("%v", row["table_name"])
			if !checker.CanRead(database, tableName) {
				continue
			}

			cols, err := pool.QueryContext(ctx, database, schemaColumnsSQL, schemaName, tableName)
			if err != nil {
				continue
			}
			if schema[schemaName] == nil {
				schema[schemaName] = make(map[string][]map[string]any)
			}
			schema[schemaName][tableName] = cols
		}

		data, _ := json.MarshalIndent(schema, "", "  ")
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      uri,
					MIMEType: "application/json",
					Text:     string(data),
				},
			},
		}, nil
	})
}
