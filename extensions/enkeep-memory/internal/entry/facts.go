package entry

import (
	"slices"
	"strings"
)

// selfFactTypes are the persona types that are facts about the person rather than views or habits: one plain
// statement of the user's own settles them (the prompt's keys, and the class names they are filed under).
var selfFactTypes = []string{"role", "responsibility", "situation", "current_work", "goal", "background",
	"角色", "职位", "职责", "处境", "在做的事", "目标", "背景"}

// SelfFact: a ⑥ entry stating a fact about the user (role, responsibility, situation, current work, a goal, the
// background) in the user's own words: a line by the user, not a bare agreement. It needs no second sighting; a view,
// a value, a preference or a habit does.
func SelfFact(e *Entry) bool {
	if e.Facet != Persona || e.Inferred || !slices.Contains(selfFactTypes, strings.ToLower(strings.TrimSpace(e.Type))) {
		return false
	}
	return slices.ContainsFunc(e.Evidence, func(x Evidence) bool { return x.Who == WhoSelf && x.Role == "user" && x.Quote != "" })
}

// PersonFact: a ⑤′ fact or commitment about someone, on that person's own line or the user's (not only what others
// said, not a reading): it needs no second sighting. Evaluations and inferences do.
func PersonFact(e *Entry) bool {
	if e.Facet != People || e.Inferred || e.ReportedOnly || (e.Part != PartFact && e.Part != PartCommitment) {
		return false
	}
	return slices.ContainsFunc(e.Evidence, func(x Evidence) bool { return x.Quote != "" && !x.Reported })
}
