package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// box is a computer for the scratch tests: a home directory with the usual scratch directories, a temporary
// directory that stands in for /tmp, a project outside the home directory, and a directory to keep.
type box struct{ root, home, tmp, project, keep string }

func newBox(t *testing.T) box {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := box{root: root, home: filepath.Join(root, "home", "me"), tmp: filepath.Join(root, "tmp"),
		project: filepath.Join(root, "code", "proj"), keep: filepath.Join(root, "keep")}
	for _, d := range []string{b.home + "/ag-work/v1", b.home + "/wt", b.home + "/.ssh", b.tmp, b.project + "/sub", b.keep} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// t.TempDir is under the real temporary directory: only the box's own temporary directory counts
	old := tmpRoots
	tmpRoots = []string{b.tmp}
	t.Cleanup(func() { tmpRoots = old })
	// on macOS it is under /private/var/folders, a system directory: drop the ones the box is in, and keep where
	// the others lead (/etc is /private/etc there)
	oldSys := systemDirs
	systemDirs = nil
	for _, s := range oldSys {
		if r := resolve(s, true); !within(root, r) {
			systemDirs = append(systemDirs, s, r)
		}
	}
	t.Cleanup(func() { systemDirs = oldSys })
	t.Setenv("HOME", b.home)
	t.Setenv("TMPDIR", "")
	return b
}

// the words in a test command: ~ stays as it is, {tmp} and the others are the box's directories
func (b box) cmd(c string) string {
	return strings.NewReplacer("{tmp}", b.tmp, "{home}", b.home, "{project}", b.project, "{keep}", b.keep, "{root}", b.root).Replace(c)
}

// realCommand is the command a workflow subagent waited an hour on (its paths made the box's).
const realCommand = "source ~/ag-work/e1env.sh >/dev/null; S={home}/ag-work/v1; for v in e1 e2v; do rm -rf $S/$v; " +
	"rsync -a --exclude .git --exclude dist ~/ag-work/wt-e1/ $S/$v/; done; cd $S/e2v && python3 -I - <<'EOF'\n" +
	"import json, pathlib\np = pathlib.Path('package.json')\nd = json.loads(p.read_text())\nd['name'] = 'e2v'\n" +
	"p.write_text(json.dumps(d, indent=2))\nEOF"

