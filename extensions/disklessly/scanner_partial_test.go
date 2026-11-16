package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// probeCutAfter is sampleProbeLocal as a probe that ran out of time: everything before marker, then the start of a
// line the probe was still writing.
func probeCutAfter(t *testing.T, marker string) string {
	t.Helper()
	i := strings.Index(sampleProbeLocal, marker)
	if i < 0 {
		t.Fatalf("sample probe has no %q", marker)
	}
	return sampleProbeLocal[:i] + "cache-gomod\tgo-cache\tGo 模块"
}

func timedOutRunner(out string) Runner {
	return func(context.Context, *Machine, string, time.Duration) (string, error) {
		return out, errors.New("本地探测命令执行失败：signal: killed")
	}
}

func localState(t *testing.T) (*State, *Machine) {
	t.Helper()
	st := &State{}
	st.ensureDefaults(t0)
	m := st.machine("local")
	if m == nil {
		t.Fatal("no local machine")
	}
	return st, m
}

// A probe that read the root volume and then ran out of time sizing caches keeps the watermark.
func TestScanKeepsWatermarkWhenCacheSizingTimesOut(t *testing.T) {
	st, m := localState(t)
	out := probeCutAfter(t, "cache-gomod\t")
	scan, err := ScanMachine(context.Background(), timedOutRunner(out), st, m, t0)
	if err != nil {
		t.Fatalf("scan failed although the watermark was read: %v", err)
	}
	if !scan.Partial || !strings.Contains(scan.PartialReason, "signal: killed") {
		t.Fatalf("partial=%v reason=%q, want partial with the reason", scan.Partial, scan.PartialReason)
	}
	if scan.Mount.TotalBytes != 214748364800 || scan.Mount.AvailableBytes != 32212254720 {
		t.Fatalf("mount = %+v, want the DF section's numbers", scan.Mount)
	}
	if len(scan.Items) != 1 || scan.Items[0].ID != "cache-gobuild" {
		t.Fatalf("items = %+v, want only the one sized before the cut (the cut line is dropped)", scan.Items)
	}
	if !m.Reachable || m.LastError != "" {
		t.Fatalf("machine reachable=%v lastError=%q, want reachable", m.Reachable, m.LastError)
	}
	ms := st.machineStatus(m)
	if ms.Level != HealthWarning || !ms.Partial {
		t.Fatalf("status level=%q partial=%v, want warning (85%% used) and partial", ms.Level, ms.Partial)
	}
	if st.Scans["local"] != scan {
		t.Fatal("the partial scan was not stored")
	}
}

// A probe that stopped before its DF section was complete still fails: there is no watermark to keep.
func TestScanFailsWhenProbeStopsBeforeTheWatermark(t *testing.T) {
	st, m := localState(t)
	i := strings.Index(sampleProbeLocal, "/dev/sda1")
	out := sampleProbeLocal[:i] + "/dev/sda1   2147"
	if _, err := ScanMachine(context.Background(), timedOutRunner(out), st, m, t0); err == nil {
		t.Fatal("scan succeeded without a complete DF section")
	}
	if m.Reachable || !strings.Contains(m.LastError, "signal: killed") {
		t.Fatalf("machine reachable=%v lastError=%q, want unreachable with the error", m.Reachable, m.LastError)
	}
	if st.Scans["local"] != nil {
		t.Fatal("a scan was stored")
	}
}

// A run whose time is up returns soon even when a program the shell started still holds its output.
func TestDefaultRunnerReturnsWhenAChildHoldsOutput(t *testing.T) {
	old := runnerWaitDelay
	runnerWaitDelay = 200 * time.Millisecond
	t.Cleanup(func() { runnerWaitDelay = old })
	script := "echo @@SECTION:DF\nsleep 8 &\nwait\n"
	start := time.Now()
	out, err := DefaultRunner(context.Background(), &Machine{ID: "local", IsLocal: true}, script, 300*time.Millisecond)
	if took := time.Since(start); took > 4*time.Second {
		t.Fatalf("the run took %v: it waited for the shell's child instead of returning", took)
	}
	if err == nil {
		t.Fatal("a run past its time reported no error")
	}
	if !strings.Contains(out, "@@SECTION:DF") {
		t.Fatalf("out = %q, want what was written before the time was up", out)
	}
}
