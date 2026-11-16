package wire

import (
	"fmt"
	"strings"
	"testing"
)

// benchView is a page-sized view: the component kinds a plugin page mixes,
// repeated to the item count the hub sees from a busy page.
func benchView(items int) *View {
	section := Section{Title: "Section"}
	for index := range items {
		switch index % 4 {
		case 0:
			section.Items = append(section.Items, Component{
				Type: ComponentText, Key: fmt.Sprintf("text-%d", index),
				Title: fmt.Sprintf("Item %d", index), Text: "A line of body text.",
			})
		case 1:
			section.Items = append(section.Items, Component{
				Type: ComponentMarkdown, Key: fmt.Sprintf("md-%d", index),
				Text: "A [link](https://example.com) and `code`.",
			})
		case 2:
			section.Items = append(section.Items, Component{
				Type: ComponentStatGrid, Key: fmt.Sprintf("stats-%d", index),
				Stats: []Stat{{Label: "Open", Value: "12"}, {Label: "Closed", Value: "34"}},
			})
		default:
			section.Items = append(section.Items, Component{
				Type: ComponentChart, Key: fmt.Sprintf("chart-%d", index), Kind: "line",
				Series: []ChartSeries{{Points: []ChartPoint{{Label: "a", Value: 1}, {Label: "b", Value: 2}}}},
			})
		}
	}
	return &View{Sections: []Section{section}}
}

// BenchmarkCheckView holds a view to the schema the way the hub does for
// every view a plugin returns.
func BenchmarkCheckView(b *testing.B) {
	for _, items := range []int{8, 64, 256} {
		v := benchView(items)
		if err := CheckView(v); err != nil {
			b.Fatalf("%d items: %v", items, err)
		}
		b.Run(fmt.Sprintf("items=%d", items), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := CheckView(v); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// streamSnapshot is a keyed UI stream snapshot frame with items progress rows,
// the shape a live plugin page streams while work advances.
func streamSnapshot(items int) []byte {
	var rows []string
	for index := range items {
		rows = append(rows, fmt.Sprintf(`{"type":"progress","key":"row-%d","label":"Item %d","ratio":0.%d}`, index, index, index%10))
	}
	return []byte(fmt.Sprintf(`{"protocol":1,"session":"session-a","epoch":"epoch-a","version":7,"root_key":"page",`+
		`"view":{"title":"Bench","sections":[{"key":"main","items":[%s]}]}}`, strings.Join(rows, ",")))
}

// BenchmarkValidateStreamSnapshot holds a stream snapshot to its schema the
// way the hub does for every stream frame. Stream frames are decoded with
// UseNumber, so every number reaches the checker as a json.Number.
func BenchmarkValidateStreamSnapshot(b *testing.B) {
	for _, items := range []int{8, 64, 256} {
		raw := streamSnapshot(items)
		if err := ValidateJSON("UIStreamSnapshot", raw); err != nil {
			b.Fatalf("%d items: %v", items, err)
		}
		b.Run(fmt.Sprintf("items=%d", items), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := ValidateJSON("UIStreamSnapshot", raw); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