func TestScratchDeletions(t *testing.T) {
	b := newBox(t)
	if err := os.Symlink(b.keep, filepath.Join(b.tmp, "link")); err != nil {
		t.Fatal(err)
	}
	goes := []string{
		realCommand,
		"rm -rf {tmp}/x", "rm -rf {tmp}/x/*", "rm -rf ~/wt/feat ~/ag-work/v1/e1", "rm -rf $HOME/ag-work/old", "rm -rf ${HOME}/wt/x",
		"rm -rf build", "rm -rf ./dist node_modules", "cd sub && rm -f out/*", "rm -rf {project}/build",
		"X={tmp}/a; rm -rf \"$X\"/b", "X={tmp}/a\nrm -rf \"${X}/b\"", "S={tmp}; for v in a b c; do rm -rf $S/$v; done",
		"S={tmp}/a && rm -rf $S/b", "rm -rf {root}/r/.claude/worktrees/w", "rm -rf ~/.claude/worktrees/w/sub",
		"rmdir {tmp}/empty", "unlink {tmp}/f", "sudo rm -rf {tmp}/x", "timeout 10 rm -rf {tmp}/x", "if true; then rm -rf {tmp}/x; fi",
		"mv {tmp}/a {tmp}/b", "mv -t {tmp}/d {tmp}/a", "cp -f {tmp}/a build/b", "echo x > build/out.json", "go test > ~/ag-work/v1/log 2>&1",
		"tee build/x.log", "truncate -s 0 {tmp}/log", "find {tmp}/x -name '*.o' -delete", "find . -name '*.o' -delete",
		"rsync -a --delete src/ {tmp}/dst/", "rm -rf {tmp}/link", "bash -c 'rm -rf {tmp}/x'",
		"cd {tmp} && rm -rf x", "cd {tmp}/a; cd b; rm -rf c", "rm -rf ~/wt/x; echo $(date)", "sh -c 'S={tmp}/a; rm -rf $S'",
	}
	g := newGuard(b.project, false)
	for _, c := range goes {
		c = b.cmd(c)
		if why := g.personOnly(c); why != "" {
			t.Errorf("%q: %s, should go through", c, why)
		} else if !strings.Contains(strings.Join(g.notes, ";"), "scratch") {
			t.Errorf("%q: no scratch note: %v", c, g.notes)
		}
	}
	stays := []string{
		// the home directory, its parents, the scratch roots themselves, system and hidden directories
		"rm -rf ~", "rm -rf ~/", "rm -rf $HOME", "rm -rf ~/ag-work", "rm -rf {tmp}", "rm -rf /", "rm -rf {home}/..",
		"rm -rf {tmp}/../keep", "rm -rf /etc/x", "rm -rf ~/.ssh", "rm -rf ~/.ssh/id", "rm -rf ~/notes", "rm -rf {keep}/x",
		"rm -rf .git", "rm -rf sub/.git/hooks", "rm -rf .", "rm -rf ..", "rm -rf ../other", "rm -rf {project}",
		// .GIT and .Git are .git on macOS's case-insensitive file systems
		"rm -rf .GIT", "rm -rf .Git/objects", "rm -rf sub/.gIt/hooks", "rm -rf {tmp}/clone/.GIT", "rm -rf ~/.claude/worktrees/w/.GIT",
		"rm -rf {tmp}/*/", "rm -rf {tmp}/.*", "rm -rf {tmp}/*/x",
		// through a symlink that leads out, or one the command may make
		"rm -rf {tmp}/link/", "rm -rf {tmp}/link/f", "ln -s {keep} {tmp}/l2; rm -rf {tmp}/l2/f",
		// variables the check cannot read
		"rm -rf $S/x", "rm -rf $S; S={tmp}/a", "S={tmp}/a; S=/etc; rm -rf $S/x", "if x; then S={tmp}/a; fi; rm -rf $S/x",
		"x && S={tmp}/a; rm -rf $S", "S={tmp}/a; read S; rm -rf $S", "S={tmp}/a; export S; rm -rf $S",
		"S={tmp}/a | true; rm -rf $S", "S={tmp}/a & rm -rf $S", "S=\"{tmp}/a /etc\"; rm -rf $S", "S=$(mktemp -d); rm -rf $S",
		"S={tmp}/a; rm -rf '$S'", "S={tmp}/a; rm -rf \\$S", "S={tmp}/a; rm -rf ${S:-/etc}", "S={tmp}/*; rm -rf $S",
		"for v in a ..; do rm -rf {tmp}/$v; done", "for v in a /etc; do rm -rf $v; done", "for v in $(ls); do rm -rf {tmp}/$v; done", "for v; do rm -rf $v; done",
		"IFS=/; S={tmp}/a; rm -rf $S", "f() { S=/etc; }; S={tmp}/a; f; rm -rf $S", "S={tmp}/a; S+=/../../etc; rm -rf $S",
		"S={tmp}/a; printf -vS /etc; rm -rf $S", "S={tmp}/a; bash -c 'rm -rf $S'", "S={tmp}/a; bash <<'EOF'\nrm -rf $S\nEOF",
		"HOME=/; rm -rf ~/x", "rm -rf ~other/x", "rm -rf $TMPDIR/x",
		// a directory the command goes to that is not scratch, or one the check cannot read
		"cd /etc && rm -rf x", "cd $D && rm -rf x", "cd && rm -rf x", "cd - && rm -rf x", "(cd /etc; rm -rf x)", "pushd /etc; rm -rf x",
		// wrappers that take the targets elsewhere
		"ssh host 'rm -rf {tmp}/x'", "docker exec c sh -c 'rm -rf {tmp}/x'", "sh -c 'rm -rf x'",
		"find {tmp} -exec rm -rf {} +", "ls | xargs rm -rf", "ssh host rm -rf {tmp}/x", "eval rm -rf {tmp}/x",
		"env -C /etc rm -rf x", "sudo -D /etc rm -rf x", "find -L {tmp}/x -delete", "rsync -a --delete src/ host:{tmp}/d",
		"rsync -a --delete src/ ~/notes/", "rmdir -p {tmp}/a/b", "mv {tmp}/a ~/notes", "cp -f {tmp}/a ~/notes",
		// what scratch never covers
		"chmod -R 777 {tmp}/x", "rm -rf {tmp}/x && chmod 777 y", "rm -rf {tmp}/x; git push --force", "dd if=/dev/zero of={tmp}/x",
		"sed -i s/a/b/ {tmp}/f",
	}
	for _, c := range stays {
		c = b.cmd(c)
		if g.personOnly(c) == "" {
			t.Errorf("%q should stay with a person", c)
		}
	}
	// a session whose working directory is not a project: its relative targets are not scratch
	for _, cwd := range []string{b.home, "/", filepath.Dir(b.home), b.home + "/.ssh", "/etc", ""} {
		if why := newGuard(cwd, false).personOnly("rm -rf build"); why == "" {
			t.Errorf("cwd %q: rm -rf build should stay with a person", cwd)
		}
	}
	if why := newGuard(b.home, false).personOnly(b.cmd("rm -rf {tmp}/x")); why != "" {
		t.Errorf("an absolute scratch target from the home directory: %s", why)
	}
	t.Setenv("TMPDIR", b.tmp+"/t")
	if why := newGuard(b.home, false).personOnly(b.cmd("rm -rf $TMPDIR/x")); why != "" {
		t.Errorf("$TMPDIR: %s", why)
	}
}

