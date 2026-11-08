import { build } from "esbuild";
import { mkdir, mkdtemp, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

// Explicit upstream-only integration gate. Nothing is written to the main checkout.
const extension = fileURLToPath(new URL("../", import.meta.url));
const main = process.argv[2];
if (!main || !main.startsWith("/")) throw new Error("expected an absolute main repository path");
await mkdir(resolve(extension, "build/tests"), { recursive: true });
const dir = await mkdtemp(resolve(extension, "build/tests/manager-"));
const stdio = resolve(dir, "stdio.mjs");
await build({ absWorkingDir: extension, entryPoints: ["tests/upstream-stdio.ts"], bundle: true, platform: "node", format: "esm", target: "es2022", jsx: "automatic", jsxImportSource: "@persona/extension", outfile: stdio });
const test = `package extmgr
import (
 "context"
 "encoding/json"
 "os/exec"
 "testing"
 "time"
 "github.com/nwverse/persona/server/internal/extension"
 "github.com/nwverse/persona/server/internal/extension/host"
)
func TestLinuxDoUpstreamColdHTTP(t *testing.T) {
 node,err:=exec.LookPath("node"); if err!=nil {t.Fatal(err)}
 root:=shortTempDir(t)
 h:=New(Options{Dir:root,Config:&Config{dir:root},Hub:&pageTestHub{}})
 h.extensions=newExtensions(root,nil,func(string,...any){})
 manifest:=&extension.Manifest{ManifestVersion:1,ID:"linuxdo",Name:"LINUX DO",Version:"0.1.14",Contributes:map[string]json.RawMessage{"ui":json.RawMessage(\x60[{"id":"hot","title":"Hot","placement":"plugin_page"}]\x60)}}
 if err:=h.extensions.reg.AddHubJSON(manifest,nil,true);err!=nil {t.Fatal(err)}
 var process *host.Proc
 process=host.Start(t.Context(),host.Spec{ID:"linuxdo",Command:node,Args:[]string{${JSON.stringify(stdio)}},Dir:root,CleanEnv:true,RPC:true,OnCall:true,
 Init:func(context.Context)(extension.InitializeParams,error){return extension.InitializeParams{Protocol:1},nil},
 OnEvent:func(event string,raw json.RawMessage){if event=="ui.page.snapshot"{h.pageSnapshot("linuxdo",process,raw)}},
 })
 h.extensions.byID[manifest.ID]=&hubExtension{id:manifest.ID,proc:process}
 t.Cleanup(func(){h.pageDropExtension("");process.Stop();h.pageStopDelivery()})
 var in uiRequest; in.Context.UIVersion=1
 resolved:=false
 for turn:=0;turn<3;turn++ {
 result,err:=h.uiCall(t.Context(),"linuxdo","hot",in);if err!=nil{t.Fatal(err)}
 for _,section:=range result.View.Sections{for _,item:=range section.Items{if item.Key=="topic-1"{resolved=true}}}
 in.State=result.View.State
 time.Sleep(250*time.Millisecond)
 }
 var stats map[string]int
 if err:=process.Call(t.Context(),"test.stats",nil,&stats);err!=nil{t.Fatal(err)}
 t.Logf("actual manager cold HTTP synthetic transport: stats=%v resolved=%v",stats,resolved)
 if !resolved{t.Fatalf("blocked by upstream #1184: HTTP bridge never displayed topics; stats=%v",stats)}
}
`;
// Keep the overlay input outside Go's package discovery even after a failed gate.
const goFile = resolve(dir, "legacy_http_test.go.txt");
await writeFile(goFile, test);
const formatted = spawnSync("gofmt", ["-w", goFile], { encoding: "utf8", timeout: 10_000 });
if (formatted.error || formatted.status !== 0) throw formatted.error ?? new Error(formatted.stderr);
const overlay = resolve(dir, "overlay.json");
await writeFile(overlay, JSON.stringify({ Replace: { [resolve(main, "server/internal/extmgr/linuxdo_upstream_cold_http_test.go")]: goFile } }));
const result = spawnSync("go", ["test", "-overlay", overlay, "./internal/extmgr", "-run", "^TestLinuxDoUpstreamColdHTTP$", "-count=1", "-timeout=30s", "-v"], { cwd: resolve(main, "server"), encoding: "utf8", timeout: 120_000 });
await writeFile(resolve(dir, "result.log"), (result.stdout ?? "") + (result.stderr ?? ""));
console.log(result.stdout ?? "");
const status = result.error?.code === "ETIMEDOUT" ? 124 : result.status ?? 1;
console.log(`upstream manager integration: ${status === 0 ? "PASS" : status === 124 ? "TIMEOUT" : "FAIL / BLOCKED"}; exit=${status}; artifacts=${dir}`);
process.exitCode = status;
