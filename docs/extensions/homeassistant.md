# Home Assistant

[Documentation](../README.md) · [Source](../../extensions/homeassistant/)

Your own Home Assistant in Persona: read the home's state, history, logbook, long-term statistics and energy, control its devices, automations and speakers, and get told when something happens, through Home Assistant's REST and WebSocket APIs with a long-lived access token.

| Extension point | Contribution | Behavior |
| --- | --- | --- |
| `ui` | `home`, placement `nav_page`, icon `gauge` | A "家" entry on the main navigation, with three tabs (UI version 4; a segmented control before): **家** lists alarms (leaks, smoke, jammed locks…) and every area with what is on in it by name ("客厅灯、吸顶灯、插座 等 4 个开着"), its temperature and humidity. An area lists its devices, a section per kind (灯, 开关, 空调…): one row per device, from the entity registry, with the device's settings, diagnostics and accessory switches (a ceiling light's 辅光开关, a 通电默认状态 switch) folded into it ("另有 2 项"), offline ones behind a row; sensors or everything on the other choices. A light, switch, fan, humidifier or helper turns on or off by tapping its row; a row at the end switches the rows to opening the device instead. A climate's row shows its mode, room and target temperature. A search box and an area filter (UI version 3) find any entity. An entity opens under a breadcrumb (家 / 客厅 / 吸顶灯; a 返回 button on top before UI version 4) with its state, its controls, the rest of its device folded into one row, the last 24 hours (a line for numbers, the last changes otherwise) and its attributes folded into one row. **动态** is the logbook of the last two hours, newest first, an entity that changes all the time one row, each row opening its entity. **自动化** lists the automations, scripts and scenes: a script or a scene runs from its row, an automation opens with 启用 / 停用 and 触发一次. Statistics, energy, watches and cameras stay with the assistant. Without the entity registry (a token that may not read it) the area lists entities one by one. |
| `connectors` | `homeassistant`, capabilities `status`, `tools` and `actions` | Read tools `homeassistant_overview`, `homeassistant_search`, `homeassistant_entity`, `homeassistant_history`, `homeassistant_logbook`, `homeassistant_statistics`, `homeassistant_energy`, `homeassistant_automations`, `homeassistant_automation_config`, `homeassistant_speakers`, `homeassistant_camera_snapshot`, `homeassistant_watches`; write actions `homeassistant_call`, `homeassistant_secure_call`, `homeassistant_run`, `homeassistant_automation_edit`, `homeassistant_say`, `homeassistant_watch`. |

## Controls and safety

A tap in the page is the user's own and calls the service at once; unlocking, disarming, and running or switching automations, scripts and scenes ask first. A row's tap only turns a light, switch, fan, humidifier or helper on or off, or runs a script or a scene. The page offers, by domain: on / off (lights, switches, fans, humidifiers, helpers), brightness 20 / 50 / 100 % (dimmable lights), open / close / stop (covers, valves), on / off, ±1 step of the target temperature and the HVAC mode (climates), play / pause, volume and power (media players), start / pause / dock (vacuums, mowers), lock / unlock (locks), arm / disarm (alarm panels), run (scenes, scripts), enable / disable and trigger (automations), press (buttons), an option (selects) and a value (numbers).

The assistant's writes are connector actions, so they only run after the hub's gate (`connector_gate`; by default the phone confirms each one). `homeassistant_secure_call` is the only way to locks and alarm panels; it, `homeassistant_automation_edit` and `homeassistant_say` are in `confirm_always`: an `allow` rule in the gate cannot skip the confirmation. Every device call, from the page or the assistant:

