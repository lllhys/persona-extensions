package wire

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	ItemsMost  = 300
	StringMost = 2000
	StateMost  = 16 << 10
	SeriesMost = 4
	PointsMost = 120
	StatsMost  = 6
)

var tones = append([]string{""}, Tones...)

// CheckView holds a View to what the apps draw: first to the schema (known component types with what they need, short
// strings, https links, tones and styles from the fixed lists), then to what the schema cannot say (a page's size, a
// field's name once, a chart's series alike). A View that does not fit is refused, not half drawn.
func CheckView(v *View) error {
	if v == nil {
		return errors.New("no view")
	}
	if err := Validate("View", v); err != nil {
		var ve *ValidationError
		if errors.As(err, &ve) {
			return errors.New(where(v, ve.Errors[0]))
		}
		return err
	}
	if len(v.State) > StateMost {
		return fmt.Errorf("state: more than %d bytes", StateMost)
	}
	for _, a := range Actions(v) {
		if a.Push != nil && len(a.Push.State) > StateMost {
			return fmt.Errorf("action %s: the state it pushes is more than %d bytes", a.ID, StateMost)
		}
	}
	n := 0
	names := map[string]bool{}
	keys := map[string]bool{}
	if v.Search != nil {
		names["q"] = true
	}
	if f := v.Filter; f != nil {
		if names[f.Name] {
			return fmt.Errorf("filter: field %q twice (q is the search's)", f.Name)
		}
		names[f.Name] = true
		if !slices.ContainsFunc(f.Choices, func(c Choice) bool { return c.Value == f.Value }) {
			return errors.New("filter: value is one of its choices")
		}
	}
	for i, s := range v.Sections {
		for j, e := range s.Items {
			if n++; n > ItemsMost {
				return fmt.Errorf("more than %d items", ItemsMost)
			}
			var err error
			if e.Key != "" {
				if keys[e.Key] {
					return fmt.Errorf("section %d item %d (%s): key %q twice", i, j, e.Type, e.Key)
				}
				keys[e.Key] = true
			}
			switch e.Type {
			case "link":
				if u, perr := url.Parse(e.URL); perr != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
					err = errors.New("link needs an https url")
				}
			case "field":
				switch {
				case names[e.Name]:
					err = fmt.Errorf("field %q twice", e.Name)
				case e.Kind == "choice" && len(e.Choices) == 0:
					err = errors.New("a choice field needs choices")
				case e.Kind == "secret" && e.Value != "":
					err = errors.New("a secret field is never filled in")
				}
				names[e.Name] = true
			case "segmented":
				switch {
				case names[e.Name]:
					err = fmt.Errorf("field %q twice", e.Name)
				case !slices.ContainsFunc(e.Choices, func(c Choice) bool { return c.Value == e.Value }):
					err = errors.New("segmented: value is one of its choices")
				case slices.ContainsFunc(e.Choices, func(c Choice) bool { return utf8.RuneCountInString(cmp.Or(c.Title, c.Value)) > 12 }):
					err = errors.New("segmented: a choice has at most 12 characters")
				}
				names[e.Name] = true
			case "chart":
				err = checkChart(e)
			case "stat_grid":
				err = checkStats(e.Stats)
			case "row":
				if (e.Icon != "") && (e.Avatar != nil || e.Thumbnail != nil) || e.Avatar != nil && e.Thumbnail != nil {
					err = errors.New("a row has at most one of icon, avatar and thumbnail")
				} else if a := e.Avatar; a != nil && a.URL == "" && a.Text == "" {
					err = errors.New("an avatar has a url or a text")
				}
			case "table":
				for k, r := range e.Rows {
					if len(r) != len(e.Columns) {
						err = fmt.Errorf("row %d has %d cells for %d columns", k, len(r), len(e.Columns))
						break
					}
				}
			case "markdown":
				err = checkMarkdown(e.Text)
			}
			if err != nil {
				return fmt.Errorf("section %d item %d (%s): %v", i, j, e.Type, err)
			}
		}
	}
	return nil
}

// where turns the schema's "$.sections[1].items[2].tone: …" into "section 1 item 2 (metric): tone: …".
func where(v *View, msg string) string {
	var i, j int
	if k, err := fmt.Sscanf(msg, "$.sections[%d].items[%d]", &i, &j); err == nil && k == 2 && i < len(v.Sections) && j < len(v.Sections[i].Items) {
		rest := msg[len(fmt.Sprintf("$.sections[%d].items[%d]", i, j)):]
		rest = strings.TrimPrefix(strings.TrimPrefix(rest, "."), ": ")
		return fmt.Sprintf("section %d item %d (%s): %s", i, j, v.Sections[i].Items[j].Type, rest)
	}
	return strings.TrimPrefix(strings.TrimPrefix(msg, "$."), "$: ")
}

