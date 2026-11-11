package kernel

import (
	"fmt"
	"slices"
)

// mainLine is the stages whose phase is not dropped, in flow order.
func (t *Type) mainLine() []Stage {
	var main []Stage
	for _, s := range t.Flow {
		if s.Phase != Dropped {
			main = append(main, s)
		}
	}
	return main
}

// phaseRank orders the phases of the main line; dropped is not on it.
func phaseRank(p Phase) int {
	switch p {
	case Todo:
		return 0
	case Doing:
		return 1
	case Done:
		return 2
	}
	return -1
}

// Stage returns the stage with the given name.
func (t *Type) Stage(name string) (Stage, bool) {
	for _, s := range t.Flow {
		if s.Name == name {
			return s, true
		}
	}
	return Stage{}, false
}

// Moves are the stages a status may change to from the given one, in flow order. On the main line: the neighbours
// and every dropped stage. From a dropped stage: every main-line stage. Nothing else.
func (t *Type) Moves(from string) []string {
	cur, ok := t.Stage(from)
	if !ok {
		return nil
	}
	main := t.mainLine()
	idx := -1
	for i, s := range main {
		if s.Name == from {
			idx = i
		}
	}
	var out []string
	for _, s := range t.Flow {
		if s.Name == from {
			continue
		}
		switch {
		case cur.Phase == Dropped:
			if s.Phase != Dropped {
				out = append(out, s.Name)
			}
		case s.Phase == Dropped:
			out = append(out, s.Name)
		case idx >= 0 && (mainAt(main, idx-1) == s.Name || mainAt(main, idx+1) == s.Name):
			out = append(out, s.Name)
		}
	}
	return out
}

// mainAt is the name of the main-line stage at i, or "" outside the line.
func mainAt(main []Stage, i int) string {
	if i < 0 || i >= len(main) {
		return ""
	}
	return main[i].Name
}

// Next is the advance target: the next main-line stage. It is absent at the end of the line and from a dropped
// stage.
func (t *Type) Next(from string) (string, bool) {
	cur, ok := t.Stage(from)
	if !ok || cur.Phase == Dropped {
		return "", false
	}
	main := t.mainLine()
	i := slices.IndexFunc(main, func(s Stage) bool { return s.Name == from })
	if i < 0 || i+1 >= len(main) {
		return "", false
	}
	return main[i+1].Name, true
}

// Position is the place along the main line, 0..1; undefined for a dropped stage or a type without a line.
func (t *Type) Position(from string) (float64, bool) {
	main := t.mainLine()
	if len(main) < 2 {
		return 0, false
	}
	i := slices.IndexFunc(main, func(s Stage) bool { return s.Name == from })
	if i < 0 {
		return 0, false
	}
	return float64(i) / float64(len(main)-1), true
}

// checkFlow verifies the validity rules of a flow, one at a time: a flow is empty or has 2 to 8 stages with distinct
// names of at most 12 runes; the first stage is on the main line; the main line has at least two stages; and along
// the main line the phases never go backwards.
func (t *Type) checkFlow() error {
	if len(t.Flow) == 0 {
		return nil
	}
	if len(t.Flow) < 2 || len(t.Flow) > MaxStages {
		return fmt.Errorf("类型「%s」的流程需要 2 到 %d 个阶段", t.Name, MaxStages)
	}
	names := map[string]bool{}
	for _, s := range t.Flow {
		if !inRunes(s.Name, 1, MaxShort) {
			return fmt.Errorf("阶段「%s」的名称应为 1 到 %d 个字", s.Name, MaxShort)
		}
		if names[s.Name] {
			return fmt.Errorf("阶段「%s」重复", s.Name)
		}
		names[s.Name] = true
		if s.Phase != Todo && s.Phase != Doing && s.Phase != Done && s.Phase != Dropped {
			return fmt.Errorf("阶段「%s」的阶段「%s」不存在", s.Name, s.Phase)
		}
	}
	main := t.mainLine()
	if t.Flow[0].Phase == Dropped {
		return fmt.Errorf("类型「%s」的第一个阶段必须在主线上", t.Name)
	}
	if len(main) < 2 {
		return fmt.Errorf("类型「%s」的主线至少需要 2 个阶段", t.Name)
	}
	for i := 1; i < len(main); i++ {
		if phaseRank(main[i].Phase) < phaseRank(main[i-1].Phase) {
			return fmt.Errorf("阶段「%s」不能早于「%s」", main[i].Name, main[i-1].Name)
		}
	}
	return nil
}