- names its entities (`entity_id`, one domain per call, at most 20), or an area and a domain whose entities the card then lists one by one ([Calls by area](#calls-by-area)); `data` may not carry `entity_id`, `device_id`, `area_id`, `floor_id` or `label_id`, so Home Assistant is never handed a wider target than the card shows;
- must be in a writable domain: by default the common device domains (lights, switches, fans, covers, climate, media players, vacuums, humidifiers, water heaters, helpers, numbers, selects, buttons, scenes, scripts, remotes, sirens, valves, mowers, locks, alarm panels). Domains that act on the whole system or run commands (`homeassistant`, `hassio`, `shell_command`, `rest_command`, `notify`, …) are never writable unless listed in `writable_domains`;
- is checked against Home Assistant's own service list when it has been read.

`actions.describe` reads only cached state (no network) and writes the card as the entities' names, their current state, the service and its data. `actions.do` runs a request id once, and refuses a call by area or an automation change when what it would do now differs from the card.

## Assistant tools

| Tool | What it answers |
| --- | --- |
| `homeassistant_overview` | Each area: devices on, offline, temperature / humidity; alarms. |
| `homeassistant_search` | Entities by name, id, area or domain. With an area it also reads the entity registry, which a call by area needs. |
| `homeassistant_entity` | One entity's state, attributes, area and callable services. |
| `homeassistant_history` | One entity's changes over the last 1–168 hours, with min / max / mean for numbers. |
| `homeassistant_logbook` | What happened in the whole home (or one area or entity) between two times or over the last hours (up to 31 days), from `/api/logbook`. Sorted, in the user's time zone (the hub's), grouped by day; an entity with more than 8 entries in the span is one line with its count and last state; hidden areas are left out; who caused a change (an automation, a script, a person) is said. |
| `homeassistant_statistics` | Up to 5 sensors' long-term statistics (WebSocket `recorder/statistics_during_period`) by hour, day, week or month, up to two years (hourly: 14 days). Measurements get each period's mean / min / max and the whole span's; meters (`has_sum`) get each period's use, the total, the mean and the largest period. A sensor without `state_class` has none and says so. |
| `homeassistant_energy` | The energy dashboard's statistics (`energy/get_prefs`): grid import / export, solar, battery, gas, water and each device, totalled per period and over the span. Says so when energy is not set up. |
| `homeassistant_automations` | Automations (on / off, last triggered, config id or "YAML only"), scripts and scenes. An empty list is fine. |
| `homeassistant_automation_config` | One automation's full config as JSON, by entity id or config id. |
| `homeassistant_speakers` | Media players (and `remote.*speaker*`) with how each can speak, in order, and why the other ways cannot. |
| `homeassistant_camera_snapshot` | A camera entity's current image from `/api/camera_proxy` (a real camera, or e.g. a vacuum's map), scaled to at most 1600 px and returned as an image; also saved in the extension's data directory (`snapshots/`, newest 20, mode 0600). |
| `homeassistant_watches` | The watch rules, whether the subscription to Home Assistant is up, missing hub permissions, and the last firings. |

## Writes

| Action | Gate | What it does |
| --- | --- | --- |
| `homeassistant_call` | phone | A service on named entities of one domain, or on every entity of a domain in an area (`area` + `domain`, see below). |
| `homeassistant_secure_call` | always | Locks and alarm panels; never by area. |
| `homeassistant_run` | phone | `automation.trigger` (condition skipped unless `skip_condition: false`), `automation.turn_on` / `turn_off` (enable / disable), `script.turn_on` with `variables` / `script.turn_off`, `scene.turn_on`. The domain must be writable (`automation` is in the defaults now). |
| `homeassistant_automation_edit` | always | Create, update or delete a UI automation through `/api/config/automation/config/<id>` (Home Assistant reloads automations itself). The card has the whole new config; an update also has a line diff against the current config, a delete the config that goes. Updates and deletes need the config read first with `homeassistant_automation_config`, and are refused when it changed in Home Assistant after the card. A new automation's id is derived from its config, so describe and do agree. YAML automations (no config id) cannot be edited. |
| `homeassistant_say` | always | One sentence (at most 300 characters) on a speaker. |
| `homeassistant_watch` | phone | Add or remove an assistant watch rule. |