func TestAllowDestructiveSetting(t *testing.T) {
	b := newBox(t)
	g := newGuard(b.home, true)
	for _, c := range []string{
		"rm -rf /srv/data", "rm -rf ~/notes", "git reset --hard", "mv a b", "echo {} > settings.json", "git clean -fdx",
		"docker system prune -a", "psql -c 'DROP TABLE t'", "git branch -D x", "kubectl delete pod p", "git checkout -- .",
		"find . -delete", "truncate -s 0 log", "sed -i s/a/b/ f", "aws s3 rm s3://b/x", "cd x && rm -f sub/*", "ls | xargs rm",
		"ssh host rm -rf /srv/x", "bash -c 'rm -rf x'", "gh repo delete me/r --yes", "dd if=a of=b",
	} {
		if why := g.personOnly(c); why != "" {
			t.Errorf("%q: %s, the setting should let it through", c, why)
		} else if !strings.Contains(strings.Join(g.notes, ";"), "setting: ") {
			t.Errorf("%q: no setting note: %v", c, g.notes)
		}
	}
	for _, c := range []string{
		"git push --force", "git push -f origin main", "chmod 777 x", "chown me f", "rm -rf x && chmod 777 y",
		"rm -rf x; git push -f", "bash -c 'git push -f'", "gh secret delete X", "gh -R o/r secret set X",
		"gh api -X DELETE repos/o/r/branches/main/protection", "kubectl delete rolebinding rb", "psql -c 'GRANT ALL ON t TO u'",
		"psql -c 'DROP USER u'", "mkfs.ext4 /dev/sdb1", "dd if=/dev/zero of=/dev/sda", "wipefs -a /dev/sda",
		"gh api graphql -f query='mutation{x}'", "setfacl -m u:x:r f", "find . -exec chmod 777 {} +",
	} {
		if g.personOnly(c) == "" {
			t.Errorf("%q should stay with a person even with the setting", c)
		}
	}
	if why := newGuard(b.home, false).personOnly("rm -rf /srv/data"); why == "" {
		t.Error("without the setting rm -rf /srv/data should stay with a person")
	}
}

