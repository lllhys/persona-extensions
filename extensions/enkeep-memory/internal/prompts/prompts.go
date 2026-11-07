// Package prompts holds the curator's four prompts and the JSON schemas of their answers, one file each, embedded
// in the program. Their versions are in the schema package; golden tests pin their text.
package prompts

import _ "embed"

//go:embed triage.md
var Triage string

//go:embed daily.md
var Daily string

//go:embed weekly.md
var Weekly string

//go:embed memory.md
var Memory string

//go:embed triage.schema.json
var TriageSchema []byte

//go:embed daily.schema.json
var DailySchema []byte

//go:embed weekly.schema.json
var WeeklySchema []byte

//go:embed memory.schema.json
var MemorySchema []byte