### Calls by area

`{"area": "客厅", "domain": "switch", "service": "turn_off"}` targets the area's entities of that domain (an entity is in an area through its device or by itself, as Home Assistant's `area_entities()` says), except:

- `entity_category` `config` or `diagnostic` in the entity registry (WebSocket `config/entity_registry/list`): device settings such as a light's "通电默认状态". `/api/states` does not carry the category, so without the registry a call by area is refused;
- a switch, select, number, button or helper whose device also has a main entity of another domain (light, fan, climate, cover, media player, vacuum, humidifier, water heater, lock, valve, mower, alarm panel): an accessory of that device, such as a ceiling light's "辅光开关", which Xiaomi integrations expose as a plain switch without a category;
- entities hidden or disabled in Home Assistant, offline ones, and those of hidden areas.

At most 50 entities. The card lists every entity that will be operated, with its current state, and every one left out with why. `actions.describe` reads the cached states, areas and registry (it may not reach out; the extension reads them at start and every 4 minutes, and `homeassistant_search` with an area reads them again); `actions.do` refuses the call when the expansion differs from the card's.

### Speaking on a speaker

`homeassistant_say` tries, in order, the first way Home Assistant has (or the `method` given):

1. `xiaomi_miot`: service `xiaomi_miot.intelligent_speaker` `{entity_id, text, execute: false}`, only for an entity of the xiaomi_miot integration;
2. `xiaomi_home`: a `notify.*play_text*` entity of the speaker's device (the official integration turns MIoT actions into notify entities), `notify.send_message` with the action's inputs as a JSON list (`["text"]`);
3. `text`: a `text.*play_text*` entity of the speaker's device, `text.set_value`;
4. `tts`: `tts.speak` with a TTS engine (`tts.*`, the first one unless `tts_entity`) on the media player;
5. `media_source`: `media_player.play_media` with `media-source://tts/<engine>?message=…`.

