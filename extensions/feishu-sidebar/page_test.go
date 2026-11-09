package main

import (
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
)

func TestPageNotConfigured(t *testing.T) {
	v := draw(t, testApp(t, "", ""), 3)
	says(t, v, "还没有 hub 的对外地址", "设置 › 扩展 › 飞书侧边栏")
	saysNot(t, v, "applink.feishu.cn")
}

func TestPageConfigured(t *testing.T) {
	v := draw(t, testApp(t, `{"hub_url": "https://persona.example.com/", "path": "/j/abc"}`, ""), 3)
	says(t, v, "## 在飞书打开 Persona", "url=https%3A%2F%2Fpersona.example.com%2Fj%2Fabc", "飞书 3.41+", "登录", "不向飞书发请求")
	var link *wire.Component
	for i := range v.Sections {
		for j := range v.Sections[i].Items {
			if c := &v.Sections[i].Items[j]; c.Type == wire.ComponentLink {
				link = c
			}
		}
	}
	if link == nil || !strings.HasPrefix(link.URL, "https://applink.feishu.cn/client/web_url/open?mode=sidebar-semi&url=") {
		t.Fatalf("the link: %+v", link)
	}
}

func TestPageInvalidAddress(t *testing.T) {
	v := draw(t, testApp(t, `{"hub_url": "notaurl"}`, ""), 3)
	says(t, v, "不是 http(s) 地址")
	saysNot(t, v, "applink.feishu.cn")
}

func TestPageUsesDefaultPath(t *testing.T) {
	v := draw(t, testApp(t, `{"hub_url": "https://persona.example.com"}`, ""), 3)
	says(t, v, "url=https%3A%2F%2Fpersona.example.com%2F")
}
