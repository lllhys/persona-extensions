package wire

import (
	"reflect"
	"testing"
)

// TestRefCacheAnswersWhatTheWalkWould: collectRefs fills refs with what the
// pointer walk answers, so resolve reads the cache instead of splitting a JSON
// pointer per visit. Every entry must name the same file and hold the schema
// map itself, not a copy; a ref the schemas do not carry still walks.
func TestRefCacheAnswersWhatTheWalkWould(t *testing.T) {
	loadOnce.Do(loadSchemas)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	// A collector that found nothing would leave resolve correct but pointless.
	if len(refs) < 10 {
		t.Fatalf("collected %d refs, expected the schemas' own", len(refs))
	}
	for site, target := range refs {
		schema, file := walkRef(site.ref, site.here)
		if file != target.file {
			t.Errorf("%s in %s: cached file %q, walk says %q", site.ref, site.here, target.file, file)
		}
		if !reflect.DeepEqual(schema, target.schema) {
			t.Errorf("%s in %s: cached schema differs from the walk's", site.ref, site.here)
			continue
		}
		if schema != nil && reflect.ValueOf(schema).Pointer() != reflect.ValueOf(target.schema).Pointer() {
			t.Errorf("%s in %s: cache holds a copy of the schema, not the schema", site.ref, site.here)
		}
		// resolve must answer from the cache exactly as the walk does.
		if got, gotFile := resolve(site.ref, site.here); gotFile != file || !reflect.DeepEqual(got, schema) {
			t.Errorf("%s in %s: resolve disagrees with the walk", site.ref, site.here)
		}
	}
}

// TestRefCacheCoversEveryRefTheSchemasCarry: a ref the collector misses still
// resolves, by walking, so a gap costs nothing but the speed this is for. This
// walks the schemas itself, arrays included (a oneOf holds its refs in one), and
// requires every ref it finds to be cached.
func TestRefCacheCoversEveryRefTheSchemasCarry(t *testing.T) {
	loadOnce.Do(loadSchemas)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	var found []refSite
	var walk func(node any, here string)
	walk = func(node any, here string) {
		switch value := node.(type) {
		case map[string]any:
			for key, member := range value {
				if ref, ok := member.(string); ok && key == "$ref" {
					found = append(found, refSite{ref: ref, here: here})
					continue
				}
				walk(member, here)
			}
		case []any:
			for _, member := range value {
				walk(member, here)
			}
		}
	}
	for name, doc := range files {
		walk(doc, name)
	}
	if len(found) == 0 {
		t.Fatal("the schemas carry no $ref; this test no longer tests anything")
	}
	inArrays := 0
	for _, site := range found {
		if _, ok := refs[site]; !ok {
			t.Errorf("%s in %s is not cached, so every visit walks its pointer again", site.ref, site.here)
		}
	}
	// The refs of a oneOf stand inside an array; count them so a collector that
	// stops at objects cannot pass.
	for _, doc := range files {
		for _, def := range defs(doc) {
			one, _ := def.(map[string]any)["oneOf"].([]any)
			for _, variant := range one {
				if _, ok := variant.(map[string]any)["$ref"].(string); ok {
					inArrays++
				}
			}
		}
	}
	if inArrays == 0 {
		t.Error("no oneOf variant names a $ref; the array walk is no longer covered")
	}
}

func TestResolveWalksARefTheSchemasDoNotCarry(t *testing.T) {
	loadOnce.Do(loadSchemas)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	const absent = "#/$defs/NoDefinitionByThisName"
	if _, ok := refs[refSite{ref: absent, here: "view.schema.json"}]; ok {
		t.Fatalf("%s is in the schemas after all; pick another name for this test", absent)
	}
	schema, file := resolve(absent, "view.schema.json")
	if schema != nil {
		t.Errorf("resolved %s to %v, expected nothing", absent, schema)
	}
	if file != "view.schema.json" {
		t.Errorf("resolved %s in file %q, expected the file it stands in", absent, file)
	}
	// A ref naming another file still reads that file, cached or not.
	if _, file := resolve("manifest.schema.json"+absent, "view.schema.json"); file != "manifest.schema.json" {
		t.Errorf("a ref naming manifest.schema.json resolved in %q", file)
	}
}
