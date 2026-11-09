package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Tool is a tool as connector.tools.list lists it.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

var tools = []Tool{
	{Name: "feishu_sidebar_link",
		Description: "拼一个飞书 applink：在飞书里点开（或复制后发到飞书）会在侧边栏打开用户的 Persona 网页。path 是站内相对路径（如 / 或 /j/<对话id>），不填用扩展设置里的默认路径。返回拼好的链接和一句说明。",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "站内相对路径，以 / 开头，如 / 或 /j/abc123；不填用默认"}}}},
}

func (a *app) callTool(_ context.Context, name string, raw json.RawMessage) (string, error) {
	var args struct {
		Path string `json:"path"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("参数不对：%w", err)
		}
	}
	switch name {
	case "feishu_sidebar_link":
		path := strings.TrimSpace(args.Path)
		if path == "" {
			path = a.defaultPath()
		}
		return a.linkText(path)
	}
	return "", fmt.Errorf("没有工具 %s", name)
}

// linkText is the tool's answer: the link on its own line, then what it does and what it needs.
func (a *app) linkText(path string) (string, error) {
	link, target, err := a.buildLink(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s\n在飞书里打开上面的链接（或复制后发到飞书）会在侧边栏打开 Persona：%s。第一次打开要在飞书的内置浏览器里登录一次。", link, target), nil
}
