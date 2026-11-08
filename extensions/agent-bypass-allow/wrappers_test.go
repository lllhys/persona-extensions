package main

import "testing"

// Programs that exec the command after their options (caffeinate, arch and xcrun ship with macOS) and runner
// subcommands (uv run, bundle exec) are wrappers: the program after them is read.
func TestExecWrappers(t *testing.T) {
	stays := []string{
		"caffeinate -i rm -rf ~/Documents", "caffeinate -dimsu git push --force", "arch -arm64 rm -rf ~/Documents",
		"xcrun rm -rf ~/Documents", "pkexec rm -rf /srv/data", "nsenter -t 1 -m rm -rf /srv/data",
		"systemd-run --user rm -rf /srv/data", "chrt 1 rm -rf ~/Documents", "numactl -N0 rm -rf ~/Documents",
		"fakeroot rm -rf ~/Documents", "valgrind rm -rf ~/Documents", "ltrace rm -rf ~/Documents",
		"gdb --batch -ex run --args rm -rf ~/Documents", "/usr/bin/caffeinate -i chmod -R 777 ~/.ssh",
		"uv run rm -rf ~/Documents", "uv run --with x rm -rf ~/Documents", "poetry run rm -rf ~/Documents",
		"pdm run rm -rf ~/Documents", "pipenv run rm -rf ~/Documents", "hatch run rm -rf ~/Documents",
		"bundle exec rm -rf ~/Documents", "npm exec rm -rf ~/Documents", "pnpm exec rm -rf ~/Documents",
		"yarn exec rm -rf ~/Documents", "uv --directory /srv run rm -rf ~/Documents", "uv tool run rm -rf ~/Documents",
		"poetry -C /srv run rm -rf ~/Documents", "hatch env run rm -rf ~/Documents",
		"CAFFEINATE rm -rf ~/Documents", "UV run rm -rf ~/Documents",
	}
	for _, c := range stays {
		if why := newGuard("/Users/u/proj", false).personOnly(c); why == "" {
			t.Errorf("%q: auto-allowed, want it to stay with a person", c)
		}
	}
	goes := []string{
		"caffeinate -i go test ./...", "caffeinate -i make build", "arch -arm64 brew list", "xcrun simctl list",
		"uv run pytest", "poetry run python -m app", "bundle exec rspec", "npm exec -- prettier --check .",
		"uv pip install rm", "npm run rm",
	}
	for _, c := range goes {
		if why := personOnly(c); why != "" {
			t.Errorf("%q: %s, should go through", c, why)
		}
	}
}

// pkexec, nsenter and systemd-run run the program as another user, in another namespace or in a unit, and a
// runner can change directory first (uv run --directory, poetry -C), so its targets are not this shell's scratch
// paths.
func TestExecWrappersNoScratch(t *testing.T) {
	b := newBox(t)
	g := newGuard(b.project, false)
	for _, c := range []string{"pkexec rm -rf {tmp}/x", "nsenter -t 1 -m rm -rf {tmp}/x", "systemd-run rm -rf {tmp}/x",
		"cd {project} && uv run --directory /srv rm -rf build", "cd {project} && poetry -C /srv run rm -rf build"} {
		if c = b.cmd(c); g.personOnly(c) == "" {
			t.Errorf("%q should stay with a person", c)
		}
	}
}
