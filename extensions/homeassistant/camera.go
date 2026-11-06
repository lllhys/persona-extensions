package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // a vacuum's map camera answers PNG
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// A camera's snapshot (homeassistant_camera_snapshot): Home Assistant's /api/camera_proxy/<camera>, which every
// camera entity answers with its current image, a real camera's frame or a picture another integration draws (a
// vacuum's map). It goes to the assistant as an image (the result's images, for a hub that passes them on) and is kept
// in the extension's data directory, the newest few, so an assistant on the hub's machine can open the file.

const (
	snapshotMost = 20      // files kept
	snapshotSide = 1600    // pixels on the longest side, beyond which the image is scaled down
	snapshotMax  = 8 << 20 // bytes read from Home Assistant
)

// Image is an image of a tool's result.
type Image struct {
	Data     string `json:"data"` // base64
	MimeType string `json:"mime_type"`
}

// Snapshot reads camera's current image and its type.
func (h *ha) Snapshot(ctx context.Context, camera string) ([]byte, string, error) {
	raw, typ, err := h.sendTyped(ctx, http.MethodGet, "/api/camera_proxy/"+url.PathEscape(camera), nil)
	if err != nil {
		return nil, "", err
	}
	if len(raw) > snapshotMax {
		return nil, "", fmt.Errorf("摄像头的图太大了（%d MB）", len(raw)>>20)
	}
	typ, _, _ = strings.Cut(typ, ";")
	if !strings.HasPrefix(typ, "image/") {
		typ = http.DetectContentType(raw)
	}
	if !strings.HasPrefix(typ, "image/") {
		return nil, "", fmt.Errorf("Home Assistant 回的不是图片（%s）", typ)
	}
	return raw, typ, nil
}

// shrink scales an image down to snapshotSide on its longest side, as JPEG; one already small enough (or that does
// not decode) is kept as it is.
func shrink(raw []byte, typ string) ([]byte, string, image.Point) {
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return raw, typ, image.Point{}
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if max(w, h) <= snapshotSide {
		return raw, typ, image.Point{w, h}
	}
	scale := float64(snapshotSide) / float64(max(w, h))
	nw, nh := max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
	out := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := range nh {
		sy := b.Min.Y + int(float64(y)/scale)
		for x := range nw {
			out.Set(x, y, img.At(b.Min.X+int(float64(x)/scale), sy))
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: 82}); err != nil {
		return raw, typ, image.Point{w, h}
	}
	return buf.Bytes(), "image/jpeg", image.Point{nw, nh}
}

func (a *app) toolCamera(ctx context.Context, camera string) (map[string]any, error) {
	camera = strings.TrimSpace(camera)
	hm, err := a.home(ctx, statesFor)
	if err != nil {
		return nil, err
	}
	var cams []string
	for _, s := range hm.States {
		if s.Domain() == "camera" && !(hm.Hidden[s.EntityID] && hm.AreaOf[s.EntityID] == nil) {
			cams = append(cams, fmt.Sprintf("%s（%s）", s.Name(), s.EntityID))
		}
	}
	if camera == "" {
		if len(cams) == 0 {
			return nil, fmt.Errorf("Home Assistant 里没有摄像头（camera 实体）")
		}
		return nil, fmt.Errorf("要写 entity_id：有 %s", strings.Join(cams, "、"))
	}
	s, ok := hm.ByID[camera]
	switch {
	case !strings.HasPrefix(camera, "camera."):
		return nil, fmt.Errorf("「%s」不是摄像头：要 camera.xxx", clip(camera, 60))
	case !ok || (hm.Hidden[camera] && hm.AreaOf[camera] == nil):
		if len(cams) == 0 {
			return nil, fmt.Errorf("Home Assistant 里没有摄像头（camera 实体）")
		}
		return nil, fmt.Errorf("没有 %s；有：%s", camera, strings.Join(cams, "、"))
	case s.Unavailable():
		return nil, fmt.Errorf("「%s」离线了，拿不到图", s.Name())
	}
	h, _, _ := a.client()
	raw, typ, err := h.Snapshot(ctx, camera)
	if err != nil {
		return nil, err
	}
	small, typ, size := shrink(raw, typ)
	at := time.Now().In(a.loc())
	text := fmt.Sprintf("「%s」（%s）%s 的画面", s.Name(), camera, at.Format("2006-01-02 15:04:05"))
	if size.X > 0 {
		text += fmt.Sprintf("，%d×%d", size.X, size.Y)
	}
	text += "，" + typ
	if strings.Contains(camera, "_map") || strings.Contains(s.Name(), "地图") || strings.Contains(strings.ToLower(s.Name()), " map") {
		text += "（这是设备画的地图，不是摄像头拍的）"
	}
	if path, err := a.keepSnapshot(camera, at, small, typ); err == nil {
		text += "。图片也存在 " + path + "（hub 这台机器上）"
	}
	return map[string]any{"text": text, "images": []Image{{Data: base64.StdEncoding.EncodeToString(small), MimeType: typ}}}, nil
}

// keepSnapshot writes the image into the data directory and keeps the newest snapshotMost.
func (a *app) keepSnapshot(camera string, at time.Time, raw []byte, typ string) (string, error) {
	a.mu.Lock()
	dir := a.dataDir
	a.mu.Unlock()
	if dir == "" {
		return "", fmt.Errorf("no data directory")
	}
	dir = filepath.Join(dir, "snapshots")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	ext := ".jpg"
	if typ == "image/png" {
		ext = ".png"
	}
	path := filepath.Join(dir, strings.ReplaceAll(camera, ".", "_")+"-"+at.Format("20060102-150405")+ext)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", err
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*"))
	sort.Strings(files)
	if len(files) > snapshotMost {
		// by time: the names end with it, so sort by the part after the camera
		sort.Slice(files, func(i, j int) bool { return stamp(files[i]) < stamp(files[j]) })
		for _, f := range files[:len(files)-snapshotMost] {
			_ = os.Remove(f)
		}
	}
	return path, nil
}

func stamp(path string) string {
	base := filepath.Base(path)
	if i := strings.LastIndex(base, "-"); i > 0 {
		if j := strings.LastIndex(base[:i], "-"); j > 0 {
			return base[j+1:]
		}
	}
	return base
}
