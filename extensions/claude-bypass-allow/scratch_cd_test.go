package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A cd run through builtin, command, time, noglob, nocorrect or zsh's -, and zsh's chdir, changes this shell's
// directory like a plain cd; eval may cd anywhere. The shells run each command in the box to show where it deletes.
func TestScratchCdPrefixes(t *testing.T) {
	b := newBox(t)
	t.Setenv("CDPATH", "")
	thesis := filepath.Join(b.home, "Documents", "thesis")
	for _, tc := range []struct{ shell, command string }{
		{"", "cd {home} && rm -rf Documents/*"},
		{"bash", "builtin cd {home} && rm -rf Documents/*"},
		{"bash", "command cd {home} && rm -rf Documents/*"}, // zsh's command runs /usr/bin/cd
		{"bash", "command -p cd {home} && rm -rf Documents/*"},
		{"bash", "time -p cd {home}; rm -rf Documents/*"},
		{"bash", "builtin pushd {home} >/dev/null && rm -rf Documents/*"},
		{"bash", "eval cd {home}; rm -rf Documents/*"},
		{"bash", "eval 'cd {home}'; rm -rf Documents/*"},
		{"zsh", "chdir {home} && rm -rf Documents/*"},
		{"zsh", "builtin chdir {home} && rm -rf Documents/*"},
		{"zsh", "noglob cd {home} && rm -rf Documents/*"},
		{"zsh", "nocorrect cd {home} && rm -rf Documents/*"},
		{"zsh", "true; - cd {home} && rm -rf Documents/*"}, // zsh -c reads a leading - as its own option
	} {
		command := b.cmd(tc.command)
		if sh, err := exec.LookPath(tc.shell); tc.shell != "" && err == nil {
			if err := os.MkdirAll(filepath.Dir(thesis), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(thesis, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			args := []string{"--noprofile", "--norc", "-c", command}
			if tc.shell == "zsh" {
				args = []string{"-f", "-c", command}
			}
			run := exec.Command(sh, args...)
			run.Dir = b.project
			out, _ := run.CombinedOutput()
			if _, err := os.Stat(thesis); !os.IsNotExist(err) {
				t.Fatalf("fixture: %s did not delete %s: %s\n%s", tc.shell, thesis, command, out)
			}
		}
		if newGuard(b.project, false).personOnly(command) == "" {
			t.Errorf("%q should stay with a person", command)
		}
	}
	// a cd read through a prefix keeps its usual checks
	for _, command := range []string{"builtin cd {tmp} && rm -rf x/*", "command cd sub && rm -f out/*", "chdir {tmp}/a; rm -rf x"} {
		command = b.cmd(command)
		if why := newGuard(b.project, false).personOnly(command); why != "" {
			t.Errorf("%q: %s, should go through", command, why)
		}
	}
}
