package curate

import (
	"fmt"
	"strings"
)

// relationPhrases words the built-in relation types as a sentence in the output language (5.0): the ⑤ entry is read
// by the user on the page and by the assistant in the knowledge base, so "A owns B" is written out, not left as the
// type's identifier. %[1]s is the subject, %[2]s the target.
var relationPhrases = map[string]map[string]string{
	"zh": {
		"member_of": "%[1]s属于%[2]s", "owns": "%[1]s负责%[2]s", "depends_on": "%[1]s依赖%[2]s", "decides": "%[1]s拍板%[2]s",
		"collaborates_with": "%[1]s和%[2]s合作", "means": "%[1]s指的是%[2]s", "reports_to": "%[1]s向%[2]s汇报",
		"mentors": "%[1]s带%[2]s", "serves": "%[1]s为%[2]s提供服务", "same_circle": "%[1]s和%[2]s在同一个圈子",
		"blocked_by": "%[1]s卡在%[2]s上", "waiting_on": "%[1]s在等%[2]s", "decided": "%[1]s定了%[2]s", "about": "%[1]s关于%[2]s",
		"committed": "%[1]s答应了%[2]s", "assigned": "%[1]s被分派了%[2]s", "allocated": "%[1]s分到了%[2]s",
	},
	"ja": {
		"member_of": "%[1]sは%[2]sに所属", "owns": "%[1]sが%[2]sを担当", "depends_on": "%[1]sは%[2]sに依存", "decides": "%[1]sが%[2]sを決める",
		"collaborates_with": "%[1]sと%[2]sが協力", "means": "%[1]sは%[2]sのこと", "reports_to": "%[1]sは%[2]sに報告",
		"mentors": "%[1]sが%[2]sを指導", "serves": "%[1]sが%[2]sにサービスを提供", "same_circle": "%[1]sと%[2]sは同じ仲間",
		"blocked_by": "%[1]sは%[2]sで止まっている", "waiting_on": "%[1]sは%[2]sを待っている", "decided": "%[1]sが%[2]sを決めた",
		"about": "%[1]sは%[2]sについて", "committed": "%[1]sが%[2]sを約束", "assigned": "%[1]sに%[2]sが割り当て", "allocated": "%[1]sに%[2]sが配分",
	},
	"en": {
		"member_of": "%[1]s belongs to %[2]s", "owns": "%[1]s is responsible for %[2]s", "depends_on": "%[1]s depends on %[2]s",
		"decides": "%[1]s decides %[2]s", "collaborates_with": "%[1]s works with %[2]s", "means": "%[1]s means %[2]s",
		"reports_to": "%[1]s reports to %[2]s", "mentors": "%[1]s mentors %[2]s", "serves": "%[1]s serves %[2]s",
		"same_circle": "%[1]s and %[2]s are in the same circle", "blocked_by": "%[1]s is blocked by %[2]s",
		"waiting_on": "%[1]s is waiting on %[2]s", "decided": "%[1]s decided %[2]s", "about": "%[1]s is about %[2]s",
		"committed": "%[1]s committed to %[2]s", "assigned": "%[1]s was assigned %[2]s", "allocated": "%[1]s was allocated %[2]s",
	},
}

// relationText is a relation as a sentence in lang. A type the user's table learned (no phrase for it) reads
// "subject —type→ target", its words spaced out.
func relationText(lang, subject, predicate, target string) string {
	key := "en"
	switch {
	case strings.HasPrefix(lang, "zh"):
		key = "zh"
	case strings.HasPrefix(lang, "ja"):
		key = "ja"
	}
	if f, ok := relationPhrases[key][predicate]; ok {
		return fmt.Sprintf(f, subject, target)
	}
	return fmt.Sprintf("%s —%s→ %s", subject, strings.ReplaceAll(predicate, "_", " "), target)
}
