package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/wechat/internal/wechat"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

func TestPublishedManifestMatchesRuntime(t *testing.T) {
	var m wire.Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != wechat.ID || m.Version != program().Version || version != "0.1.0" {
		t.Fatal("manifest/runtime identity mismatch")
	}
	if !reflect.DeepEqual(m.Contributes.Connectors, []wire.ConnectorItem{wechat.Item()}) || !reflect.DeepEqual(m.Contributes.Channel, []wire.ChannelItem{wechat.Channel()}) {
		t.Fatal("manifest/runtime contributions differ")
	}
	if !reflect.DeepEqual(m.Permissions.Network, wechat.Network) {
		t.Fatal("manifest/runtime network permissions differ")
	}
	exttest.CheckConnectors(t, manifest, exttest.Serve(t, program()))
	exttest.CheckChannels(t, manifest, exttest.Serve(t, program()))
}
