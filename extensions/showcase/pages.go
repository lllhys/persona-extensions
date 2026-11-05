package main

import (
	"fmt"
	"strings"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The pages of single components. Every one shows each state the component has; the apps' screenshots compare them.

var tones = []struct{ tone, name string }{
	{wire.ToneNeutral, "neutral"}, {wire.TonePositive, "positive"}, {wire.ToneCaution, "caution"}, {wire.ToneDanger, "danger"},
}

func ratio(f float64) *float64 { return &f }

func items(c ...wire.Component) []wire.Component { return c }

// long is a paragraph long enough to wrap several lines (and a title long enough to be cut short on one line).
var long = strings.Repeat("这是一段很长的文字，用来看换行和截断：各端应当在一行放不下时换行，而标题、按钮这类单行的地方截断。", 4)

func (a *app) text(r *ext.UIRequest, s state) *wire.View {
	return &wire.View{Sections: []wire.Section{
		{Title: "四种样式", Items: items(
			wire.Component{Type: wire.ComponentText, Text: "body：正文，默认的样式。"},
			wire.Component{Type: wire.ComponentText, Style: wire.TextStyleSecondary, Text: "secondary：次要的说明。"},
			wire.Component{Type: wire.ComponentText, Style: wire.TextStyleCaption, Text: "caption：最小的注脚。"},
			wire.Component{Type: wire.ComponentText, Style: wire.TextStyleCode, Text: "code: retry=3 timeout=30s"},
		)},
		{Title: "长文字", Footer: "节的页脚：在最后一组下面。", Items: items(
			wire.Component{Type: wire.ComponentText, Text: long},
		)},
	}}
}

func (a *app) metric(r *ext.UIRequest, s state) *wire.View {
	c := []wire.Component{{Type: wire.ComponentMetric, Label: "没有语气", Value: "1,234"}}
	for _, t := range tones {
		c = append(c, wire.Component{Type: wire.ComponentMetric, Label: "语气 " + t.name, Value: "¥36.20", Detail: "比昨天 +12%", Tone: t.tone})
	}
	c = append(c, wire.Component{Type: wire.ComponentMetric, Label: "很长的标签：" + strings.Repeat("长", 30), Value: strings.Repeat("9", 24), Detail: long[:120]})
	return &wire.View{Sections: []wire.Section{{Items: c}}}
}

func (a *app) progress(r *ext.UIRequest, s state) *wire.View {
	c := []wire.Component{
		{Type: wire.ComponentProgress, Label: "0", Ratio: ratio(0), Detail: "还没开始"},
		{Type: wire.ComponentProgress, Label: "一部分", Ratio: ratio(0.3), Detail: "30%"},
		{Type: wire.ComponentProgress, Label: "满", Ratio: ratio(1), Detail: "100%"},
	}
	for _, t := range tones {
		c = append(c, wire.Component{Type: wire.ComponentProgress, Label: "语气 " + t.name, Ratio: ratio(0.75), Tone: t.tone})
	}
	return &wire.View{Sections: []wire.Section{{Items: c}}}
}

func (a *app) row(r *ext.UIRequest, s state) *wire.View {
	badges := []wire.Component{}
	for _, t := range tones {
		badges = append(badges, wire.Component{Type: wire.ComponentRow, Key: "badge-" + t.name, Title: "角标 " + t.name,
			Badge: &wire.Badge{Text: t.name, Tone: t.tone}})
	}
	return &wire.View{Sections: []wire.Section{
		{Title: "文字", Items: items(
			wire.Component{Type: wire.ComponentRow, Key: "plain", Title: "只有标题"},
			wire.Component{Type: wire.ComponentRow, Key: "sub", Title: "标题", Subtitle: "副标题：一行说明"},
			wire.Component{Type: wire.ComponentRow, Key: "value", Title: "带值", Value: "42 条"},
			wire.Component{Type: wire.ComponentRow, Key: "long", Title: "很长的标题：" + long[:150], Subtitle: long[:150], Value: "很长的值"},
			wire.Component{Type: wire.ComponentRow, Key: "tap", Title: "点一下（回一句提示）", Subtitle: "有动作的行", Action: &wire.Action{ID: "toast"}},
		)},
		{Title: "角标", Items: badges},
		{Title: "图标、头像、缩略图", Items: items(
			wire.Component{Type: wire.ComponentRow, Key: "icon", Title: "图标 star", Icon: wire.NavIconStar},
			wire.Component{Type: wire.ComponentRow, Key: "avatar-text", Title: "首字母头像", Subtitle: "avatar.text", Avatar: &wire.Avatar{Text: "Al"}},
			wire.Component{Type: wire.ComponentRow, Key: "avatar-url", Title: "图片头像", Subtitle: "avatar.url（加载不了画首字母）",
				Avatar: &wire.Avatar{URL: "https://avatars.githubusercontent.com/u/9919?s=96", Text: "G"}},
			wire.Component{Type: wire.ComponentRow, Key: "thumb-square", Title: "方形缩略图", Subtitle: "thumbnail square",
				Thumbnail: &wire.Thumbnail{URL: "https://picsum.photos/seed/persona-showcase-1/160", Aspect: wire.ThumbnailAspectSquare}},
			wire.Component{Type: wire.ComponentRow, Key: "thumb-wide", Title: "宽缩略图", Subtitle: "thumbnail wide",
				Thumbnail: &wire.Thumbnail{URL: "https://picsum.photos/seed/persona-showcase-2/320/180", Aspect: wire.ThumbnailAspectWide}},
			wire.Component{Type: wire.ComponentRow, Key: "thumb-broken", Title: "加载不了的缩略图", Subtitle: "画占位",
				Thumbnail: &wire.Thumbnail{URL: "https://example.invalid/none.png"}},
		)},
	}}
}

func (a *app) button(r *ext.UIRequest, s state) *wire.View {
	return &wire.View{Sections: []wire.Section{{Items: items(
		wire.Component{Type: wire.ComponentButton, Title: "普通按钮", Action: &wire.Action{ID: "toast"}},
		wire.Component{Type: wire.ComponentButton, Title: "危险按钮（先确认）", Role: wire.ButtonRoleDestructive,
			Action: &wire.Action{ID: "delete", Confirm: "真的要删掉吗？这只是展示，不会删任何东西。"}},
		wire.Component{Type: wire.ComponentButton, Title: "打开一个网址", Action: &wire.Action{ID: "open-url"}},
		wire.Component{Type: wire.ComponentButton, Title: "很长的按钮文字也只占一行：" + strings.Repeat("长", 30), Action: &wire.Action{ID: "toast"}},
	)}}}
}

func (a *app) link(r *ext.UIRequest, s state) *wire.View {
	return &wire.View{Sections: []wire.Section{{Items: items(
		wire.Component{Type: wire.ComponentLink, Title: "example.com", URL: "https://example.com/"},
		wire.Component{Type: wire.ComponentLink, Title: "很长的链接标题：" + strings.Repeat("长", 30), URL: "https://example.com/a/very/long/path?with=query"},
	)}}}
}

func (a *app) field(r *ext.UIRequest, s state) *wire.View {
	return &wire.View{Sections: []wire.Section{
		{Title: "五种输入", Items: items(
			wire.Component{Type: wire.ComponentField, Name: "text", Label: "文字", Kind: wire.FieldKindText, Value: "预填的文字", Placeholder: "写点什么"},
			wire.Component{Type: wire.ComponentField, Name: "empty", Label: "空的文字", Kind: wire.FieldKindText, Placeholder: "占位提示"},
			wire.Component{Type: wire.ComponentField, Name: "secret", Label: "密钥", Kind: wire.FieldKindSecret, Placeholder: "不回显、不预填"},
			wire.Component{Type: wire.ComponentField, Name: "number", Label: "数字", Kind: wire.FieldKindNumber, Value: "42"},
			wire.Component{Type: wire.ComponentField, Name: "toggle", Label: "开关", Kind: wire.FieldKindToggle, Value: "true"},
			wire.Component{Type: wire.ComponentField, Name: "choice", Label: "选择", Kind: wire.FieldKindChoice, Value: "b",
				Choices: []wire.Choice{{Value: "a", Title: "选项一"}, {Value: "b", Title: "选项二"}, {Value: "c", Title: "选项三"}}},
		)},
		{Items: items(
			wire.Component{Type: wire.ComponentButton, Title: "提交", Action: &wire.Action{ID: "submit", Submit: true}},
		)},
	}}
}

var days = []string{"10-30", "10-31", "11-01", "11-02", "11-03", "11-04", "11-05"}

func series(name, tone string, values ...float64) wire.ChartSeries {
	s := wire.ChartSeries{Name: name, Tone: tone}
	for i, v := range values {
		s.Points = append(s.Points, wire.ChartPoint{Label: days[i], Value: v})
	}
	return s
}

func (a *app) chart(r *ext.UIRequest, s state) *wire.View {
	in := series("输入", "", 1200000, 800000, 1500000, 900000, 2100000, 1700000, 600000)
	out := series("输出", "", 300000, 250000, 410000, 200000, 520000, 380000, 150000)
	money := wire.ChartSeries{}
	for i, v := range []float64{1.2, 0.8, 2.4, 1.6, 3.1, 2.2, 0.5} {
		money.Points = append(money.Points, wire.ChartPoint{Label: days[i], Value: v, Display: fmt.Sprintf("¥%.2f", v)})
	}
	neg := series("差额", wire.ToneDanger, 5, -3, 2, -8, 4, 0, -1)
	return &wire.View{Sections: []wire.Section{
		{Title: "柱状", Items: items(wire.Component{Type: wire.ComponentChart, Kind: wire.ChartKindBar, Title: "近 7 天", Unit: "tokens",
			Series: []wire.ChartSeries{in, out}, Detail: "两个系列并排"})},
		{Title: "叠起来", Items: items(wire.Component{Type: wire.ComponentChart, Kind: wire.ChartKindBar, Title: "近 7 天", Unit: "tokens", Stacked: true,
			Series: []wire.ChartSeries{in, out}})},
		{Title: "折线", Items: items(wire.Component{Type: wire.ComponentChart, Kind: wire.ChartKindLine, Title: "费用", Series: []wire.ChartSeries{money},
			Detail: "一个系列、display 写法"})},
		{Title: "有负数、有语气", Items: items(wire.Component{Type: wire.ComponentChart, Kind: wire.ChartKindBar, Series: []wire.ChartSeries{neg}})},
	}}
}

func (a *app) statGrid(r *ext.UIRequest, s state) *wire.View {
	all := []wire.Stat{
		{Label: "今天", Value: "1.2M", Detail: "+12% 比昨天", Tone: wire.ToneCaution},
		{Label: "近 7 天", Value: "8.4M"},
		{Label: "费用", Value: "¥36.20", Detail: "预算内", Tone: wire.TonePositive},
		{Label: "错误", Value: "3", Detail: "比昨天多", Tone: wire.ToneDanger},
		{Label: "请求", Value: "12,034", Detail: "平稳", Tone: wire.ToneNeutral},
		{Label: "很长的标签" + strings.Repeat("长", 10), Value: "999,999,999"},
	}
	var sections []wire.Section
	for n := 1; n <= len(all); n++ {
		sections = append(sections, wire.Section{Title: fmt.Sprintf("%d 个", n), Items: items(
			wire.Component{Type: wire.ComponentStatGrid, Stats: all[:n]})})
	}
	return &wire.View{Sections: sections}
}

func (a *app) segmented(r *ext.UIRequest, s state) *wire.View {
	rng := s.Range
	if rng == "" {
		rng = "7d"
	}
	n := map[string]int{"1d": 1, "7d": 7, "30d": 7}[rng]
	ch := series("用量", "", 3, 5, 2, 8, 6, 4, 7)
	ch.Points = ch.Points[len(ch.Points)-n:]
	return &wire.View{Sections: []wire.Section{{Items: items(
		wire.Component{Type: wire.ComponentSegmented, Name: "range", Value: rng, Action: &wire.Action{ID: "range"},
			Choices: []wire.Choice{{Value: "1d", Title: "今天"}, {Value: "7d", Title: "7 天"}, {Value: "30d", Title: "30 天"}}},
		wire.Component{Type: wire.ComponentChart, Kind: wire.ChartKindBar, Series: []wire.ChartSeries{ch}, Detail: "选的是 " + rng},
		wire.Component{Type: wire.ComponentSegmented, Name: "answer", Value: orDefault(s.Answer, "yes"), Action: &wire.Action{ID: "answer"},
			Choices: []wire.Choice{{Value: "yes", Title: "是"}, {Value: "no", Title: "否"}}},
	)}}}
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

const markdownAll = "# 一级标题\n\n## 二级标题\n\n### 三级标题\n\n" +
	"一段正文，有 **粗体**、*斜体*、`行内代码` 和一个 [链接](https://example.com/)。\n" +
	"同一段的第二行。\n\n" +
	"- 无序列表\n- 第二项，**带粗体**\n  - 嵌套的一项\n\n" +
	"1. 有序列表\n2. 第二项\n3. 第三项\n\n" +
	"> 引用：一段别人说的话。\n\n" +
	"```\nfunc main() {\n\tfmt.Println(\"代码块\")\n}\n```\n\n" +
	"HTML 原样显示成文字：<b>不是粗体</b>。\n"

func (a *app) markdown(r *ext.UIRequest, s state) *wire.View {
	return &wire.View{Sections: []wire.Section{{Items: items(wire.Component{Type: wire.ComponentMarkdown, Text: markdownAll})}}}
}

func (a *app) table(r *ext.UIRequest, s state) *wire.View {
	wideCols := []wire.TableColumn{{Title: "日期"}}
	for _, m := range []string{"模型 A", "模型 B", "模型 C", "模型 D", "模型 E", "模型 F"} {
		wideCols = append(wideCols, wire.TableColumn{Title: m, Align: wire.TableAlignEnd})
	}
	wideCols = append(wideCols, wire.TableColumn{Title: "备注"})
	var wideRows [][]string
	for i, d := range days {
		row := []string{d}
		for j := 0; j < 6; j++ {
			row = append(row, fmt.Sprintf("%d,%03d", (i+1)*(j+2), (i*37+j*11)%1000))
		}
		wideRows = append(wideRows, append(row, "一段比较长的备注文字"))
	}
	return &wire.View{Sections: []wire.Section{
		{Title: "窄表", Items: items(wire.Component{Type: wire.ComponentTable, Caption: "本周的条数",
			Columns: []wire.TableColumn{{Title: "日期"}, {Title: "条数", Align: wire.TableAlignEnd}},
			Rows:    [][]string{{"11-01", "12"}, {"11-02", "8"}, {"11-03", "1,024"}}})},
		{Title: "宽表（8 列，窄屏横向滚动）", Items: items(wire.Component{Type: wire.ComponentTable, Columns: wideCols, Rows: wideRows})},
	}}
}

func (a *app) empty(r *ext.UIRequest, s state) *wire.View {
	return &wire.View{Sections: []wire.Section{
		{Title: "有按钮", Items: items(wire.Component{Type: wire.ComponentEmpty, Title: "还没有附件", Detail: "拖一个文件进来，或者点下面的按钮。",
			Icon: wire.NavIconFolder, Button: &wire.EmptyButton{Title: "上传", Action: wire.Action{ID: "toast"}}})},
		{Title: "没按钮", Items: items(wire.Component{Type: wire.ComponentEmpty, Title: "没有结果"})},
	}}
}

func (a *app) loading(r *ext.UIRequest, s state) *wire.View {
	return &wire.View{Sections: []wire.Section{
		{Title: "有字", Items: items(wire.Component{Type: wire.ComponentLoading, Title: "正在同步"})},
		{Title: "没字", Items: items(wire.Component{Type: wire.ComponentLoading})},
	}}
}

func (a *app) divider(r *ext.UIRequest, s state) *wire.View {
	return &wire.View{Sections: []wire.Section{{Title: "节的标题跟第一组", Footer: "页脚跟最后一组", Items: items(
		wire.Component{Type: wire.ComponentRow, Title: "第一组的一行"},
		wire.Component{Type: wire.ComponentRow, Title: "第一组的另一行"},
		wire.Component{Type: wire.ComponentDivider},
		wire.Component{Type: wire.ComponentRow, Title: "第二组的一行"},
		wire.Component{Type: wire.ComponentDivider},
		wire.Component{Type: wire.ComponentText, Text: "第三组：一段字"},
	)}}}
}
