package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenamedHookOwnerMarkers(t *testing.T) {
	for _, tc := range []struct {
		command string
		owned   bool
	}{
		{"old hook " + tagFlag, true},
		{"new hook " + renamedTagFlag + " \n", true},
		{"other hook", false},
		{"other hook " + tagFlag + "-other", false},
		{"other hook --persona-extension=unrelated", false},
		{"other hook " + renamedTagFlag + " extra", false},
	} {
		if got := isOurCommand(tc.command); got != tc.owned {
			t.Errorf("marker %q: owned=%v, want %v", tc.command, got, tc.owned)
		}
	}
}

func TestRenamedHookMixedMarkersAreReplacedOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	legacy := "old-binary hook " + tagFlag
	renamed := "new-binary hook " + renamedTagFlag
	original := map[string]any{
		"model": "unchanged",
		"hooks": map[string]any{
			"PermissionRequest": []any{
				map[string]any{"matcher": "Bash", "hooks": []any{
					map[string]any{"type": "command", "command": legacy},
					map[string]any{"type": "command", "command": "other-tool hook"},
				}},
				map[string]any{"matcher": "*", "hooks": []any{
					map[string]any{"type": "command", "command": renamed},
				}},
			},
			"Stop": []any{map[string]any{"hooks": []any{map[string]any{"command": "keep-stop-hook"}}}},
		},
	}
	before, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	command := hookCommand("/opt/bin/"+currentExtensionID, dir, "/owned/data")
	if changed, err := installHook(path, command); err != nil || !changed {
		t.Fatalf("migrate: changed=%v err=%v", changed, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, keep := range []string{"other-tool hook", "keep-stop-hook", "unchanged"} {
		if !strings.Contains(string(data), keep) {
			t.Fatalf("unrelated setting %q was removed", keep)
		}
	}
	if strings.Count(string(data), tagFlag) != 1 || strings.Contains(string(data), renamedTagFlag) {
		t.Fatalf("migration left duplicate/incompatible markers: %s", data)
	}
	if strings.Contains(string(data), "old-binary") || strings.Contains(string(data), "new-binary hook") {
		t.Fatalf("old commands survived migration: %s", data)
	}
	if changed, err := installHook(path, command); err != nil || changed {
		t.Fatalf("repeat migration changed the file: %v %v", changed, err)
	}
	if changed, err := uninstallHook(path); err != nil || !changed {
		t.Fatalf("uninstall: changed=%v err=%v", changed, err)
	}
	data, _ = os.ReadFile(path)
	if strings.Contains(string(data), tagFlag) || strings.Contains(string(data), renamedTagFlag) ||
		!strings.Contains(string(data), "other-tool hook") || !strings.Contains(string(data), "keep-stop-hook") {
		t.Fatalf("uninstall changed another owner's hooks: %s", data)
	}
	if changed, err := uninstallHook(path); err != nil || changed {
		t.Fatalf("repeat uninstall changed the file: %v %v", changed, err)
	}
}

func TestGeneratedRenamedHookIsRecognizedByOldVersion(t *testing.T) {
	command := hookCommand("/opt/bin/"+currentExtensionID, "/owned/config", "/owned/data")
	// This is the exact ownership predicate shipped by 0.2.23. A rollback
	// must recognize the new command without needing a host-specific cleanup.
	oldVersionOwns := strings.HasSuffix(strings.TrimSpace(command), " --persona-extension="+legacyExtensionID)
	if !oldVersionOwns || strings.Contains(command, renamedTagFlag) {
		t.Fatalf("rollback cannot remove generated command: %s", command)
	}
}

func TestEmbeddedManifestStripsAuthorMetadata(t *testing.T) {
	var source, runtime map[string]json.RawMessage
	if err := json.Unmarshal(authorManifest, &source); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(manifest, &runtime); err != nil {
		t.Fatal(err)
	}
	if string(source["replaces"]) != `"claude-bypass-allow"` {
		t.Fatal("source manifest lost its predecessor declaration")
	}
	for _, key := range []string{"tags", "replaces", "icon"} {
		if _, ok := runtime[key]; ok {
			t.Fatalf("author metadata %s leaked into runtime manifest", key)
		}
	}
	for key, value := range source {
		if key != "tags" && key != "replaces" && key != "icon" && !jsonEqual(runtime[key], value) {
			t.Fatalf("runtime field %s changed", key)
		}
	}
	var identity, sourceIdentity struct{ ID, Version string }
	if err := json.Unmarshal(manifest, &identity); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(authorManifest, &sourceIdentity); err != nil {
		t.Fatal(err)
	}
	// the version follows the source manifest's, so a later bump needs no change here
	if identity.ID != currentExtensionID || identity.Version == "" || identity != sourceIdentity {
		t.Fatalf("runtime identity: %+v, source %+v", identity, sourceIdentity)
	}
}

func TestPackagedManifestStripsSyntheticIcon(t *testing.T) {
	raw := []byte(`{"id":"synthetic","version":"0.1.0","icon":"assets/icon.svg","tags":["demo"],"replaces":"predecessor","unknown_field":true}`)
	var runtime map[string]json.RawMessage
	if err := json.Unmarshal(packagedManifest(raw), &runtime); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"icon", "tags", "replaces"} {
		if _, ok := runtime[key]; ok {
			t.Errorf("source metadata %s leaked", key)
		}
	}
	if string(runtime["unknown_field"]) != "true" {
		t.Fatal("projection silently removed an unknown runtime field")
	}
}
