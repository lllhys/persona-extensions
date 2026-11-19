package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// The Music.app AppleScript this extension's Mac program runs. Every script prints records separated by ASCII 30 and
// fields by ASCII 31, so a library scan is one process; arguments after "--" reach the script's run handler and are
// never pasted into AppleScript source. A title or description can itself contain either separator, so every dynamic
// text goes through sanitize(), which escapes the separators (and the backslash that introduces the escapes); records()
// reverses it.
const (
	fieldSep  = "\x1f"
	recordSep = "\x1e"
)

// sanitizeScript is the handler behind every dynamic text field below. Call it as "my sanitize(...)": inside a tell
// block an unqualified name would be looked up in Music's own terminology first.
const sanitizeScript = `
on sanitize(v)
  if v is missing value then return ""
  set v to v as text
  set US to (ASCII character 31)
  set RS to (ASCII character 30)
  set text item delimiters to "\\"
  set parts to text items of v
  set text item delimiters to "\\\\"
  set v to parts as text
  set text item delimiters to RS
  set parts to text items of v
  set text item delimiters to "\\u001e"
  set v to parts as text
  set text item delimiters to US
  set parts to text items of v
  set text item delimiters to "\\u001f"
  set v to parts as text
  set text item delimiters to ""
  return v
end sanitize
`

// scriptRunner runs one AppleScript.
type scriptRunner interface {
	Run(ctx context.Context, script string, args ...string) (string, error)
}

// osaRunner runs AppleScript with osascript.
type osaRunner struct{}

