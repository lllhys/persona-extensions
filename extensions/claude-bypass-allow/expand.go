package main

import (
	"path"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The shell's word expansions that scan and segmentPersonOnly read, so that a program spelled another way is still
// seen: $'…' quotes, brace expansion, and a glob in the program word. Each errs toward asking.

// While scan reads a word, it writes the braces and commas the shell may brace-expand as these runes (from Unicode's
// private use area), so that a quoted "{a,b}" stays one word; braceExpand turns them back.
const (
	braceOpen  = '\uE000'
	braceClose = '\uE001'
	braceComma = '\uE002'
)

// braceMax is how many words braceExpand makes of one word at most; past it, the rest are not made.
const braceMax = 4096

// braceExpand is the words the shell makes of w by brace expansion: r{m,} is rm and r, {rm,-rf,x} is rm, -rf and x,
// {q..s}m is qm, rm and sm. Braces without a comma or a range ({}, {x}) are kept as they are.
func braceExpand(w string) []string {
	if !strings.ContainsRune(w, braceOpen) {
		return []string{restoreBraces(w)}
	}
	var out []string
	expandInto(w, &out)
	for i := range out {
		out[i] = restoreBraces(out[i])
	}
	return out
}

func expandInto(w string, out *[]string) {
	if len(*out) >= braceMax {
		return
	}
	rs := []rune(w)
	for i, r := range rs {
		if r != braceOpen {
			continue
		}
		depth, commas, end := 0, []int(nil), -1
		for j := i; j < len(rs) && end < 0; j++ {
			switch rs[j] {
			case braceOpen:
				depth++
			case braceClose:
				if depth--; depth == 0 {
					end = j
				}
			case braceComma:
				if depth == 1 {
					commas = append(commas, j)
				}
			}
		}
		if end < 0 {
			break // no closing brace: the rest is as written
		}
		var parts []string
		if len(commas) > 0 {
			start := i + 1
			for _, c := range append(commas, end) {
				parts = append(parts, string(rs[start:c]))
				start = c + 1
			}
		} else {
			parts = braceRange(string(rs[i+1 : end]))
		}
		if parts == nil {
			continue // {}, {x}: literal braces; a later pair may still expand
		}
		prefix, suffix := string(rs[:i]), string(rs[end+1:])
		for _, p := range parts {
			expandInto(prefix+p+suffix, out)
		}
		return
	}
	*out = append(*out, w)
}

// braceRange is the words of a sequence expression's inside (a..e, 1..10, 1..10..2), nil when it is not one.
func braceRange(in string) []string {
	f := strings.Split(in, "..")
	if len(f) != 2 && len(f) != 3 {
		return nil
	}
	step := 1
	if len(f) == 3 {
		n, err := strconv.Atoi(f[2])
		if err != nil {
			return nil
		}
		if step = n; step < 0 {
			step = -step
		}
		if step == 0 {
			step = 1
		}
	}
	a, errA := strconv.Atoi(f[0])
	b, errB := strconv.Atoi(f[1])
	letters := false
	if errA != nil || errB != nil {
		ra, na := utf8.DecodeRuneInString(f[0])
		rb, nb := utf8.DecodeRuneInString(f[1])
		if na != len(f[0]) || nb != len(f[1]) || na == 0 || nb == 0 || ra > 127 || rb > 127 {
			return nil
		}
		a, b, letters = int(ra), int(rb), true
	}
	var out []string
	add := func(n int) {
		if letters {
			out = append(out, string(rune(n)))
		} else {
			out = append(out, strconv.Itoa(n))
		}
	}
	if a <= b {
		for n := a; n <= b && len(out) < braceMax; n += step {
			add(n)
		}
	} else {
		for n := a; n >= b && len(out) < braceMax; n -= step {
			add(n)
		}
	}
	return out
}

func restoreBraces(w string) string {
	return strings.NewReplacer(string(braceOpen), "{", string(braceClose), "}", string(braceComma), ",").Replace(w)
}

// ansiQuote reads the $'…' quote whose ' is at rs[i]: its text with the backslash escapes decoded as bash does, and
// the index of its closing quote (the last one when it is not closed).
func ansiQuote(rs []rune, i int) (int, string) {
	var b strings.Builder
	for i++; i < len(rs) && rs[i] != '\''; i++ {
		if rs[i] != '\\' || i+1 == len(rs) {
			b.WriteRune(rs[i])
			continue
		}
		i++
		if r, ok := map[rune]rune{'a': 7, 'b': 8, 'e': 27, 'E': 27, 'f': 12, 'n': '\n', 'r': '\r', 't': '\t', 'v': 11,
			'\\': '\\', '\'': '\'', '"': '"', '?': '?'}[rs[i]]; ok {
			b.WriteRune(r)
			continue
		}
		base, max := 0, 0
		switch {
		case rs[i] >= '0' && rs[i] <= '7':
			base, max = 8, 3
			i-- // the digit is the number's first
		case rs[i] == 'x':
			base, max = 16, 2
		case rs[i] == 'u':
			base, max = 16, 4
		case rs[i] == 'U':
			base, max = 16, 8
		case rs[i] == 'c' && i+1 < len(rs):
			i++
			b.WriteRune(rs[i] & 0x1f)
			continue
		default:
			b.WriteRune('\\')
			b.WriteRune(rs[i])
			continue
		}
		n, digits := 0, 0
		for digits < max && i+1 < len(rs) {
			d, err := strconv.ParseInt(string(rs[i+1]), base, 0)
			if err != nil {
				break
			}
			n, digits, i = n*base+int(d), digits+1, i+1
		}
		if digits == 0 {
			b.WriteRune('\\')
			b.WriteRune(rs[i])
			continue
		}
		b.WriteRune(rune(n))
	}
	return i, b.String()
}

// globNames are the program names the guard knows about; a program word with glob characters (/bin/r?, r[m]) counts
// as each of them it matches, since the shell runs whichever file the glob matches.
var globNames = func() []string {
	names := []string{"git", "gh", "find", "rsync", "sed", "perl", "ln", "install", "cp", "tee", "docker", "podman",
		"kubectl", "helm", "terraform", "tofu", "pulumi", "aws", "gcloud", "gsutil", "az", "mkfs", "mkfs.ext4"}
	for n := range alwaysPerson {
		names = append(names, n)
	}
	for n := range wrappers {
		if !strings.HasPrefix(n, "-") {
			names = append(names, n)
		}
	}
	for n := range shells {
		names = append(names, n)
	}
	return names
}()

// globMatches is the known program names that the glob pattern name (a path's last element) matches.
func globMatches(name string) []string {
	if !strings.ContainsAny(name, "*?[") {
		return nil
	}
	pattern := strings.ReplaceAll(name, "[!", "[^") // the shell's [!…] is path.Match's [^…]
	var out []string
	for _, n := range globNames {
		if ok, err := path.Match(pattern, n); err == nil && ok {
			out = append(out, n)
		}
	}
	return out
}
