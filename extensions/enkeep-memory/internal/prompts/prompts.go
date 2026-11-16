// Package prompts holds the curator's prompts and the JSON schemas of their answers, one file each, embedded
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

//go:embed synth.md
var Synth string

// Rewrite asks for rejected wording once more: the target language, the word for the user (daily/8 checks).
//
//go:embed rewrite.md
var Rewrite string

//go:embed triage.schema.json
var TriageSchema []byte

//go:embed daily.schema.json
var DailySchema []byte

//go:embed weekly.schema.json
var WeeklySchema []byte

//go:embed memory.schema.json
var MemorySchema []byte

//go:embed synth.schema.json
var SynthSchema []byte

//go:embed rewrite.schema.json
var RewriteSchema []byte
