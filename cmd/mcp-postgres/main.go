package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/verzth/mcp-postgres/internal/access"
	"github.com/verzth/mcp-postgres/internal/config"
	"github.com/verzth/mcp-postgres/internal/dryrun"
	pgpool "github.com/verzth/mcp-postgres/internal/postgres"
	"github.com/verzth/mcp-postgres/internal/resources"
	"github.com/verzth/mcp-postgres/internal/tools"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	if envPath := os.Getenv("MCP_POSTGRES_CONFIG"); envPath != "" {
		*configPath = envPath
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	pools, err := pgpool.NewManager(cfg.Hosts)
	if err != nil {
		log.Fatalf("connect to postgres: %v", err)
	}
	defer pools.Close()

	checker := access.New(cfg)
	dryrunStore := dryrun.NewStore(time.Duration(cfg.DryRun.TimeoutSeconds) * time.Second)

	server := mcp.NewServer(
		&mcp.Implementation{Name: "mcp-postgres", Version: "0.1.0"},
		nil,
	)

	toolDeps := &tools.Deps{
		Pools:   pools,
		Access:  checker,
		DryRuns: dryrunStore,
		Config:  cfg,
	}
	tools.RegisterAll(server, toolDeps)

	resDeps := &resources.Deps{
		Pools:  pools,
		Access: checker,
		Config: cfg,
	}
	resources.RegisterAll(server, resDeps)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
