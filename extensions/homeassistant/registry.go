package main

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// The registries (WebSocket config/*_registry/list): which device an entity belongs to, its own area, and its
// entity_category, which the states do not carry. A device's configuration switches ("辅光开关", "通电默认状态")
// read as plain switches in /api/states; only the registry says they are config or diagnostic.

var registryFor = 5 * time.Minute

// RegEntity is an entity registry entry, the fields read here.
type RegEntity struct {
	EntityID       string  `json:"entity_id"`
	DeviceID       string  `json:"device_id"`
	AreaID         string  `json:"area_id"`
	EntityCategory string  `json:"entity_category"` // "config", "diagnostic" or ""
	HiddenBy       *string `json:"hidden_by"`
	DisabledBy     *string `json:"disabled_by"`
	Platform       string  `json:"platform"` // the integration: xiaomi_home, xiaomi_miot, …
}

// RegDevice is a device registry entry.
type RegDevice struct {
	ID         string `json:"id"`
	AreaID     string `json:"area_id"`
	Name       string `json:"name"`
	NameByUser string `json:"name_by_user"`
}

func (d RegDevice) title() string {
	if d.NameByUser != "" {
		return d.NameByUser
	}
	return d.Name
}

// Registry is the three registries, indexed.
type Registry struct {
	Entities map[string]RegEntity
	Devices  map[string]RegDevice
	ByDevice map[string][]string // device → its entities, sorted
}

// Registry reads the entity and device registries (kept registryFor).
func (h *ha) Registry(ctx context.Context) (*Registry, error) {
	h.mu.Lock()
	if h.reg != nil && time.Since(h.regAt) < registryFor {
		r := h.reg
		h.mu.Unlock()
		return r, nil
	}
	h.mu.Unlock()
	var ents []RegEntity
	if err := h.WS(ctx, "config/entity_registry/list", nil, &ents); err != nil {
		return nil, err
	}
	var devs []RegDevice
	if err := h.WS(ctx, "config/device_registry/list", nil, &devs); err != nil {
		return nil, err
	}
	r := &Registry{Entities: map[string]RegEntity{}, Devices: map[string]RegDevice{}, ByDevice: map[string][]string{}}
	for _, e := range ents {
		r.Entities[e.EntityID] = e
		if e.DeviceID != "" {
			r.ByDevice[e.DeviceID] = append(r.ByDevice[e.DeviceID], e.EntityID)
		}
	}
	for _, d := range devs {
		r.Devices[d.ID] = d
	}
	for _, l := range r.ByDevice {
		sort.Strings(l)
	}
	h.mu.Lock()
	h.reg, h.regAt = r, time.Now()
	h.mu.Unlock()
	return r, nil
}

// cachedRegistry is the last registry read, however old (nil: never read): for actions.describe, which may not reach
// out.
func (h *ha) cachedRegistry() *Registry {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reg
}

// pageRegistry is the registry for the page: read when stale, the last one (or nil) when it cannot be (a token that
// may not use the WebSocket's config commands): the page then lists entities one by one. A failure is not tried
// again for registryFor, and is not the connector's error: the states were read a moment ago.
func (a *app) pageRegistry(ctx context.Context) *Registry {
	h, _, _ := a.client()
	h.mu.Lock()
	failed, last := !h.regFailAt.IsZero() && time.Since(h.regFailAt) < registryFor, h.reg
	h.mu.Unlock()
	if failed {
		return last
	}
	r, err := h.Registry(ctx)
	if err != nil {
		if ctx.Err() == nil {
			h.mu.Lock()
			h.regFailAt = time.Now()
			h.mu.Unlock()
			h.note(nil)
		}
		return last
	}
	h.mu.Lock()
	h.regFailAt = time.Time{}
	h.mu.Unlock()
	return r
}

// mainDomains are what a device is: a light, a fan… A switch, select, number or button of a device that also has one
// of these is a setting of that device, not a device of its own.
var mainDomains = []string{"light", "fan", "climate", "cover", "media_player", "vacuum", "humidifier", "water_heater",
	"lock", "valve", "lawn_mower", "alarm_control_panel"}

// accessoryDomains are those such settings come as.
var accessoryDomains = []string{"switch", "select", "number", "button", "input_boolean"}

// skip says why an entity is not a target of a batch call ("": it is).
func (r *Registry) skip(e string, states map[string]State) string {
	if r == nil {
		return ""
	}
	re, ok := r.Entities[e]
	if !ok {
		return ""
	}
	switch {
	case re.EntityCategory == "config":
		return "设备的配置项"
	case re.EntityCategory == "diagnostic":
		return "设备的诊断项"
	case re.HiddenBy != nil && *re.HiddenBy != "":
		return "在 Home Assistant 里隐藏了"
	case re.DisabledBy != nil && *re.DisabledBy != "":
		return "在 Home Assistant 里停用了"
	}
	d, _, _ := strings.Cut(e, ".")
	if re.DeviceID == "" || !slices.Contains(accessoryDomains, d) {
		return ""
	}
	for _, sib := range r.ByDevice[re.DeviceID] {
		sd, _, _ := strings.Cut(sib, ".")
		if sib == e || !slices.Contains(mainDomains, sd) || sd == d {
			continue
		}
		name := sib
		if s, ok := states[sib]; ok {
			name = s.Name()
		}
		return fmt.Sprintf("是「%s」（%s）的附属开关", name, sib)
	}
	return ""
}

// skipped is an entity a batch call leaves out, and why.
type skipped struct {
	ID, Name, Why string
}

// batch is the targets of a call on every entity of domain in an area: the devices, with their settings and
// diagnostics (entity_category), hidden ones, those offline and those of hidden areas left out and listed.
func batch(hm *home, reg *Registry, ar *Area, domain string) (targets []State, left []skipped) {
	for _, e := range ar.Entities {
		s, ok := hm.ByID[e]
		if !ok || s.Domain() != domain {
			continue
		}
		why := reg.skip(e, hm.ByID)
		if why == "" && s.Unavailable() {
			why = "离线"
		}
		if why != "" {
			left = append(left, skipped{e, s.Name(), why})
			continue
		}
		targets = append(targets, s)
	}
	return targets, left
}
