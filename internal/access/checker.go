package access

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/verzth/mcp-postgres/internal/config"
)

type Level int

const (
	None Level = iota
	Read
	Write
	Admin
)

func (l Level) String() string {
	switch l {
	case None:
		return "none"
	case Read:
		return "read"
	case Write:
		return "write"
	case Admin:
		return "admin"
	default:
		return "unknown"
	}
}

func ParseLevel(s string) Level {
	switch strings.ToLower(s) {
	case "read":
		return Read
	case "write":
		return Write
	case "admin":
		return Admin
	case "none":
		return None
	default:
		return None
	}
}

// HostChecker resolves permissions for a single host.
type HostChecker struct {
	defaultAccess Level
	databases     map[string]config.DatabaseConfig
}

func (c *HostChecker) DatabaseAccess(database string) Level {
	if db, ok := c.databases[database]; ok {
		return ParseLevel(db.Access)
	}
	return c.defaultAccess
}

func (c *HostChecker) TableAccess(database, table string) Level {
	if db, ok := c.databases[database]; ok {
		if tbl, ok := db.Tables[table]; ok {
			return ParseLevel(tbl.Access)
		}
		return ParseLevel(db.Access)
	}
	return c.defaultAccess
}

func (c *HostChecker) CanRead(database, table string) bool {
	return c.TableAccess(database, table) >= Read
}

func (c *HostChecker) CanWrite(database, table string) bool {
	return c.TableAccess(database, table) >= Write
}

func (c *HostChecker) CanAdmin(database, table string) bool {
	return c.TableAccess(database, table) >= Admin
}

func (c *HostChecker) IsVisible(database string) bool {
	return c.DatabaseAccess(database) != None
}

func (c *HostChecker) FilterDatabases(all []string) []string {
	var filtered []string
	for _, db := range all {
		if c.IsVisible(db) {
			filtered = append(filtered, db)
		}
	}
	return filtered
}

// Checker manages access control across multiple hosts.
type Checker struct {
	hosts map[string]*HostChecker
}

func New(cfg *config.Config) *Checker {
	defaultLevel := ParseLevel(cfg.Defaults.Access)
	hosts := make(map[string]*HostChecker, len(cfg.Hosts))
	for name, host := range cfg.Hosts {
		hosts[name] = &HostChecker{
			defaultAccess: defaultLevel,
			databases:     host.Databases,
		}
	}
	return &Checker{hosts: hosts}
}

func (c *Checker) ForHost(host string) (*HostChecker, error) {
	hc, ok := c.hosts[host]
	if !ok {
		return nil, fmt.Errorf("host %q not found in access config", host)
	}
	return hc, nil
}

// SQL classification

type SQLKind string

const (
	KindSelect SQLKind = "SELECT"
	KindInsert SQLKind = "INSERT"
	KindUpdate SQLKind = "UPDATE"
	KindDelete SQLKind = "DELETE"
	KindAlter  SQLKind = "ALTER"
	KindCreate SQLKind = "CREATE"
	KindDrop   SQLKind = "DROP"
	KindOther  SQLKind = "OTHER"
)

func (k SQLKind) IsRead() bool {
	return k == KindSelect
}

func (k SQLKind) IsWrite() bool {
	return k == KindInsert || k == KindUpdate || k == KindDelete
}

func (k SQLKind) IsDDL() bool {
	return k == KindAlter || k == KindCreate || k == KindDrop
}

type SQLClassification struct {
	Kind   SQLKind
	Tables []string
}

// Identifier patterns: PostgreSQL identifiers may be unquoted (\w+) or
// double-quoted ("name"). Schema-qualified names like "schema"."table" or
// schema.table are reduced to the final table component.
const identPat = `(?:"([^"]+)"|(\w+))`

var (
	reFrom        = regexp.MustCompile(`(?i)\bFROM\s+(?:` + identPat + `\s*\.\s*)?` + identPat)
	reJoin        = regexp.MustCompile(`(?i)\bJOIN\s+(?:` + identPat + `\s*\.\s*)?` + identPat)
	reInto        = regexp.MustCompile(`(?i)\bINTO\s+(?:` + identPat + `\s*\.\s*)?` + identPat)
	reUpdate      = regexp.MustCompile(`(?i)\bUPDATE\s+(?:` + identPat + `\s*\.\s*)?` + identPat)
	reAlterTable  = regexp.MustCompile(`(?i)\bALTER\s+TABLE\s+(?:IF\s+EXISTS\s+)?(?:ONLY\s+)?(?:` + identPat + `\s*\.\s*)?` + identPat)
	reCreateTable = regexp.MustCompile(`(?i)\bCREATE\s+(?:UNIQUE\s+)?(?:TABLE|INDEX)\s+(?:IF\s+NOT\s+EXISTS\s+)?(?:` + identPat + `\s*\.\s*)?` + identPat)
	reDropTable   = regexp.MustCompile(`(?i)\bDROP\s+(?:TABLE|INDEX)\s+(?:IF\s+EXISTS\s+)?(?:` + identPat + `\s*\.\s*)?` + identPat)
)

// pickIdent returns the captured table name. Each pattern has 4 capture groups
// (quoted-schema, unquoted-schema, quoted-table, unquoted-table). Returns the
// non-empty table capture.
func pickIdent(m []string) string {
	// m[0] = whole match; m[1..2] = schema; m[3..4] = table
	if len(m) < 5 {
		return ""
	}
	if m[3] != "" {
		return m[3]
	}
	return m[4]
}

func ClassifySQL(query string) SQLClassification {
	trimmed := strings.TrimSpace(query)
	upper := strings.ToUpper(trimmed)

	var kind SQLKind
	switch {
	case strings.HasPrefix(upper, "SELECT"), strings.HasPrefix(upper, "WITH"):
		kind = KindSelect
	case strings.HasPrefix(upper, "INSERT"):
		kind = KindInsert
	case strings.HasPrefix(upper, "UPDATE"):
		kind = KindUpdate
	case strings.HasPrefix(upper, "DELETE"):
		kind = KindDelete
	case strings.HasPrefix(upper, "ALTER"):
		kind = KindAlter
	case strings.HasPrefix(upper, "CREATE"):
		kind = KindCreate
	case strings.HasPrefix(upper, "DROP"):
		kind = KindDrop
	default:
		kind = KindOther
	}

	tables := extractTables(trimmed, kind)
	return SQLClassification{Kind: kind, Tables: tables}
}

func extractTables(query string, kind SQLKind) []string {
	seen := map[string]bool{}
	var tables []string

	addMatches := func(re *regexp.Regexp) {
		for _, m := range re.FindAllStringSubmatch(query, -1) {
			name := pickIdent(m)
			if name != "" && !seen[name] {
				seen[name] = true
				tables = append(tables, name)
			}
		}
	}

	switch kind {
	case KindSelect:
		addMatches(reFrom)
		addMatches(reJoin)
	case KindInsert:
		addMatches(reInto)
	case KindUpdate:
		addMatches(reUpdate)
	case KindDelete:
		addMatches(reFrom)
	case KindAlter:
		addMatches(reAlterTable)
	case KindCreate:
		addMatches(reCreateTable)
	case KindDrop:
		addMatches(reDropTable)
	}

	return tables
}
