package tools

import (
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/verzth/mcp-postgres/internal/access"
	"github.com/verzth/mcp-postgres/internal/config"
	"github.com/verzth/mcp-postgres/internal/dryrun"
	pgpool "github.com/verzth/mcp-postgres/internal/postgres"
)

type Deps struct {
	Pools   *pgpool.PoolManager
	Access  *access.Checker
	DryRuns *dryrun.Store
	Config  *config.Config
}

func ptrBool(b bool) *bool { return &b }

// resolveHost resolves host name to pool and checker.
// If host is empty: use "default" if exists, or auto-select if only one host.
func resolveHost(deps *Deps, host string) (*pgpool.Pool, *access.HostChecker, string, error) {
	if host == "" {
		names := deps.Pools.HostNames()
		if len(names) == 1 {
			host = names[0]
		} else {
			host = "default"
		}
	}

	pool, err := deps.Pools.Get(host)
	if err != nil {
		return nil, nil, "", fmt.Errorf("resolve host: %w", err)
	}

	checker, err := deps.Access.ForHost(host)
	if err != nil {
		return nil, nil, "", fmt.Errorf("resolve host access: %w", err)
	}

	return pool, checker, host, nil
}

func RegisterAll(server *mcp.Server, deps *Deps) {
	registerListHosts(server, deps)
	registerListDatabases(server, deps)
	registerListTables(server, deps)
	registerDescribeTable(server, deps)
	registerQuery(server, deps)
	registerExplainQuery(server, deps)
	registerDryRun(server, deps)
	registerConfirmExecute(server, deps)
}
