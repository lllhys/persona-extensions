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
// never pasted into AppleScript source.
const (
	fieldSep  = "\x1f"
	recordSep = "\x1e"
)

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

const statusScript = `
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
  return "1" & US & v & US & (trackCount as text) & US & (playlistCount as text)
end tell
`

const nowScript = `
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
  return stateText & US & (persistent ID of t) & US & (name of t) & US & (artist of t) & US & (album of t) & US & (pos as text) & US & (dur as text)
end tell
`

const recentScript = `
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
      set out to out & (persistent ID of t) & US & (name of t) & US & (artist of t) & US & (album of t) & US & (duration of t as integer) & US & (pc as text) & US & pd & US & (rt as text) & RS
      set n to n + 1
      if n ≥ 2000 then exit repeat
    end repeat
    return out
  end tell
end run
`

const playlistsScript = `
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
        set out to out & (persistent ID of pl) & US & (name of pl) & US & ((count of tracks of pl) as text) & US & ((duration of pl as integer) as text) & US & descr & US & cls & RS
      end try
    end repeat
    return out
  end tell
end run
`

const playlistScript = `
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
    set out to "P" & US & (persistent ID of pl) & US & (name of pl) & US & (totalTracks as text) & US & ((duration of pl as integer) as text) & US & descr & RS
    set lastIndex to startAt + lim
    if lastIndex > totalTracks then set lastIndex to totalTracks
    if startAt < totalTracks then
      repeat with i from (startAt + 1) to lastIndex
        set t to item i of trackList
        set out to out & "T" & US & (persistent ID of t) & US & (name of t) & US & (artist of t) & US & (album of t) & US & (duration of t as integer) & US & (played count of t) & US & (rating of t) & RS
      end repeat
    end if
    return out
  end tell
end run
`

const searchScript = `
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
      set out to out & (persistent ID of t) & US & (name of t) & US & (artist of t) & US & (album of t) & US & (duration of t as integer) & US & (played count of t) & US & (rating of t) & RS
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

const createPlaylistScript = `
on run argv
  set US to (ASCII character 31)
  set pname to item 1 of argv
  set pdesc to item 2 of argv
  tell application "Music"
    set pl to make new user playlist with properties {name:pname, description:pdesc}
    set added to 0
    repeat with i from 3 to count of argv
      set pid to item i of argv
      try
        set t to first track of library playlist 1 whose persistent ID is pid
        duplicate t to pl
        set added to added + 1
      end try
    end repeat
    return (persistent ID of pl) & US & (name of pl) & US & ((count of tracks of pl) as text) & US & (added as text)
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

const lyricsScript = `
tell application "Music"
  set t to missing value
  try
    set t to current track
  end try
  if t is missing value then return ""
  set lyricText to ""
  try
    set lyricText to lyrics of t
  end try
  if lyricText is missing value then return ""
  return lyricText
end tell
`

// records splits a script's output into records and fields.
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
			v := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(fields[i], fieldSep, " "), recordSep, " "))
			if v == "missing value" {
				v = ""
			}
			fields[i] = v
		}
		rows = append(rows, fields)
	}
	return rows
}
