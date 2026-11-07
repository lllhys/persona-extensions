package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ssh, scp and sftp read -o after other one-letter options in the same word (-qo…), and ssh drops the double quotes
// around a keyword ("ProxyCommand"=…) and skips every = before the command. The real clients run the command in each
// of these spellings, so the guard reads it like -o 'ProxyCommand=…'.
func TestSSHOptionSpellingsTheClientsRun(t *testing.T) {
	b := newBox(t)
	sentinel := filepath.Join(b.keep, "sentinel")
	src := filepath.Join(b.keep, "src")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	rm := "rm -f " + sentinel
	bash, bashErr := exec.LookPath("bash")
	for _, command := range []string{
		"ssh -F /dev/null -qo'ProxyCommand=" + rm + "' host",
		"ssh -F /dev/null -4oProxyCommand='" + rm + "' host",
		"scp -F /dev/null -po'ProxyCommand=" + rm + "' " + src + " host:dst",
		"sftp -F /dev/null -qo'ProxyCommand=" + rm + "' host",
		`ssh -F /dev/null -o '"ProxyCommand"=` + rm + "' host",
		`ssh -F /dev/null -o '"ProxyCommand" ` + rm + "' host",
		`ssh -F /dev/null -o 'Proxy"Command"=` + rm + "' host",
		"ssh -F /dev/null -o 'ProxyCommand==" + rm + "' host",
	} {
		if _, err := exec.LookPath(strings.Fields(command)[0]); err == nil && bashErr == nil {
			if err := os.WriteFile(sentinel, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			out, _ := exec.CommandContext(ctx, bash, "--noprofile", "--norc", "-c", command).CombinedOutput()
			cancel()
			if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
				t.Fatalf("fixture: the client did not run the -o command: %s\n%s", command, out)
			}
		}
		if why := newGuard(b.project, false).personOnly(command); why == "" {
			t.Errorf("auto-allowed, but the client runs the rm in its -o value: %s", command)
		}
	}
}
