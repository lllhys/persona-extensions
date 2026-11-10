package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every applemusic_* name in the Apple skill must be a tool or action the connector lists; a wildcard prefix must
// match at least one of them. Renaming a tool without the skill then fails the test instead of the model.
func TestAppleSkillNamesMatchTheTools(t *testing.T) {
	a := appWith(t, newMacProvider("m1", macFixture()), newIPhoneProvider(openSnapshotStore(t.TempDir())))
	src := a.sources[appleMusicID]
	known := map[string]bool{}
	for _, list := range [][]tool{a.connectorTools(src), a.connectorActions(src)} {
		for _, tl := range list {
			known[tl.Name] = true
		}
	}
	data, err := os.ReadFile("skills/applemusic/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	names := regexp.MustCompile(`applemusic_[a-z0-9_]+`).FindAllString(string(data), -1)
	if len(names) == 0 {
		t.Fatal("the Apple skill names no tools")
	}
	for _, name := range names {
		if known[name] {
			continue
		}
		if strings.HasSuffix(name, "_") {
			prefix := false
			for tool := range known {
				if strings.HasPrefix(tool, name) {
					prefix = true
					break
				}
			}
			if prefix {
				continue
			}
		}
		t.Errorf("the Apple skill names %q, which the connector does not list", name)
	}
	// The shared skill holds the rules common to every service and names none of one service's tools.
	shared, err := os.ReadFile("skills/music/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(shared), "applemusic_") {
		t.Error("the shared music skill names Apple Music's tools")
	}
}