func (osaRunner) Run(ctx context.Context, script string, args ...string) (string, error) {
	argv := []string{"-e", script}
	if len(args) > 0 {
		argv = append(argv, "--")
		argv = append(argv, args...)
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/osascript", argv...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}

const statusScript = sanitizeScript + `
set US to (ASCII character 31)
set isRunning to application "Music" is running
if not isRunning then return "0" & US & "" & US & "0" & US & "0"
tell application "Music"
  set v to version
  set trackCount to 0
  set playlistCount to 0
  try
    set trackCount to count of tracks of library playlist 1
  end try
  try
    set playlistCount to count of user playlists
  end try
  return "1" & US & (my sanitize(v)) & US & (trackCount as text) & US & (playlistCount as text)
end tell
`

const nowScript = sanitizeScript + `
set US to (ASCII character 31)
tell application "Music"
  set stateText to player state as text
  if stateText is "stopped" then return stateText & US & "" & US & "" & US & "" & US & "" & US & "0" & US & "0"
  try
    set t to current track
  on error
    return stateText & US & "" & US & "" & US & "" & US & "" & US & "0" & US & "0"
  end try
  set pos to 0
  try
    set pos to player position as integer
  end try
  set dur to 0
  try
    set dur to duration of t as integer
  end try
  return stateText & US & (my sanitize(persistent ID of t)) & US & (my sanitize(name of t)) & US & (my sanitize(artist of t)) & US & (my sanitize(album of t)) & US & (pos as text) & US & (dur as text)
end tell
`

const recentScript = sanitizeScript + `
on run argv
  set US to (ASCII character 31)
  set RS to (ASCII character 30)
  set lim to (item 1 of argv) as integer
  tell application "Music"
    set out to ""
    set n to 0
    set lib to library playlist 1
    set cutoff to (current date) - 90 * days
    set candidates to {}
    try
      set candidates to (every track of lib whose played date is greater than cutoff)
    end try
    if (count of candidates) is 0 then
      try
        set candidates to (every track of lib whose played date is not missing value)
      end try
    end if
    repeat with t in candidates
      set pd to ""
      try
        set pd to (played date of t as «class isot» as string)
      end try
      set pc to 0
      try
        set pc to played count of t
      end try
      set rt to 0
      try
        set rt to rating of t
      end try
      set out to out & (my sanitize(persistent ID of t)) & US & (my sanitize(name of t)) & US & (my sanitize(artist of t)) & US & (my sanitize(album of t)) & US & (duration of t as integer) & US & (pc as text) & US & (my sanitize(pd)) & US & (rt as text) & RS
      set n to n + 1
      if n ≥ 2000 then exit repeat
    end repeat
    return out
  end tell
end run
`

const playlistsScript = sanitizeScript + `
on run argv
  set US to (ASCII character 31)
  set RS to (ASCII character 30)
  tell application "Music"
    set out to ""
    repeat with pl in user playlists
      try
        set descr to ""
        try
          set descr to description of pl
        end try
        set cls to class of pl as text
        set out to out & (my sanitize(persistent ID of pl)) & US & (my sanitize(name of pl)) & US & ((count of tracks of pl) as text) & US & ((duration of pl as integer) as text) & US & (my sanitize(descr)) & US & (my sanitize(cls)) & RS
      end try
    end repeat
    return out
  end tell
end run
`

const playlistScript = sanitizeScript + `
on run argv
  set US to (ASCII character 31)
  set RS to (ASCII character 30)
  set pid to item 1 of argv
  set startAt to (item 2 of argv) as integer
  set lim to (item 3 of argv) as integer
  tell application "Music"
    set pl to missing value
    try
      set pl to first user playlist whose persistent ID is pid
    end try
    if pl is missing value then return ""
    set descr to ""
    try
      set descr to description of pl
    end try
    set trackList to tracks of pl
    set totalTracks to count of trackList
    set out to "P" & US & (my sanitize(persistent ID of pl)) & US & (my sanitize(name of pl)) & US & (totalTracks as text) & US & ((duration of pl as integer) as text) & US & (my sanitize(descr)) & RS
    set lastIndex to startAt + lim
    if lastIndex > totalTracks then set lastIndex to totalTracks
    if startAt < totalTracks then
      repeat with i from (startAt + 1) to lastIndex
        set t to item i of trackList
        set out to out & "T" & US & (my sanitize(persistent ID of t)) & US & (my sanitize(name of t)) & US & (my sanitize(artist of t)) & US & (my sanitize(album of t)) & US & (duration of t as integer) & US & (played count of t) & US & (rating of t) & RS
      end repeat
    end if
    return out
  end tell
end run
`

const searchScript = sanitizeScript + `
on run argv
  set US to (ASCII character 31)
  set RS to (ASCII character 30)
  set q to item 1 of argv
  set lim to (item 2 of argv) as integer
  tell application "Music"
    set out to ""
    set n to 0
    set hits to (every track of library playlist 1 whose name contains q or artist contains q or album contains q)
    repeat with t in hits
      set out to out & (my sanitize(persistent ID of t)) & US & (my sanitize(name of t)) & US & (my sanitize(artist of t)) & US & (my sanitize(album of t)) & US & (duration of t as integer) & US & (played count of t) & US & (rating of t) & RS
      set n to n + 1
      if n ≥ lim then exit repeat
    end repeat
    return out
  end tell
end run
`

const statsScript = `
set US to (ASCII character 31)
tell application "Music"
  set lib to library playlist 1
  set trackCount to count of tracks of lib
  set durationSeconds to 0
  try
    set durationSeconds to duration of lib as integer
  end try
  set playlistCount to 0
  try
    set playlistCount to count of user playlists
  end try
  return (trackCount as text) & US & (durationSeconds as text) & US & (playlistCount as text)
end tell
`

const controlScript = `
on run argv
  set action to item 1 of argv
  tell application "Music"
    if action is "play_track" then
      set pid to item 2 of argv
      set t to first track of library playlist 1 whose persistent ID is pid
      play t
    else if action is "play_playlist" then
      set pid to item 2 of argv
      set pl to first user playlist whose persistent ID is pid
      play pl
    else if action is "position" then
      set player position to (item 2 of argv) as real
    else if action is "play" then
      play
    else if action is "pause" then
      pause
    else if action is "playpause" then
      playpause
    else if action is "next" then
      next track
    else if action is "previous" then
      previous track
    else if action is "stop" then
      stop
    else
      error "unknown control action"
    end if
    return player state as text
  end tell
end run
`

const createPlaylistScript = sanitizeScript + `
on run argv
  set US to (ASCII character 31)
  set pname to item 1 of argv
  set pdesc to item 2 of argv
  tell application "Music"
    set lib to library playlist 1
    -- Resolve every track before the playlist exists: a stale or unknown id must leave nothing behind, never an
    -- empty or partial playlist the user already confirmed.
    set foundTracks to {}
    set missingIDs to ""
    repeat with i from 3 to count of argv
      set pid to item i of argv
      set found to missing value
      try
        set found to first track of lib whose persistent ID is pid
      end try
      if found is missing value then
        -- The id came from the caller and may itself contain a separator: escape it like any other value so the
        -- answer stays one record and the caller can still say not_found.
        if missingIDs is not "" then set missingIDs to missingIDs & ","
        set missingIDs to missingIDs & (my sanitize(pid))
      else
        set end of foundTracks to found
      end if
    end repeat
    if missingIDs is not "" then return "missing" & US & missingIDs
    set pl to make new user playlist with properties {name:pname, description:pdesc}
    set added to 0
    set failedIDs to ""
    repeat with i from 1 to count of foundTracks
      set found to item i of foundTracks
      try
        duplicate found to pl
        set added to added + 1
      on error
        if failedIDs is not "" then set failedIDs to failedIDs & ","
        set failedIDs to failedIDs & (my sanitize(persistent ID of found))
      end try
    end repeat
    return "ok" & US & (my sanitize(persistent ID of pl)) & US & (my sanitize(name of pl)) & US & ((count of tracks of pl) as text) & US & (added as text) & US & failedIDs
  end tell
end run
`

const artworkScript = `
on run argv
  set itemKind to item 1 of argv
  set pid to item 2 of argv
  set outPath to item 3 of argv
  tell application "Music"
    if itemKind is "playlist" then
      set itemRef to first user playlist whose persistent ID is pid
    else
      set itemRef to missing value
      try
        set itemRef to first track of library playlist 1 whose persistent ID is pid
      end try
      if itemRef is missing value then
        try
          set playingTrack to current track
          if (persistent ID of playingTrack) is pid then set itemRef to playingTrack
        end try
      end if
      if itemRef is missing value then return "none"
    end if
    try
      set d to raw data of artwork 1 of itemRef
    on error
      return "none"
    end try
  end tell
  set f to open for access (POSIX file outPath) with write permission
  set eof f to 0
  write d to f
  close access f
  return "ok"
end run
`

// lyricsScript answers the words of the track the caller names, not simply whatever is playing: the provider caches
// the answer under that id, so reading the current track would store another song's words under this one. The current
// track is used when it is the requested one (that also covers a stream that is not in the library); no argument keeps
// the old "what is playing" behavior.
const lyricsScript = `
on run argv
  set wantID to ""
  try
    set wantID to item 1 of argv as text
  end try
  tell application "Music"
    set t to missing value
    try
      set t to current track
      if wantID is not "" and (persistent ID of t) is not wantID then set t to missing value
    end try
    if t is missing value and wantID is not "" then
      try
        set t to first track of library playlist 1 whose persistent ID is wantID
      end try
    end if
    if t is missing value then return ""
    set lyricText to ""
    try
      set lyricText to lyrics of t
    end try
    if lyricText is missing value then return ""
    return lyricText
  end tell
end run
`

// records splits a script's output into records and fields. sanitize() in the scripts escaped the separators (and the
// backslash that introduces an escape) inside a value, so a title containing them cannot shift fields; unescapeField
// reverses that. Fields keep their exact text — a title may legitimately start with a space or read "missing value" —
// and only the newline osascript ends the output with is dropped (osaRunner already trims it, a fake may not).
func records(out string) [][]string {
	out = strings.Trim(out, "\r\n")
	if out == "" {
		return nil
	}
	raw := strings.Split(out, recordSep)
	rows := make([][]string, 0, len(raw))
	for _, r := range raw {
		if r == "" {
			continue
		}
		fields := strings.Split(r, fieldSep)
		for i := range fields {
			fields[i] = unescapeField(fields[i])
		}
		rows = append(rows, fields)
	}
	return rows
}

// unescapeField reverses sanitize(): "\\" is a literal backslash, "\u001e" and "\u001f" are the separators. Any other
// backslash is kept as it is, so text that never went through sanitize() still reads back unchanged.
func unescapeField(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		switch s[i+1] {
		case '\\':
			b.WriteByte('\\')
			i++
		case 'u':
			switch {
			case strings.HasPrefix(s[i:], `\u001e`):
				b.WriteByte(0x1e)
				i += 5
			case strings.HasPrefix(s[i:], `\u001f`):
				b.WriteByte(0x1f)
				i += 5
			default:
				b.WriteByte('\\')
			}
		default:
			b.WriteByte('\\')
		}
	}
	return b.String()
}
