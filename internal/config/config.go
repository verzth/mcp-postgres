package config

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	// Multi-host config (preferred)
	Hosts map[string]HostConfig `yaml:"hosts"`

	// Single-host config (backward compat — migrated to Hosts["default"] on load)
	Connection ConnectionConfig          `yaml:"connection"`
	Databases  map[string]DatabaseConfig `yaml:"databases"`

	Defaults DefaultsConfig `yaml:"defaults"`
	DryRun   DryRunConfig   `yaml:"dry_run"`
}

type HostConfig struct {
	Connection ConnectionConfig          `yaml:"connection"`
	Databases  map[string]DatabaseConfig `yaml:"databases"`
}

type ConnectionConfig struct {
	Host            string `yaml:"host"`
	Port            int    `yaml:"port"`
	User            string `yaml:"user"`
	Password        string `yaml:"password"`
	SSLMode         string `yaml:"sslmode"`
	DefaultDatabase string `yaml:"default_database"`
}

type DefaultsConfig struct {
	Access string `yaml:"access"`
}

type DatabaseConfig struct {
	Access string                 `yaml:"access"`
	Tables map[string]TableConfig `yaml:"tables"`
}

type TableConfig struct {
	Access string `yaml:"access"`
}

type DryRunConfig struct {
	TimeoutSeconds int `yaml:"timeout_seconds"`
}

// DSN builds a libpq-style key=value connection string for the given database.
// If database is empty, the connection config's DefaultDatabase is used.
func (c *ConnectionConfig) DSN(database string) string {
	if database == "" {
		database = c.DefaultDatabase
	}
	sslmode := c.SSLMode
	if sslmode == "" {
		sslmode = "disable"
	}

	parts := []string{
		"host=" + quoteValue(c.Host),
		fmt.Sprintf("port=%d", c.Port),
		"user=" + quoteValue(c.User),
		"password=" + quoteValue(c.Password),
		"dbname=" + quoteValue(database),
		"sslmode=" + quoteValue(sslmode),
	}
	return strings.Join(parts, " ")
}

// quoteValue wraps a value in single quotes if it contains whitespace or special chars.
// Empty values get an explicit '' so libpq doesn't fall back to env vars.
func quoteValue(v string) string {
	if v == "" {
		return "''"
	}
	if strings.ContainsAny(v, " \t'\\") {
		escaped := strings.ReplaceAll(v, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `'`, `\'`)
		return "'" + escaped + "'"
	}
	return v
}

func (c *Config) HostNames() []string {
	names := make([]string, 0, len(c.Hosts))
	for name := range c.Hosts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	applyDefaults(cfg)
	migrateToMultiHost(cfg)

	if err := validate(cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return cfg, nil
}

// migrateToMultiHost converts single-host config to multi-host format.
// If `hosts` is not set but `connection` is, create a single host named "default".
func migrateToMultiHost(cfg *Config) {
	if len(cfg.Hosts) > 0 {
		return
	}
	if cfg.Connection.Host == "" {
		return
	}
	cfg.Hosts = map[string]HostConfig{
		"default": {
			Connection: cfg.Connection,
			Databases:  cfg.Databases,
		},
	}
}

func applyDefaults(cfg *Config) {
	if cfg.Defaults.Access == "" {
		cfg.Defaults.Access = "read"
	}
	if cfg.DryRun.TimeoutSeconds == 0 {
		cfg.DryRun.TimeoutSeconds = 300
	}

	applyConnDefaults(&cfg.Connection)

	for name, host := range cfg.Hosts {
		applyConnDefaults(&host.Connection)
		cfg.Hosts[name] = host
	}
}

func applyConnDefaults(c *ConnectionConfig) {
	if c.Port == 0 {
		c.Port = 5432
	}
	if c.SSLMode == "" {
		c.SSLMode = "disable"
	}
	if c.DefaultDatabase == "" {
		c.DefaultDatabase = "postgres"
	}
}

var validAccessLevels = map[string]bool{
	"none": true, "read": true, "write": true, "admin": true,
}

func validate(cfg *Config) error {
	if !validAccessLevels[cfg.Defaults.Access] {
		return fmt.Errorf("defaults.access must be one of: none, read, write, admin")
	}

	if len(cfg.Hosts) == 0 {
		return fmt.Errorf("at least one host must be configured (use 'hosts' or 'connection')")
	}

	for hostName, host := range cfg.Hosts {
		if host.Connection.Host == "" {
			return fmt.Errorf("hosts.%s.connection.host is required", hostName)
		}
		if host.Connection.User == "" {
			return fmt.Errorf("hosts.%s.connection.user is required", hostName)
		}
		for dbName, db := range host.Databases {
			if !validAccessLevels[db.Access] {
				return fmt.Errorf("hosts.%s.databases.%s.access must be one of: none, read, write, admin", hostName, dbName)
			}
			for tblName, tbl := range db.Tables {
				if !validAccessLevels[tbl.Access] {
					return fmt.Errorf("hosts.%s.databases.%s.tables.%s.access must be one of: none, read, write, admin", hostName, dbName, tblName)
				}
			}
		}
	}

	return nil
}
