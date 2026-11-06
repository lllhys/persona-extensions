package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestScratchCDPATHRequiresConfirmation(t *testing.T) {
	b := newBox(t)
	if err := os.MkdirAll(filepath.Join(b.keep, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, env, command string
	}{
		{"assignment", "", "CDPATH={keep}; cd sub && rm -f sentinel"},
		{"export", "", "export CDPATH={keep}; cd sub && rm -f sentinel"},
		{"command-local", "", "CDPATH={keep} cd sub && rm -f sentinel"},
		{"inherited", b.keep, "cd sub && rm -f sentinel"},
		{"dynamic", "", "read CDPATH; cd sub && rm -f sentinel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CDPATH", tc.env)
			g := newGuard(b.project, false)
			if g.roots.holds(filepath.Join(b.keep, "sub", "sentinel")) {
				t.Fatal("fixture outside path is unexpectedly scratch")
			}
			if why := g.personOnly(b.cmd(tc.command)); why == "" {
				t.Fatal("relative deletion after a CDPATH directory change was auto-approved")
			}
		})
	}
}

func TestScratchCDPATHExplicitPaths(t *testing.T) {
	b := newBox(t)
	t.Setenv("CDPATH", b.keep)
	for _, command := range []string{
		"cd ./sub && rm -f sentinel",
		"cd . && rm -f sentinel",
		"cd ../proj && rm -f sentinel",
		"CDPATH={keep}; cd {project}/sub && rm -f sentinel",
		"CDPATH={keep}; cd sub && rm -f {tmp}/sentinel",
	} {
		g := newGuard(b.project, false)
		if why := g.personOnly(b.cmd(command)); why != "" {
			t.Errorf("explicit scratch path should still be allowed: %q: %s", command, why)
		}
	}
	t.Setenv("CDPATH", "")
	if why := newGuard(b.project, false).personOnly("cd sub && rm -f sentinel"); why != "" {
		t.Fatalf("ordinary relative cd without CDPATH: %s", why)
	}
}

func TestScratchCDPATHMatchesShellDestination(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable")
	}
	b := newBox(t)
	outside := filepath.Join(b.keep, "sub")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CDPATH", b.keep)
	command := exec.Command(bash, "--noprofile", "--norc", "-c", "cd sub >/dev/null && pwd -P")
	command.Dir = b.project
	out, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != outside {
		t.Fatalf("shell destination = %q, want %q", got, outside)
	}
	if why := newGuard(b.project, false).personOnly("cd sub && rm -f sentinel"); why == "" {
		t.Fatal("guard approved a path the shell resolves outside scratch")
	}
}
