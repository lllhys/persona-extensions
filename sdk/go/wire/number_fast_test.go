package wire

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strconv"
	"testing"
)

// The comparisons as they were before the float64 fast paths (#5348), kept as oracles.
func legacyCompare(n json.Number, bound any) (int, bool) {
	x, ok := preciseNumber(n)
	if !ok {
		return 0, false
	}
	y, ok := preciseNumber(bound)
	if !ok {
		return 0, false
	}
	return x.Cmp(y), true
}

func legacyIsNumber(t string, n json.Number) bool {
	f, valid := preciseNumber(n)
	return valid && (t == "number" || f.IsInt())
}

func legacyEqual(a, b any) bool {
	_, aNumber := a.(json.Number)
	_, bNumber := b.(json.Number)
	if aNumber || bNumber {
		if left, ok := preciseNumber(a); ok {
			if right, ok := preciseNumber(b); ok {
				return left.Cmp(right) == 0
			}
		}
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// numberBounds are the float64 bounds and constants a schema can carry: every minimum, maximum and numeric const
// or enum entry in the embedded schemas, and the edges of float64.
func numberBounds(t *testing.T) []float64 {
	t.Helper()
	loadOnce.Do(loadSchemas)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	bounds := []float64{0, math.Copysign(0, -1), 1, -1, 0.5, 0.1, 1e-7, 1 << 53, 1<<53 + 2, -(1 << 53),
		math.MaxFloat64, -math.MaxFloat64, math.SmallestNonzeroFloat64, 2.2250738585072014e-308, 255, 65535, 1e15}
	var walk func(any)
	walk = func(node any) {
		switch x := node.(type) {
		case map[string]any:
			for k, v := range x {
				if f, ok := v.(float64); ok && (k == "minimum" || k == "maximum" || k == "const") {
					bounds = append(bounds, f)
				}
				walk(v)
			}
		case []any:
			for _, v := range x {
				if f, ok := v.(float64); ok {
					bounds = append(bounds, f)
				}
				walk(v)
			}
		}
	}
	for _, doc := range files {
		walk(doc)
	}
	slices.Sort(bounds)
	return slices.CompactFunc(bounds, func(a, b float64) bool { return math.Float64bits(a) == math.Float64bits(b) })
}

// numberTexts are number texts around each bound: the bound itself in several spellings, its float64
// neighbours, decimals that round onto it from either side, and text that is not a JSON number.
func numberTexts(bounds []float64, rng *rand.Rand) []json.Number {
	texts := []string{
		"0", "-0", "0.0", "-0.0", "1", "1.0", "1e0", "1E+0", "10e-1", "1e2", "100", "1.5", "-1.5", "1e400", "-1e400",
		"1e-400", "-1e-400", "1e9999", "1e-9999", "1e10000", "1e-10000", "5e-3240000000000000000000001", "0e-99999", "1.0000000000000000001", "0.9999999999999999999", "9007199254740993", "9007199254740992.5",
		"123456789012345678901234567890", "1e99999999999999999999", "5e-324", "2e-324", "1.7976931348623157e308",
		"1.7976931348623158e308", "1.7976931348623159e308", "Inf", "-Inf", "NaN", "0x10", "1_000", "+1", "01", "1.",
		".5", "1e", "-", "", " 1", "1 ", "١",
	}
	for _, m := range bounds {
		for _, f := range []float64{m, math.Nextafter(m, math.Inf(1)), math.Nextafter(m, math.Inf(-1))} {
			if math.IsInf(f, 0) {
				continue
			}
			for _, format := range []byte{'g', 'e', 'f'} {
				s := strconv.FormatFloat(f, format, -1, 64)
				texts = append(texts, s, s+"0000000000000000000001")
				if format == 'e' {
					texts = append(texts, s[:len(s)-1]+"9")
				}
			}
			exact := strconv.FormatFloat(f, 'e', 30, 64)
			texts = append(texts, exact, exact[:len(exact)-5]+"1"+exact[len(exact)-4:])
		}
	}
	for range 2000 {
		var s string
		switch rng.IntN(4) {
		case 0:
			s = strconv.FormatInt(rng.Int64()>>rng.IntN(63), 10)
		case 1:
			s = strconv.FormatFloat(rng.NormFloat64()*math.Pow(10, float64(rng.IntN(40)-20)), 'g', -1, 64)
		case 2:
			s = fmt.Sprintf("%d.%d", rng.IntN(1000)-500, rng.IntN(1000000))
		default:
			s = fmt.Sprintf("%de%d", rng.IntN(2000)-1000, rng.IntN(700)-350)
		}
		texts = append(texts, s)
	}
	out := make([]json.Number, len(texts))
	for i, s := range texts {
		out[i] = json.Number(s)
	}
	return out
}

func TestNumberFastPathsMatchLegacy(t *testing.T) {
	bounds := numberBounds(t)
	texts := numberTexts(bounds, rand.New(rand.NewPCG(5348, 1)))
	pairs, fast := 0, 0
	for _, n := range texts {
		if _, ok := fastNumber(n); ok {
			fast++
		}
		for _, kind := range []string{"number", "integer"} {
			if got, want := isType(kind, n), legacyIsNumber(kind, n); got != want {
				t.Fatalf("isType(%s, %q) = %v, legacy %v", kind, n, got, want)
			}
		}
		for _, m := range append([]any{nil, "1", math.NaN(), math.Inf(1), json.Number("1")}, anys(bounds)...) {
			gotCmp, gotOK := compareNumber(n, m)
			wantCmp, wantOK := legacyCompare(n, m)
			if gotOK != wantOK || (gotOK && gotCmp != wantCmp) {
				t.Fatalf("compareNumber(%q, %v) = %d %v, legacy %d %v", n, m, gotCmp, gotOK, wantCmp, wantOK)
			}
			if got, want := equal(n, m), legacyEqual(n, m); got != want {
				t.Fatalf("equal(%q, %v) = %v, legacy %v", n, m, got, want)
			}
			if got, want := equal(m, n), legacyEqual(m, n); got != want {
				t.Fatalf("equal(%v, %q) = %v, legacy %v", m, n, got, want)
			}
			pairs++
		}
	}
	// The oracle only proves something where the fast path runs.
	if fast < len(texts)*3/4 {
		t.Fatalf("fast path took %d of %d texts", fast, len(texts))
	}
	t.Logf("%d texts (%d on the fast path), %d bounds, %d pairs", len(texts), fast, len(bounds), pairs)
}

func TestEqualFastPathsMatchLegacy(t *testing.T) {
	values := []any{nil, true, false, "", "a", "b", "progress", "<&>", "<&>", " ", "é", "é",
		"\xff", "\xfe", "�", "a\x00", "a\xc3", 0.0, 1.0, json.Number("0"), json.Number("1"), []any{}, map[string]any{}}
	for _, a := range values {
		for _, b := range values {
			if got, want := equal(a, b), legacyEqual(a, b); got != want {
				t.Fatalf("equal(%#v, %#v) = %v, legacy %v", a, b, got, want)
			}
		}
	}
}

func anys(fs []float64) []any {
	out := make([]any, len(fs))
	for i, f := range fs {
		out[i] = f
	}
	return out
}
