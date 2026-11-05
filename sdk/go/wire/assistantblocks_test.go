package wire

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestAssistantViewFencesAndKeys(t *testing.T) {
	valid := `{"fallback":"Example","sections":[]}`
	text := "Intro\n```persona-card\n{\"type\":\"stat\",\"title\":\"Old\",\"value\":3}\n```\n" +
		"```persona-view\ninvalid\n```\n" +
		"~~~persona-view\n" + valid + "\n~~~\n" +
		"````markdown\n```persona-view\n" + valid + "\n```\n````\n" +
		"````persona-view\n" + valid + "\n````\n"
	blocks := AssistantViewBlocks(text)
	if len(blocks) != 4 {
		t.Fatalf("blocks %+v", blocks)
	}
	want := []int{-1, 0, 1, 2}
	for i, b := range blocks {
		if b.Index != want[i] || !b.Complete {
			t.Fatalf("block %+v", b)
		}
	}
	if _, err := blocks[1].Parse(); err == nil {
		t.Fatal("bad JSON accepted")
	}
	for _, i := range []int{2, 3} {
		if _, err := blocks[i].Parse(); err != nil {
			t.Fatal(err)
		}
	}
	if key := AssistantBlockKey("uuid#0", 2); key != "uuid#0/2" {
		t.Fatal(key)
	}
}

func TestAssistantFenceClosureAndLimit(t *testing.T) {
	valid := `{"fallback":"Example","sections":[]}`
	text := "```persona-view\r\n" + valid + "\r\n```\r\n"
	blocks := AssistantViewBlocks(text)
	if len(blocks) != 1 || !blocks[0].Complete {
		t.Fatalf("%+v", blocks)
	}
	if _, err := blocks[0].Parse(); err != nil {
		t.Fatal(err)
	}
	partial := AssistantViewBlocks("```persona-view\n" + valid)
	if len(partial) != 1 || partial[0].Complete {
		t.Fatal("partial fence treated as complete")
	}
	if _, err := partial[0].Parse(); err == nil {
		t.Fatal("partial fence accepted")
	}
	text = strings.Repeat("```persona-view\n"+valid+"\n```\n", 4)
	blocks = AssistantViewBlocks(text)
	if len(blocks) != 4 {
		t.Fatal("fourth block lost its index")
	}
	if _, err := blocks[3].Parse(); err == nil {
		t.Fatal("fourth block accepted")
	}
}

func TestAssistantCardFallbackDoesNotLeakJSON(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`{"fallback":"Readable","sections":[]}`, "Readable"},
		{`{"fallback":"Readable","sections":`, "Readable"},
		{`{"fallback":42}`, "〔卡片〕"},
		{`{"sections":[]}`, "〔卡片〕"},
		{`not JSON`, "〔卡片〕"},
	} {
		if got := AssistantViewFallback([]byte(tc.raw)); got != tc.want {
			t.Fatalf("%s: %q", tc.raw, got)
		}
	}
	for _, newline := range []string{"\n", "\r\n", "\r"} {
		text := "Conclusion" + newline + "```PERSONA-VIEW" + newline + `{"fallback":"Readable","sections":[]}` + newline + "```" + newline
		if got := AssistantCardText(text); got != "Conclusion"+newline+"Readable\n" {
			t.Fatalf("%q: %q", newline, got)
		}
	}
	text := "Conclusion\n```persona-view\n{\"fallback\":\"Readable\",\"sections\":[]}\n```\nMore"
	if got := AssistantCardText(text); got != "Conclusion\nReadable\nMore" {
		t.Fatalf("%q", got)
	}
	if got := AssistantCardText("Conclusion\n```persona-view\n{\"fallback\":\"Readable\",\"sections\":"); got != "Conclusion\nReadable\n" {
		t.Fatalf("%q", got)
	}
}

func TestSharedAssistantFenceVectors(t *testing.T) {
	raw, err := os.ReadFile("../../../schema/extension/examples/assistant-text/fences.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name     string
		Text     string
		Indexes  []int
		Complete []bool
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			blocks := AssistantViewBlocks(tc.Text)
			if len(blocks) != len(tc.Indexes) {
				t.Fatalf("blocks %+v", blocks)
			}
			for i, b := range blocks {
				if b.Index != tc.Indexes[i] || b.Complete != tc.Complete[i] {
					t.Fatalf("block %+v", b)
				}
			}
		})
	}
	if _, err := ParseAssistantView([]byte(`{"Fallback":"not the schema field","sections":[]}`)); err == nil {
		t.Fatal("accepted case-insensitive fallback that other languages cannot read")
	}
}
