//go:build darwin

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

func musicScripts() map[string]string {
	return map[string]string{
		"status":          statusScript,
		"now":             nowScript,
		"recent":          recentScript,
		"playlists":       playlistsScript,
		"playlist":        playlistScript,
		"search":          searchScript,
		"stats":           statsScript,
		"control":         controlScript,
		"create-playlist": createPlaylistScript,
		"artwork":         artworkScript,
		"lyrics":          lyricsScript,
	}
}

// TestScriptsCompile checks every AppleScript against Music's dictionary without running it, so a syntax or
// terminology mistake fails here instead of on the user's Mac. Compiling sends no Apple events and asks for no
// Automation permission.
func TestScriptsCompile(t *testing.T) {
	dir := t.TempDir()
	for name, script := range musicScripts() {
		src := filepath.Join(dir, name+".applescript")
		if err := os.WriteFile(src, []byte(script), 0600); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(dir, name+".scpt")
		cmd := exec.Command("/usr/bin/osacompile", "-o", out, src)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s: %v\n%s", name, err, b)
		}
	}
}

// TestScriptVariablesAvoidMusicTerms rejects a script variable named like a Music term. A script that mentions an
// application compiles such a bare identifier as the application's property, even outside the tell block, so
// `set kind to "track"` sends Music a set event and fails at run time with -10006. Compiling alone cannot catch it.
func TestScriptVariablesAvoidMusicTerms(t *testing.T) {
	terms := musicTerms(t)
	setTo := regexp.MustCompile(`(?m)\bset\s+(?:my\s+)?([A-Za-z_][A-Za-z0-9_]*)\s+to\b`)
	for name, script := range musicScripts() {
		for _, m := range setTo.FindAllStringSubmatch(script, -1) {
			if _, ok := terms[m[1]]; ok {
				t.Errorf("%s: variable %q is a Music term; it would be compiled as a Music property, not a script variable", name, m[1])
			}
		}
	}
}

func musicTerms(t *testing.T) map[string]struct{} {
	t.Helper()
	app := ""
	for _, candidate := range []string{"/System/Applications/Music.app", "/Applications/Music.app"} {
		if _, err := os.Stat(candidate); err == nil {
			app = candidate
			break
		}
	}
	if app == "" {
		t.Skip("Music.app is not installed")
	}
	out, err := exec.Command("/usr/bin/sdef", app).Output()
	if err != nil {
		t.Skipf("sdef %s: %v", app, err)
	}
	names := regexp.MustCompile(`name="([^"]+)"`).FindAllStringSubmatch(string(out), -1)
	terms := make(map[string]struct{}, len(names))
	for _, m := range names {
		terms[m[1]] = struct{}{}
	}
	return terms
}
