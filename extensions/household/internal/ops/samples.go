package ops

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
)

// LoadSamples creates the sample records of model.Samples, behind 载入示例, through Save so that the household's defaults
// apply to them as to any record. It refuses a household space that already has records, and returns how many it
// created.
func LoadSamples(b *kernel.Base, now time.Time, loc *time.Location) (int, error) {
	n := 0
	err := tx(b, now, loc, func(nb *kernel.Base, sp *kernel.Space, today model.Day) error {
		if len(sp.Entities) > 0 {
			return errors.New("家庭管家里已有记录，载入示例只能在空的家庭管家中进行")
		}
		ids := map[string]string{} // sample ref -> id of the record made for it
		for _, s := range model.Samples() {
			values, err := resolve(s.Values, ids, today)
			if err != nil {
				return fmt.Errorf("载入示例「%s」：%w", s.Title, err)
			}
			in := kernel.EntityInput{Type: s.Type, Title: strPtr(s.Title), Values: values}
			if s.Status != "" {
				in.Status = strPtr(s.Status)
			}
			e, err := save(sp, nb, in, now, today)
			if err != nil {
				return fmt.Errorf("载入示例「%s」：%w", s.Title, err)
			}
			ids[s.Ref] = e.ID
			n++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// resolve turns the values of a sample into record values: "@ref" is the id of the record made for the ref, a list of
// them is a list of ids, and "+3d" or "-2d" is the day that many days from today.
func resolve(values map[string]any, ids map[string]string, today model.Day) (map[string]any, error) {
	out := make(map[string]any, len(values))
	for key, v := range values {
		var err error
		switch x := v.(type) {
		case string:
			v, err = resolveString(x, ids, today)
		case []string:
			list := make([]string, 0, len(x))
			for _, s := range x {
				id, refErr := resolveRef(s, ids)
				if refErr != nil {
					return nil, refErr
				}
				list = append(list, id)
			}
			v = list
		}
		if err != nil {
			return nil, err
		}
		out[key] = v
	}
	return out, nil
}

// resolveString resolves one string value of a sample: a reference, a relative day, or the string itself.
func resolveString(s string, ids map[string]string, today model.Day) (any, error) {
	if strings.HasPrefix(s, "@") {
		return resolveRef(s, ids)
	}
	if n, ok := relativeDays(s); ok {
		return string(today.Add(n)), nil
	}
	return s, nil
}

// resolveRef is the id of the record that the "@ref" was made for; the ref must come earlier in the samples.
func resolveRef(s string, ids map[string]string) (string, error) {
	ref := strings.TrimPrefix(s, "@")
	id, ok := ids[ref]
	if !ok {
		return "", fmt.Errorf("示例引用「%s」不存在", ref)
	}
	return id, nil
}

// relativeDays reads "+3d" or "-2d" as a number of days.
func relativeDays(s string) (int, bool) {
	if len(s) < 3 || (s[0] != '+' && s[0] != '-') || s[len(s)-1] != 'd' {
		return 0, false
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	return n, err == nil
}
