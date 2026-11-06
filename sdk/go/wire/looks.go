package wire

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"image/png"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Looks' files (docs/extensions.md "Looks"): what the hub holds a look's pictures to when it installs an extension and
// before it serves them, here so an extension's own tests can hold them to the same (CheckLooks).

const (
	LookMaxFile    = 4 << 20   // a PNG
	LookMaxSVG     = 256 << 10 // an SVG
	LookMaxTotal   = 16 << 20  // a look
	LookMinSide    = 16
	lookMaxSide    = 1024 // an avatar's side
	lookMaxDotSide = 512  // a dot frame's side
)

// CheckLookPNG holds a picture to being a square PNG of side px (0: any) at most most px wide.
func CheckLookPNG(b []byte, side, most int) error {
	cfg, err := png.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return errors.New("not a PNG")
	}
	if cfg.Width != cfg.Height {
		return fmt.Errorf("%d×%d, not square", cfg.Width, cfg.Height)
	}
	if cfg.Width > most || cfg.Width < LookMinSide {
		return fmt.Errorf("%d px, %d to %d", cfg.Width, LookMinSide, most)
	}
	if side > 0 && cfg.Width != side {
		return fmt.Errorf("%d px, its key says %d", cfg.Width, side)
	}
	return nil
}

// ReadLookFile reads a file a look names: a regular file inside the package at root (a link may not lead out of it)
// within LookMaxFile (LookMaxSVG for an SVG).
func ReadLookFile(root, name string) ([]byte, error) {
	clean := path.Clean(name)
	if name == "" || clean != name || path.IsAbs(name) || strings.Contains(name, `\`) || clean == ".." || strings.HasPrefix(clean, "../") {
		return nil, fmt.Errorf("%q is not a path inside the package", name)
	}
	p := filepath.Join(root, filepath.FromSlash(name))
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return nil, fmt.Errorf("%s 不在包里", name)
	}
	rootResolved, _ := filepath.EvalSymlinks(root)
	if rel, err := filepath.Rel(rootResolved, resolved); err != nil || rel == ".." || strings.HasPrefix(filepath.ToSlash(rel), "../") {
		return nil, fmt.Errorf("%s 跳出了包", name)
	}
	fi, err := os.Stat(resolved)
	if err != nil || !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s 不是普通文件", name)
	}
	most := int64(LookMaxFile)
	if strings.HasSuffix(name, ".svg") {
		most = LookMaxSVG
	}
	if fi.Size() > most {
		return nil, fmt.Errorf("%s: %d bytes, more than %d", name, fi.Size(), most)
	}
	return os.ReadFile(resolved)
}

func darkSizes(l LookItem) map[string]string {
	if l.Avatar.Dark == nil {
		return nil
	}
	return l.Avatar.Dark.Sizes
}

// CheckLooks holds the looks of a manifest to what the hub checks of their files in the package at root: an
// extension's test calls it before it ships (the manifest's own rules: ValidateJSON("Manifest", manifest)).
func CheckLooks(manifest []byte, root string) error {
	var m struct {
		Contributes struct {
			Looks []LookItem `json:"looks"`
		} `json:"contributes"`
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		return err
	}
	for _, l := range m.Contributes.Looks {
		// Each key on its own: two sides may name the same file, and each must hold (a map by file would keep one).
		type pic struct {
			file       string
			side, most int
		}
		var pics []pic
		for _, sizes := range []map[string]string{l.Avatar.Sizes, darkSizes(l)} {
			for side, f := range sizes {
				n, _ := strconv.Atoi(side)
				pics = append(pics, pic{f, n, lookMaxSide})
			}
		}
		if d := l.Dot; d != nil {
			for _, st := range []*LookDotState{&d.States.Idle, d.States.Thinking, d.States.Working, d.States.NeedsYou, d.States.Offline} {
				if st != nil {
					pics = append(pics, pic{st.File, d.Size, lookMaxDotSide})
				}
			}
		}
		read := func(f string) ([]byte, error) { return ReadLookFile(root, f) }
		counted := map[string]bool{}
		var total int64
		for _, p := range pics {
			b, err := read(p.file)
			if err != nil {
				return fmt.Errorf("look %s: %v", l.ID, err)
			}
			if !counted[p.file] {
				counted[p.file] = true
				total += int64(len(b))
			}
			if err := CheckLookPNG(b, p.side, p.most); err != nil {
				return fmt.Errorf("look %s: %s: %v", l.ID, p.file, err)
			}
		}
		svgs := []string{l.Avatar.Svg}
		if l.Avatar.Dark != nil {
			svgs = append(svgs, l.Avatar.Dark.Svg)
		}
		for _, f := range svgs {
			if f == "" {
				continue
			}
			b, err := read(f)
			if err != nil {
				return fmt.Errorf("look %s: %v", l.ID, err)
			}
			if !counted[f] {
				counted[f] = true
				total += int64(len(b))
			}
			if _, err := CleanSVG(b); err != nil {
				return fmt.Errorf("look %s: %s: %v", l.ID, f, err)
			}
		}
		if total > LookMaxTotal {
			return fmt.Errorf("look %s: %d bytes, more than %d", l.ID, total, LookMaxTotal)
		}
	}
	return nil
}

// svgAllowed are the SVG elements a face is drawn with; anything else (script, foreignObject, use of another file,
// animation that sets attributes, links) is dropped with what is inside it.
var svgAllowed = map[string]bool{
	"svg": true, "g": true, "path": true, "circle": true, "ellipse": true, "rect": true, "line": true, "polyline": true,
	"polygon": true, "defs": true, "linearGradient": true, "radialGradient": true, "stop": true, "clipPath": true,
	"mask": true, "title": true, "desc": true, "symbol": true, "use": true, "filter": true, "feGaussianBlur": true,
	"feOffset": true, "feMerge": true, "feMergeNode": true, "feFlood": true, "feComposite": true, "feColorMatrix": true,
	"feBlend": true, "text": true, "tspan": true,
}

// CleanSVG is the SVG with what could run or reach out taken out: elements not in svgAllowed (script, foreignObject,
// style, image, a, animate…) with their content, on* attributes, hrefs that are not to the same document (#…), any
// url(…) that is not url(#…), and the DOCTYPE (no entities). An SVG that does not parse, has no <svg> root or whose
// root has neither a viewBox nor a size is refused.
func CleanSVG(raw []byte) ([]byte, error) {
	if len(raw) > LookMaxSVG {
		return nil, fmt.Errorf("%d bytes, more than %d", len(raw), LookMaxSVG)
	}
	dec := xml.NewDecoder(bytes.NewReader(raw))
	dec.Strict = true
	var out bytes.Buffer
	enc := xml.NewEncoder(&out)
	skip := 0
	depth := 0
	sawRoot := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("not an SVG: %v", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if skip > 0 {
				skip++
				continue
			}
			if depth == 0 {
				if sawRoot || t.Name.Local != "svg" {
					return nil, errors.New("not an SVG: the root is not <svg>")
				}
				sawRoot = true
				if !hasAttr(t, "viewBox") && !(hasAttr(t, "width") && hasAttr(t, "height")) {
					return nil, errors.New("the <svg> has neither a viewBox nor a width and height")
				}
			}
			if !svgAllowed[t.Name.Local] {
				skip = 1
				continue
			}
			depth++
			t.Attr = cleanAttrs(t.Attr)
			if err := enc.EncodeToken(svgToken(t)); err != nil {
				return nil, err
			}
		case xml.EndElement:
			if skip > 0 {
				skip--
				continue
			}
			depth--
			if err := enc.EncodeToken(xml.EndElement{Name: localName(t.Name)}); err != nil {
				return nil, err
			}
		case xml.CharData:
			if skip == 0 && depth > 0 {
				if err := enc.EncodeToken(t.Copy()); err != nil {
					return nil, err
				}
			}
		case xml.Directive:
			// <!DOCTYPE …> and its entities: never written out.
			if bytes.Contains(bytes.ToUpper(t), []byte("ENTITY")) {
				return nil, errors.New("an SVG with entities")
			}
		}
		// Comments and processing instructions are left out.
	}
	if !sawRoot {
		return nil, errors.New("not an SVG: no <svg>")
	}
	if err := enc.Flush(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func hasAttr(t xml.StartElement, name string) bool {
	for _, a := range t.Attr {
		if a.Name.Local == name && a.Name.Space == "" {
			return true
		}
	}
	return false
}

// localName drops the namespace the decoder resolved: the cleaned SVG is written with the SVG namespace on its root.
func localName(n xml.Name) xml.Name { return xml.Name{Local: n.Local} }

func svgToken(t xml.StartElement) xml.StartElement {
	out := xml.StartElement{Name: localName(t.Name)}
	if t.Name.Local == "svg" {
		out.Attr = append(out.Attr, xml.Attr{Name: xml.Name{Local: "xmlns"}, Value: "http://www.w3.org/2000/svg"})
	}
	out.Attr = append(out.Attr, t.Attr...)
	return out
}

var cssURL = regexp.MustCompile(`(?i)url\(\s*['"]?\s*([^)'"]*)`)

// cleanAttrs keeps the attributes that only draw: no on* handlers, no namespaced ones but xlink:href, hrefs and
// url()s only to the same document.
func cleanAttrs(attrs []xml.Attr) []xml.Attr {
	var out []xml.Attr
	for _, a := range attrs {
		name := strings.ToLower(a.Name.Local)
		switch {
		case strings.HasPrefix(name, "on"):
			continue
		case name == "xmlns" || a.Name.Space == "xmlns":
			continue // written again on the root
		case name == "href":
			if a.Name.Space != "" && a.Name.Space != "http://www.w3.org/1999/xlink" {
				continue
			}
			if !strings.HasPrefix(strings.TrimSpace(a.Value), "#") {
				continue
			}
			out = append(out, xml.Attr{Name: xml.Name{Local: "href"}, Value: a.Value})
			continue
		case a.Name.Space != "" && a.Name.Space != "http://www.w3.org/XML/1998/namespace":
			continue
		case name == "style" || strings.Contains(strings.ToLower(a.Value), "url("):
			if !sameDocumentURLs(a.Value) {
				continue
			}
		}
		if strings.Contains(strings.ToLower(a.Value), "javascript:") {
			continue
		}
		out = append(out, xml.Attr{Name: xml.Name{Local: a.Name.Local}, Value: a.Value})
	}
	return out
}

func sameDocumentURLs(v string) bool {
	for _, m := range cssURL.FindAllStringSubmatch(v, -1) {
		if !strings.HasPrefix(strings.TrimSpace(m[1]), "#") {
			return false
		}
	}
	return !strings.Contains(strings.ToLower(v), "@import") && !strings.Contains(strings.ToLower(v), "expression(")
}
