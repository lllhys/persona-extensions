package verify

import (
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/voice"
)

func TestDetails(t *testing.T) {
	got := Details("project-x 在 2026-11-08 发布 build42，每天 09:00 跑，需要 15 张卡，前 4 步，11月8日")
	for _, want := range []string{"project-x", "build42", "09:00", "15", "4"} {
		if !slices.Contains(got, want) {
			t.Errorf("Details missing %q: %v", want, got)
		}
	}
	for _, not := range []string{"2026", "11", "08", "8"} {
		if slices.Contains(got, not) {
			t.Errorf("Details has %q: %v", not, got)
		}
	}
}

func detailWindow() *Window {
	w := &Window{Chat: "chats/group-x", Day: "2026-11-05", All: map[int]norm.Line{}, Shown: map[int]bool{}}
	for n, l := range map[int]norm.Line{
		1:  {N: 1, Speaker: "user-a", Body: "附件保留期怎么放长？"},
		2:  {N: 2, Speaker: "user-b", Body: "加一个 attachment_days 配置就行"},
		3:  {N: 3, Speaker: norm.Me, Body: "好，就这么做"},
		20: {N: 20, Speaker: "user-b", Body: "定时任务每天 09:00 跑"},
	} {
		w.All[n], w.Shown[n] = l, true
	}
	return w
}

func TestSupplyAddsANearbyLine(t *testing.T) {
	w := detailWindow()
	ev := []store.Evidence{{Chat: w.Chat, Day: w.Day, N: 3, Speaker: norm.Me, Quote: "好，就这么做"}}
	got, rej := w.Supply("附件保留期用 attachment_days 配置", ev)
	if rej != "" || !slices.ContainsFunc(got, func(e store.Evidence) bool { return e.N == 2 }) {
		t.Fatalf("Supply = %v, %q; want line 2 added", got, rej)
	}
}

func TestSupplyRejectsADetailNoLineNearHas(t *testing.T) {
	w := detailWindow()
	ev := []store.Evidence{{Chat: w.Chat, Day: w.Day, N: 3, Speaker: norm.Me, Quote: "好，就这么做"}}
	if _, rej := w.Supply("每天 09:00 用 LaunchAgent 发布", ev); rej != DetailNotInEvidence {
		t.Fatalf("Supply rejection = %q, want %s (LaunchAgent is nowhere)", rej, DetailNotInEvidence)
	}
}

func TestSupplyTakesSpeakersAndKnownNames(t *testing.T) {
	w := detailWindow()
	ev := []store.Evidence{{Chat: w.Chat, Day: w.Day, N: 2, Speaker: "user-b", Quote: "加一个 attachment_days 配置就行"}}
	if _, rej := w.Supply("user-b 提议加 attachment_days 配置，Avery 同意", ev, "Avery"); rej != "" {
		t.Fatalf("Supply rejection = %q, want none", rej)
	}
}

func TestSupplyCitesThePartOfACitedLineWithTheDetail(t *testing.T) {
	w := detailWindow()
	long := "前面很长的背景说明" + strings.Repeat("。", 220) + "最后决定用 attachment_days 配置"
	w.All[2] = norm.Line{N: 2, Speaker: "user-b", Body: long}
	ev := []store.Evidence{{Chat: w.Chat, Day: w.Day, N: 2, Speaker: "user-b", Quote: "前面很长的背景说明"}}
	got, rej := w.Supply("用 attachment_days 配置", ev)
	if rej != "" || len(got) != 2 || got[1].N != 2 || !strings.Contains(got[1].Quote, "attachment_days") || !strings.Contains(long, got[1].Quote) {
		t.Fatalf("Supply = %+v, %q", got, rej)
	}
}

func TestSupplyTakesAFarLineAndTheUnitsNames(t *testing.T) {
	w := detailWindow()
	w.Names = []string{"project-x"}
	ev := []store.Evidence{{Chat: w.Chat, Day: w.Day, N: 3, Speaker: norm.Me, Quote: "好，就这么做"}}
	got, rej := w.Supply("project-x 的定时任务每天 09:00 跑", ev)
	if rej != "" || len(got) != 2 || got[1].N != 20 {
		t.Fatalf("Supply = %+v, %q; want line 20 added and project-x taken from the unit's names", got, rej)
	}
}

func TestDetailsNumeralsAndCommonWords(t *testing.T) {
	w := detailWindow()
	w.All[4], w.Shown[4] = norm.Line{N: 4, Speaker: "user-a", Body: "先做前两步，#12 已经关了"}, true
	ev := []store.Evidence{{Chat: w.Chat, Day: w.Day, N: 4, Speaker: "user-a", Quote: "先做前两步，#12 已经关了"}}
	if _, rej := w.Supply("Issue #12 已关闭，先执行前2步", ev); rej != "" {
		t.Fatalf("两 for 2, and a common word for the quote's wording: rejected %q (%s)", rej, w.Missing)
	}
	if _, rej := w.Supply("先执行前3步", ev); rej != DetailNotInEvidence || w.Missing != "3" {
		t.Fatalf("a number no line has: %q %q", rej, w.Missing)
	}
}

func TestSupplySkipsTheAIsLines(t *testing.T) {
	w := detailWindow()
	w.All[5], w.Shown[5] = norm.Line{N: 5, Speaker: voice.AI, Body: "那台主机叫 host-a"}, true
	ev := []store.Evidence{{Chat: w.Chat, Day: w.Day, N: 3, Speaker: norm.Me, Quote: "好，就这么做"}}
	if _, rej := w.Supply("那台主机叫 host-a", ev); rej != DetailNotInEvidence {
		t.Fatalf("a detail only the AI said: %q", rej)
	}
}
