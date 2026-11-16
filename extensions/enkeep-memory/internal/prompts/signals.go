package prompts

import _ "embed"

// Prompts of the friction signals and the correction ledger (internal/signals) and of the exits' rewriting
// (internal/export). Instructions are English; the target language is a parameter of every call.
const (
	CorrectPrompt = "correct/2"   // is this user message a correction of the AI, and of what
	IssuesPrompt  = "issues/1"    // group the ledger's open issues that ask for the same change
	DigPrompt     = "dig/1"       // does a screened signal deserve a dig, and which folded stretch
	MinePrompt    = "mine/1"      // what a dug stretch teaches: ② approaches, pitfalls, boundaries; ① and ④ from the user's words
	ExportPrompt  = "export/4"    // one entry rewritten for an exit (design 6.4)
	EmptyPrompt   = "emptiness/3" // the empty-talk question asked of an exit line alone (5.0, 9.4); ⑥ lines: a specific fact?
)

//go:embed correct.md
var Correct string

//go:embed correct.schema.json
var CorrectSchema []byte

//go:embed issues.md
var Issues string

//go:embed issues.schema.json
var IssuesSchema []byte

//go:embed dig.md
var Dig string

//go:embed dig.schema.json
var DigSchema []byte

//go:embed mine.md
var Mine string

//go:embed mine.schema.json
var MineSchema []byte

//go:embed export.md
var Export string

//go:embed export.schema.json
var ExportSchema []byte

//go:embed emptiness.md
var Emptiness string

//go:embed emptiness.schema.json
var EmptinessSchema []byte