func TestHookScratchAndSetting(t *testing.T) {
	b := newBox(t)
	e := newEnv(t)
	now := time.Now()
	req := func(cwd, cmd string) map[string]any {
		r := request(e, "bypassPermissions", "Bash", cmd)
		r["cwd"] = cwd
		return r
	}
	real := b.cmd(realCommand)
	if got := e.hook(t, req(b.project, real), now); strings.TrimSpace(got) != allowOutput {
		t.Fatalf("the real command in bypass mode: %q", got)
	}
	r := req(b.project, real)
	r["permission_mode"] = "default"
	if got := e.hook(t, r, now); got != "" {
		t.Errorf("the real command in default mode: %q", got)
	}
	audit := readAudit(e.data, 1)
	if len(audit) != 1 || audit[0].Note != "scratch: rm" || !strings.HasPrefix(audit[0].Command, "source ~/ag-work/e1env.sh") {
		t.Errorf("audit: %+v", audit)
	}

	// the setting is off until the node program writes it on
	if got := e.hook(t, req(b.home, "rm -rf /srv/data"), now); got != "" {
		t.Errorf("rm -rf /srv/data without the setting: %q", got)
	}
	writeJSON(t, filepath.Join(e.data, optionsFile), `{"allow_destructive":true}`)
	if got := e.hook(t, req(b.home, "rm -rf /srv/data && git reset --hard"), now); strings.TrimSpace(got) != allowOutput {
		t.Errorf("rm -rf /srv/data with the setting: %q", got)
	}
	if a := readAudit(e.data, 1)[0]; a.Note != "setting: deletion: rm; setting: overwrite: git reset --hard" {
		t.Errorf("audit note: %q", a.Note)
	}
	for _, c := range []string{"git push --force", "chmod -R 777 .", "rm -rf x; chown me y"} {
		if got := e.hook(t, req(b.home, c), now); got != "" {
			t.Errorf("%q with the setting: %q, should stay with a person", c, got)
		}
	}
	// the user's own ask and deny rules still ask, the setting or scratch notwithstanding
	writeJSON(t, filepath.Join(e.config, "settings.json"), `{"permissions":{"ask":["Bash(rm -rf:*)"]}}`)
	for _, c := range []string{"rm -rf /srv/data", b.cmd("rm -rf {tmp}/x")} {
		if got := e.hook(t, req(b.home, c), now); got != "" {
			t.Errorf("%q under an ask rule: %q", c, got)
		}
	}
	writeJSON(t, filepath.Join(e.data, optionsFile), `not json`)
	if got := e.hook(t, req(b.home, "git reset --hard"), now); got != "" {
		t.Errorf("an unreadable options file is the defaults: %q", got)
	}
}

func TestNodeWritesOptions(t *testing.T) {
	for _, c := range []struct {
		config string
		want   bool
	}{{`{"machines":["m1"]}`, false}, {`{"machines":["m1"],"allow_destructive":true}`, true}} {
		e := newEnv(t)
		t.Setenv("HOME", filepath.Dir(e.config))
		writeJSON(t, filepath.Join(e.data, optionsFile), `{"allow_destructive":true}`) // an old one
		n := &node{bin: "/opt/bin/claude-bypass-allow"}
		in := initWith(e, c.config)
		ctx, cancel := context.WithCancel(context.Background())
		if _, err := n.init(ctx, in, nil); err != nil {
			t.Fatal(err)
		}
		if got := readOptions(e.data).AllowDestructive; got != c.want {
			t.Errorf("%s: allow_destructive %v", c.config, got)
		}
		b, _ := os.ReadFile(filepath.Join(e.data, optionsFile))
		var o map[string]any
		if json.Unmarshal(b, &o) != nil {
			t.Errorf("options file: %q", b)
		}
		cancel()
		n.cleanup()
	}
}

func initWith(e env, config string) *ext.Init {
	in := &ext.Init{}
	in.Extension = &wire.ExtensionInfo{ID: "claude-bypass-allow", DataDir: e.data}
	in.InitializeParams.Config = json.RawMessage(config)
	return in
}
