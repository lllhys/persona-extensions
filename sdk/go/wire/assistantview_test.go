package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAssistantExamples(t *testing.T) {
	files, _ := filepath.Glob("../../../schema/extension/examples/assistant/*.json")
	if len(files) < 7 {
		t.Fatal("assistant examples missing")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			v, err := ParseAssistantView(raw)
			if err != nil {
				t.Fatal(err)
			}
			out, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			var want, got any
			_ = json.Unmarshal(raw, &want)
			_ = json.Unmarshal(out, &got)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("round trip differs\nwant %s\n got %s", raw, out)
			}
		})
	}
}

func TestAssistantViewRefuses(t *testing.T) {
	cases := map[string]string{
		"null":                  `null`,
		"fallback":              `{"sections":[]}`,
		"blank fallback":        `{"fallback":"  ","sections":[]}`,
		"bad component":         `{"fallback":"summary","sections":[{"items":[{"type":"text"}]}]}`,
		"unknown component":     `{"fallback":"summary","sections":[{"items":[{"type":"future"}]}]}`,
		"bare extension action": `{"fallback":"summary","sections":[{"items":[{"type":"button","title":"choose","action":{"id":"x"}}]}]}`,
		"two actions":           `{"fallback":"summary","sections":[{"items":[{"type":"button","title":"choose","action":{"id":"x","reply":"yes","pop":true}}]}]}`,
		"submit open":           `{"fallback":"summary","sections":[{"items":[{"type":"button","title":"choose","action":{"id":"x","open":"https://example.com","submit":true}}]}]}`,
		"missing page":          `{"fallback":"summary","sections":[{"items":[{"type":"button","title":"detail","action":{"id":"x","push":{"page":"missing"}}}]}]}`,
		"extension push":        `{"fallback":"summary","sections":[{"items":[{"type":"button","title":"detail","action":{"id":"x","push":{"page":"p","ui":"other"}}}]}],"pages":{"p":{"sections":[]}}}`,
		"nested pages":          `{"fallback":"summary","sections":[],"pages":{"p":{"sections":[],"pages":{"q":{"sections":[]}}}}}`,
		"bad page name":         `{"fallback":"summary","sections":[],"pages":{"../p":{"sections":[]}}}`,
		"duplicate action":      `{"fallback":"summary","sections":[{"items":[{"type":"button","title":"one","action":{"id":"x","reply":"one"}}]}],"pages":{"p":{"sections":[{"items":[{"type":"button","title":"two","action":{"id":"x","reply":"two"}}]}]}}}`,
		"secret":                `{"fallback":"summary","sections":[{"items":[{"type":"field","name":"password","label":"Password","kind":"secret"}]}]}`,
		"bad html height":       `{"fallback":"summary","sections":[{"items":[{"type":"html","html":"<p>ok</p>","height":9999}]}]}`,
		"wrong row cells":       `{"fallback":"summary","sections":[{"items":[{"type":"table","columns":[{"title":"x"}],"rows":[["a","b"]]}]}]}`,
		"deep JSON":             strings.Repeat(`{"pages":{"p":`, 20) + `{}` + strings.Repeat(`}}`, 20),
		"large":                 `{"fallback":"summary","sections":[],"ignored":"` + strings.Repeat("a", AssistantViewMost) + `"}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseAssistantView([]byte(raw)); err == nil {
				t.Fatal("accepted invalid assistant view")
			}
		})
	}
}

func TestAssistantComponentShorthand(t *testing.T) {
	v, err := ParseAssistantView([]byte(`{"type":"metric","label":"Count","value":"3","fallback":"Count is 3"}`))
	if err != nil || v.Fallback != "Count is 3" || len(v.Sections) != 1 || v.Sections[0].Items[0].Type != "metric" {
		t.Fatalf("got %#v, %v", v, err)
	}
	if _, err := ParseAssistantView([]byte(`{"type":"metric","label":"Count","value":"3"}`)); err == nil {
		t.Fatal("accepted shorthand without fallback")
	}
}

func TestAssistantOpen(t *testing.T) {
	for _, target := range []string{"https://example.com/page", "persona://session/example-machine/abc", "jarvis://session/example-machine/abc", "persona://task/task_1", "persona://kb/notes/example.md", "persona://file/example-machine/%2Ftmp%2Fexample.txt"} {
		if err := CheckAssistantOpen(target); err != nil {
			t.Errorf("%s: %v", target, err)
		}
	}
	for _, target := range []string{"javascript:alert(1)", "http://example.com", "https://user:pass@example.com", "https://:443", "https://example.com\n", "persona://settings", "persona://session/a", "persona://session/a/b/c", "persona://task/a?x=1", "persona://kb/../secret", "persona://kb/%2e%2e/secret", "persona://kb/a%2Fb", "persona://kb/%E0%", "persona://file/machine/relative", "persona://file/a%2Fb/%2Ftmp", "persona://task/%0a"} {
		if err := CheckAssistantOpen(target); err == nil {
			t.Errorf("accepted %q", target)
		}
	}
}

func TestStripAssistantFields(t *testing.T) {
	v := View{Fallback: "summary", Pages: map[string]View{"detail": {Sections: []Section{}}}, Sections: []Section{{Items: []Component{
		{Type: "row", Title: "readable", Action: &Action{ID: "reply", Reply: "yes"}},
		{Type: "button", Title: "reply", Action: &Action{ID: "reply-button", Reply: "yes"}},
		{Type: "html", Html: "<p>untrusted</p>"},
		{Type: "button", Title: "extension", Action: &Action{ID: "extension", Push: &Push{UI: "detail", State: "state"}}},
	}}}}
	dropped := StripAssistantFields(&v)
	if v.Fallback != "" || v.Pages != nil || len(v.Sections[0].Items) != 2 || v.Sections[0].Items[0].Action != nil || len(dropped) != 4 {
		t.Fatalf("got %#v, dropped %v", v, dropped)
	}
	if err := CheckView(&v); err != nil {
		t.Fatal(err)
	}
	if a := v.Sections[0].Items[1].Action; a == nil || a.Push == nil || a.Push.UI != "detail" || a.Push.State != "state" {
		t.Fatal("extension push changed")
	}
}