func checkChart(e Component) error {
	if !slices.Contains(ChartKinds, e.Kind) {
		return errors.New("chart kind is bar or line")
	}
	if len(e.Series) == 0 || len(e.Series) > SeriesMost {
		return fmt.Errorf("a chart has 1 to %d series", SeriesMost)
	}
	first := e.Series[0].Points
	if len(first) == 0 || len(first) > PointsMost {
		return fmt.Errorf("a series has 1 to %d points", PointsMost)
	}
	for k, s := range e.Series {
		if utf8.RuneCountInString(s.Name) > 40 || !slices.Contains(tones, s.Tone) {
			return fmt.Errorf("series %d: a name of at most 40 characters; tone is neutral, positive, caution or danger", k)
		}
		if len(e.Series) > 1 && s.Name == "" {
			return fmt.Errorf("series %d: series of a chart with more than one are named", k)
		}
		if len(s.Points) != len(first) {
			return fmt.Errorf("series %d: every series has the same labels", k)
		}
		for n, pt := range s.Points {
			if pt.Label != first[n].Label {
				return fmt.Errorf("series %d point %d: every series has the same labels, in the same order", k, n)
			}
			if utf8.RuneCountInString(pt.Label) > 40 || utf8.RuneCountInString(pt.Display) > 40 {
				return fmt.Errorf("series %d point %d: label and display have at most 40 characters", k, n)
			}
			if math.IsNaN(pt.Value) || math.IsInf(pt.Value, 0) {
				return fmt.Errorf("series %d point %d: a number", k, n)
			}
		}
	}
	return nil
}

func checkStats(stats []Stat) error {
	if len(stats) == 0 || len(stats) > StatsMost {
		return fmt.Errorf("a stat_grid has 1 to %d stats", StatsMost)
	}
	for k, st := range stats {
		if st.Label == "" || st.Value == "" {
			return fmt.Errorf("stat %d: label and value", k)
		}
		if utf8.RuneCountInString(st.Label)+utf8.RuneCountInString(st.Value)+utf8.RuneCountInString(st.Detail) > StringMost {
			return fmt.Errorf("stat %d: too long", k)
		}
		if !slices.Contains(tones, st.Tone) {
			return fmt.Errorf("stat %d: tone is neutral, positive, caution or danger", k)
		}
	}
	return nil
}

// mdImage includes inline, reference and shortcut images.
var mdImage = regexp.MustCompile(`!\[[^\]]*\]`)

// Reference links are outside the safe inline subset; rejecting definitions also covers multiline destinations.
var mdReference = regexp.MustCompile(`(?m)^ {0,3}\[[^\]\n]+\]:`)

// mdAutolink is a CommonMark URI autolink, not an HTML tag.
var mdAutolink = regexp.MustCompile(`<([A-Za-z][A-Za-z0-9+.-]*:[^<>\s]*)>`)

// mdLink is a Markdown link's or image's target: [text](target "title").
var mdLink = regexp.MustCompile(`\]\(\s*<?([^)\s>]*)`)

// mdCode is Markdown's code, a block or a span: what looks like a link in it is text.
var mdCode = regexp.MustCompile("(?s)```.*?(```|$)|`[^`\n]*`")

// checkMarkdown holds a markdown to its subset where it matters: links are https (the apps open nothing else), no
// images. HTML is drawn as the text it is.
func checkMarkdown(text string) error {
	text = mdCode.ReplaceAllString(text, "")
	if mdImage.MatchString(text) {
		return errors.New("markdown has no images")
	}
	if mdReference.MatchString(text) {
		return errors.New("markdown reference links are unsupported; use inline https links")
	}
	links := append(mdLink.FindAllStringSubmatch(text, -1), mdAutolink.FindAllStringSubmatch(text, -1)...)
	for _, m := range links {
		if u, err := url.Parse(m[1]); err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
			return fmt.Errorf("markdown links are https: %q", m[1])
		}
	}
	return nil
}

// Actions are a view's actions (its components', its search's, filter's and sections' more) and where each is.
func Actions(v *View) []*Action {
	var out []*Action
	if v == nil {
		return nil
	}
	if v.Search != nil {
		out = append(out, &v.Search.Action)
	}
	if v.Filter != nil {
		out = append(out, &v.Filter.Action)
	}
	for i := range v.Sections {
		s := &v.Sections[i]
		for j := range s.Items {
			e := &s.Items[j]
			if e.Action != nil {
				out = append(out, e.Action)
			}
			if e.Button != nil {
				out = append(out, &e.Button.Action)
			}
		}
		if s.More != nil {
			out = append(out, &s.More.Action)
		}
	}
	return out
}
