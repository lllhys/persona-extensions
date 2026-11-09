package main

import (
	"context"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The page (ui contribution "feishu-sidebar", a nav_page): the Feishu applink for the hub's public address and the
// default path, with what to do before there is one. It carries no state: the link is computed from the settings.

func (a *app) render(_ context.Context, _ *ext.UIRequest) (*wire.View, error) {
	return a.page(), nil
}

// action redraws the page (nothing on it needs an action of its own; the link opens inside the app).
func (a *app) action(_ context.Context, _ *ext.UIRequest) (*wire.ActionResult, error) {
	return &wire.ActionResult{View: a.page()}, nil
}

func text(s, style string) wire.Component {
	return wire.Component{Type: wire.ComponentText, Text: s, Style: style}
}

// page draws the link, or what to do before there is one.
func (a *app) page() *wire.View {
	if a.hubURL == "" {
		return &wire.View{Title: "飞书侧边栏", Sections: []wire.Section{{Items: []wire.Component{
			text("还没有 hub 的对外地址，拼不出链接。", wire.TextStyleBody),
			text("去「设置 › 扩展 › 飞书侧边栏」填 hub 的对外地址（飞书内置浏览器能访问到的地址），或者给 hub 配好它的对外地址（public_url）。", wire.TextStyleSecondary),
		}}}}
	}

	link, target, err := a.buildLink(a.defaultPath())
	if err != nil {
		return &wire.View{Title: "飞书侧边栏", Sections: []wire.Section{{Items: []wire.Component{
			text(clip(err.Error(), 1900), wire.TextStyleSecondary),
			text("在「设置 › 扩展 › 飞书侧边栏」里改好 hub 的对外地址和默认路径。", wire.TextStyleSecondary),
		}}}}
	}

	open := wire.Section{Title: "在飞书打开 Persona", Items: []wire.Component{
		{Type: wire.ComponentLink, Title: "在飞书侧边栏打开", URL: link},
		text("把下面的链接复制下来，发到飞书里点开（或直接用上面的入口）。", wire.TextStyleCaption),
		text(clip(link, 1900), wire.TextStyleBody),
	}}
	if len(link) > 1900 {
		open.Items = append(open.Items, text("链接太长，这里截断了：用上面的入口打开，或让助手拼一条。", wire.TextStyleCaption))
	}

	notes := wire.Section{Title: "说明", Items: []wire.Component{
		text("打开的是："+clip(target, 1900), wire.TextStyleCaption),
		text("需要飞书 3.41+（sidebar-semi）；手机端飞书可能没有侧边栏。", wire.TextStyleSecondary),
		text("第一次打开要在飞书的内置浏览器里登录一次 Persona（网页版的登录态和 App 是分开的）。", wire.TextStyleSecondary),
		text("这个扩展不向飞书发请求，也不保存任何凭证。", wire.TextStyleSecondary),
	}}

	return &wire.View{Title: "飞书侧边栏", Sections: []wire.Section{open, notes}}
}
