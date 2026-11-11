package connector

import (
	"context"

	"github.com/nwverse/persona/sdk/go/wire"
)

// Catalog is the connector's subscribable read-only sources, for Persona's source subscriptions (connector.catalog.list
// and connector.catalog.call; the host's docs/extensions/subscriptions.md). Without it the hub derives one source from
// the read tools; with it, the catalog says which tools there are and answers their calls, so a read tool can answer
// subscriptions with JSON while the assistant's tools.call keeps its text.
type Catalog interface {
	Catalog(context.Context) ([]CatalogSource, error)
	// CatalogCall runs one of the catalog's tools; a source or tool not in the catalog is refused (not_read_only).
	CatalogCall(context.Context, CatalogQuery) (string, error)
}

type (
	CatalogSource = wire.ConnectorCatalogSource
	CatalogTool   = wire.ConnectorCatalogTool
	CatalogQuery  = wire.ConnectorCatalogCallParams
)
