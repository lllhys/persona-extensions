package curate

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/schema"
)

// Own is the user's hand on an entry (or on a rule r-…, pattern p-… or trait t-… of the old layer, which is the
// entry it was migrated into): pin it (in force whatever the evidence does), unpin, delete it (the same content is
// not made again: a tombstone), restore it, or write its text (the user's version: never overwritten, 5.0). What the
// user did is kept through every later run. The old layer's own flags follow along, for the pages that still read it.
func (c *Curator) Own(id, action, text string) (string, error) {
	c.St.Lock()
	defer c.St.Unlock()
	s := c.St.S
	if _, err := c.Migrate(); err != nil {
		return "", err
	}
	eid := id
	if s.Entries[eid] == nil {
		eid = legacyIDs(s)[id]
	}
	if s.Entries[eid] == nil {
		return "", fmt.Errorf("没有 %s", id)
	}
	msg := ""
	switch action {
	case "edit":
		text = strings.TrimSpace(text)
		if n := utf8.RuneCountInString(text); n < minRuleText || n > schema.MaxRuleText {
			return "", fmt.Errorf("写 %d 到 %d 个字", minRuleText, schema.MaxRuleText)
		}
		if err := c.UserAct(eid, ActEdit, text); err != nil {
			return "", err
		}
		msg = "改好了，这一条以后不会被覆盖"
	case "pin":
		_ = c.UserAct(eid, ActRestore, "")
		if err := c.UserAct(eid, ActPin, ""); err != nil {
			return "", err
		}
		msg = "钉住了：一直有效，不会被覆盖"
	case "unpin":
		if err := c.UserAct(eid, ActUnpin, ""); err != nil {
			return "", err
		}
		msg = "取消钉住了"
	case "delete":
		_ = c.UserAct(eid, ActUnpin, "")
		if err := c.UserAct(eid, ActDelete, ""); err != nil {
			return "", err
		}
		msg = "删掉了：以后同样的内容不再生成"
	case "restore":
		if err := c.UserAct(eid, ActRestore, ""); err != nil {
			return "", err
		}
		msg = "恢复了"
	default:
		return "", fmt.Errorf("不认识的操作 %s", action)
	}
	e := s.Entries[eid]
	switch {
	case strings.HasPrefix(id, "r-") && s.Rules[id] != nil:
		r := s.Rules[id]
		r.Pinned, r.Deleted = e.Pinned, e.Deleted
		if e.User != nil {
			r.Text, r.Edited = e.User.Text, true
		}
	case strings.HasPrefix(id, "p-") && s.Patterns[id] != nil:
		s.Patterns[id].Pinned, s.Patterns[id].Deleted = e.Pinned, e.Deleted
	case strings.HasPrefix(id, "t-") && s.Traits[id] != nil:
		s.Traits[id].Pinned, s.Traits[id].Deleted = e.Pinned, e.Deleted
	}
	c.recount()
	return msg, c.St.Save()
}
