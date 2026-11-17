package maintenance

import (
	"context"
	"database/sql"
	"iter"
	"time"

	"gatewaylab/internal/maintenance/sandbox"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Catalog serializes publication with routing configuration checks. The commit
// callback persists both registry and maintenance run before publishing a snapshot.
type Catalog interface {
	Snapshot() Registry
	UpdateCatalog(serverID, routingFingerprint string, update func(Registry) (Registry, error), commit func(Registry) error) error
}

type RoutingResult struct {
	Status string
	Tools  []string
}

type Router interface {
	RoutingFingerprint(serverID string) string
	Discover(context.Context, RoutingCase, Registry) (RoutingResult, error)
}

type Upstream interface {
	NativeTools(context.Context, Server) (iter.Seq2[*mcp.Tool, error], func(), error)
	CallMCP(context.Context, Server, Tool, map[string]any) (any, error)
	CallHTTP(context.Context, Server, Tool, map[string]any) (any, error)
	RedactResponse(any, Server) any
}

type Recorder interface {
	ErrorDetail(error) string
	Record(kind, actor, status, title string, start time.Time, detail any)
}

type OpenAPIImporter func(string, []byte, []string) ([]Tool, error)

type Options struct {
	DB            *sql.DB
	Dir           string
	Catalog       Catalog
	Router        Router
	Upstream      Upstream
	Recorder      Recorder
	Execution     sandbox.Policy
	ImportOpenAPI OpenAPIImporter
	LookupEnv     func(string) string
}