The device's entities come from the registry (by device), else from the speaker's id prefix; entities of other devices (a gateway's `text.*`) are never used. Without any way the answer lists what is missing for each, e.g. a xiaomi_home speaker with only `media_player` services and no TTS engine cannot speak until a TTS integration is added.

## Watches

Rules on state changes that notify the phone or wake the assistant as they happen. The extension keeps a WebSocket to Home Assistant subscribed to `state_changed` (`subscribe_events`) while there are rules, reconnecting with backoff, and checks every change itself:

- a rule targets an entity id or pattern (`binary_sensor.*_door`), an area, a domain and / or a `device_class`;
- its condition is `to` a state (`offline` is `unavailable`), `from` a state, `above` / `below` a number (the state or an `attribute`); it fires when the condition becomes true, after `for_seconds` if set (cancelled when it stops holding first). Offline rules wait 10 minutes unless told otherwise, so a restart of Home Assistant is not an outage;
- the same rule tells about the same entity once per `cooldown_minutes` (30); firings within 3 seconds go out as one delivery per rule ("2 个设备离线"); at most 30 deliveries an hour;
- `do: notify` (default) sends `POST /api/v1/notify` on the hub (`level` `active` or `time-sensitive`, collapsing by rule, a tap opens the entity on the 家 page); `do: assistant` opens an assistant conversation with `POST /api/v1/assistant/conversations` saying what happened and the rule's `message` as the user's instruction; `both` does both.

Rules come from the `watches` setting (one per line, below) and from the assistant through `homeassistant_watch` (kept in the data directory, `watches.json`; one that cannot be read or parsed is moved aside to `watches.json.corrupt` and logged, since 0.2.2). The manifest asks for the hub scopes `notify` and `assistant.ask` for this; a hub that did not grant them records the firing with the reason in `homeassistant_watches`.

Setting lines: `<who> <condition> [for=10m] [do=notify|assistant|both] [level=time-sensitive] [cooldown=30m] [class=<device_class>] [attr=<attribute>] [from=<state>] [name=<名字>] [message=<the rest of the line>]`, where `<who>` is an entity id or pattern, or `<domain>@<area>` (`*@客厅`), and `<condition>` is `=<state>`, `><number>`, `<<number>` or `offline`:

```text
binary_sensor.front_door =on for=1m
sensor.bathroom_humidity >75 for=10m do=assistant message=开一下排风扇
binary_sensor@厨房 class=moisture =on level=time-sensitive name=漏水
switch.* offline
```

## Configuration

[Manifest and settings schema](../../extensions/homeassistant/persona-extension.json)

| Setting | Meaning |
| --- | --- |
| `url` | The address the hub's machine reaches Home Assistant at, `http(s)://host:port` (a path is kept for a reverse proxy under one). |
| secret `token` | A long-lived access token: Home Assistant → your profile → Security → Long-lived access tokens. It acts as that user. Areas need an administrator's token (see [Data](#data)). |
| `insecure_tls` | Skip certificate checks for an `https` address with a self-signed certificate. |
| `writable_domains` | Replace the default writable domains (an empty list makes everything read-only). |
| `hidden_areas` | Area names or ids the page and the assistant never show (another home in the same account, for example). Entities only in a hidden area are left out of searches, tools, the logbook, batches and watches too. |
| `watches` | Watch rules, one per line (see [Watches](#watches)). |

Permissions: `permissions.hub` asks for `notify` (send notifications to your devices) and `assistant.ask` (open conversations with Persona), for the watches; upgrading from 0.1.x asks to accept them.

## Network and sandbox

The address is the user's, so the manifest cannot list it in `permissions.network` (which takes fixed host names). Installed from this market, the extension runs outside the sandbox and connects directly. Installed from a local package (`file:`, `dir:`, an upload) it starts in the sandbox, whose egress proxy then refuses the address: turn the sandbox off for it on the hub's machine with `personad extension sandbox homeassistant off`. A Home Assistant on the hub's own machine is `http://host.persona.example:8123` only inside the sandbox; with it off, use `http://127.0.0.1:8123`.

## Data

States are read in one request (`/api/states`) and kept 5 seconds; areas come from one template render (`areas()`, `area_entities()`) kept 5 minutes, since the REST API has no area registry. Home Assistant keeps its template API for administrators: with a non-administrator's token it answers 401 there while states work, so the page and the tools group devices by type instead and say why (`hidden_areas` cannot apply then); services are kept 10 minutes; history is read per entity when its page or `homeassistant_history` asks. The entity and device registries, statistics and energy settings come over the WebSocket API (`/api/websocket`, the registries kept 5 minutes; the registry needs an administrator's token). On disk, in the extension's data directory, only the assistant's watch rules and the last camera snapshots. Errors never contain the token; a refused token reports its shape (length, `eyJ` start, dots) and the refused request.

Home Assistant bans an address after a few failed logins and then answers 403 to everything from it, a valid token included (`ip_bans.yaml`, read at start). So after a refusal the extension does not try again on its own for 5 minutes; the page's Retry and a new token try at once. To lift a ban, remove the address from `ip_bans.yaml` and restart Home Assistant.

An area's temperature and humidity are the averages of its sensors that read like a room (10–40 °C, 0–100 %), so a fridge's or a water heater's sensor does not count.

## Limits

- The page does not use the subscription: it refreshes every 30 seconds (15 on an entity) and after each tap.
- The page lists a device's configuration switches with the devices; only calls by area leave them out.
- A camera snapshot reaches the assistant as an image only on a hub whose connector protocol passes tool images on: today's hub keeps only a tool's `text` (`serve.CallResult`), so the assistant gets the description and the saved file's path, which it can open when it runs on the hub's machine. The result carries `images: [{data, mime_type}]` for when the hub forwards them as image content.
- Cameras, colors and color temperature, and arbitrary service data from the page are not offered; the assistant can still set them through `homeassistant_call` data.
