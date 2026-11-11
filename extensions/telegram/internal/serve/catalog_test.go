package serve

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/telegram/internal/connector"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// catalogConn is a connector whose catalog has one read-only tool and one that is not.
type catalogConn struct{ calls []connector.CatalogQuery }

func (*catalogConn) ID() string   { return "example" }
func (*catalogConn) Name() string { return "Example" }
func (*catalogConn) Manifest() connector.Manifest {
	return connector.Manifest{Capabilities: []connector.Capability{connector.CapCatalog}}
}
func (*catalogConn) Catalog(context.Context) ([]connector.CatalogSource, error) {
	return []connector.CatalogSource{{ID: "example", Name: "Example", Tools: []connector.CatalogTool{
		{Name: "list", Description: "List items", InputSchema: json.RawMessage(`{"type":"object"}`), ReadOnly: true}}}}, nil
}
func (c *catalogConn) CatalogCall(_ context.Context, q connector.CatalogQuery) (string, error) {
	c.calls = append(c.calls, q)
	return `{"items":[]}`, nil
}

// catalog.list answers the connector's checked catalog; catalog.call runs only a tool it lists, with {} for no args.
func TestCatalogMethods(t *testing.T) {
	ctx := context.Background()
	c := &catalogConn{}
	s := &server{c: c}
	out, err := s.handle(ctx, MCatalog, json.RawMessage(`{"contribution":"example"}`))
	if got, ok := out.(wire.ConnectorCatalog); err != nil || !ok || len(got.Sources) != 1 || got.Sources[0].Tools[0].Name != "list" {
		t.Fatalf("catalog.list %+v %v", out, err)
	}
	out, err = s.handle(ctx, MCatalogCall, json.RawMessage(`{"contribution":"example","source":"example","tool":"list"}`))
	if r, ok := out.(CallResult); err != nil || !ok || r.Text != `{"items":[]}` || len(c.calls) != 1 || string(c.calls[0].Args) != "{}" {
		t.Fatalf("catalog.call %+v %v %+v", out, err, c.calls)
	}
	for _, bad := range []string{`{"source":"example","tool":"send"}`, `{"source":"other","tool":"list"}`} {
		var e *ext.Error
		if _, err := s.handle(ctx, MCatalogCall, json.RawMessage(bad)); !errors.As(err, &e) || e.Code != wire.ConnectorErrorCodeNotReadOnly {
			t.Errorf("%s: %v", bad, err)
		}
	}
	if len(c.calls) != 1 {
		t.Fatalf("a refused call reached the connector: %+v", c.calls)
	}
}
