package main

import (
	"encoding/json"
	"fmt"
	"os"
	"unicode"
)

// Generate the complete Go simple-fold relation, not JavaScript full case folding.
func main() {
	fold := map[rune]rune{}
	for r := rune(0); r <= unicode.MaxRune; r++ {
		least := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < least {
				least = next
			}
		}
		if least != r {
			fold[r] = least
		}
	}
	data, err := json.Marshal(fold)
	if err != nil {
		panic(err)
	}
	if len(os.Args) != 2 {
		panic("expected output path")
	}
	if err := os.WriteFile(os.Args[1], append(data, '\n'), 0644); err != nil {
		panic(err)
	}
	fmt.Printf("simple-fold: PASS unicode=%s entries=%d\n", unicode.Version, len(fold))
}
