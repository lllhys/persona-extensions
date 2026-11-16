package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// AMap's answers: the envelope every endpoint shares, its error codes
// (https://lbs.amap.com/api/webservice/guide/tools/info), and the loose string fields its v3 endpoints send.

// str is a text field of AMap's: a string, or an empty array when there is no value (v3 geocoding sends
// "township": [] and "city": [] for Beijing's districts); numbers are taken as their text.
type str string

func (s *str) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case len(b) == 0 || string(b) == "null":
		*s = ""
	case b[0] == '"':
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*s = str(v)
	case b[0] == '[':
		var vs []json.RawMessage
		if err := json.Unmarshal(b, &vs); err != nil {
			return err
		}
		var parts []string
		for _, v := range vs {
			var p str
			if err := p.UnmarshalJSON(v); err == nil && p != "" {
				parts = append(parts, string(p))
			}
		}
		*s = str(strings.Join(parts, "、"))
	case b[0] == '{':
		*s = ""
	default:
		*s = str(b)
	}
	return nil
}

func (s str) String() string { return strings.TrimSpace(string(s)) }

// num is a str read as a number; ok is false when it is empty or not one.
func (s str) num() (float64, bool) {
	f, err := strconv.ParseFloat(s.String(), 64)
	return f, err == nil
}

// envelope is what every answer starts with.
type envelope struct {
	Status   str `json:"status"`
	Info     str `json:"info"`
	InfoCode str `json:"infocode"`
}

// amapError is AMap's refusal, worded for people.
type amapError struct {
	code, info string
	msg        string
}

func (e *amapError) Error() string { return e.msg }

type errKind int

const (
	kindOther errKind = iota
	kindBusy          // too many a second or a minute, or the service is busy: try again shortly
	kindDaily         // the day's allowance is used up
	kindPlan          // the plan (or the month's allowance) is used up
	kindKey           // the key itself is refused: only a new key in settings helps
)

func (e *amapError) kind() errKind {
	switch e.code {
	case "10004", "10014", "10015", "10016", "10019", "10020", "10021":
		return kindBusy
	case "10003", "10044", "10029", "10045":
		return kindDaily
	case "40000", "40003":
		return kindPlan
	// 10002 (a service the key has not enabled) and 10012 (not enough privileges) may concern one service only, so
	// they stay errors of the request: an error status would take every tool away.
	case "10001", "10005", "10006", "10007", "10008", "10009", "10010", "10013", "10026", "10041", "40002":
		return kindKey
	}
	return kindOther
}

// keyWords is what each refusal of the key means for the user.
var keyWords = map[string]string{
	"10001": "高德说这把 Key 不对或已经过期",
	"10005": "这把 Key 开了 IP 白名单，hub 这台机器的公网 IP 不在里面",
	"10006": "这把 Key 绑定的域名无效，要用「Web服务」类型的 Key",
	"10007": "这把 Key 开了数字签名，扩展不支持签名；在高德控制台关掉数字签名",
	"10008": "这把 Key 的安全码校验没通过，多半不是「Web服务」类型的 Key",
	"10009": "这把 Key 不是「Web服务」类型的（可能是网页 JS API 或手机 SDK 用的）",
	"10010": "hub 这台机器的 IP 请求太多，被高德限制了，要在高德控制台提工单",
	"10013": "这把 Key 已经被删除",
	"10026": "这个高德账号被封禁了",
	"10041": "这把 Key 的接口权限已过期",
	"40002": "这把 Key 购买的服务已经到期",
}

// otherWords is what refusals about one request mean.
var otherWords = map[string]string{
	"10002": "这把 Key 没有开通这项服务：在高德控制台看这把 Key 的服务平台是不是「Web服务」",
	"10012": "这把 Key 没有这项服务的权限",
	"20000": "高德说参数不对",
	"20001": "高德说少了参数",
	"20002": "高德说请求方式不对",
	"20003": "高德出了未知错误，稍后再试",
	"20011": "这个地点在境外，这把 Key 没有境外权限",
	"20012": "高德拒绝了这个搜索词",
	"20800": "起点或终点不在中国境内，高德算不了这条路线",
	"20801": "起点、终点或途经点附近找不到路",
	"20802": "高德没算出路线（多半是道路不通）",
	"20803": "起点和终点离得太远，这种出行方式算不了，换驾车或公交试试",
	"30007": "高德没算出结果（RESULTS_ARE_EMPTY）：起终点可能离得太远或之间不通，核对一下地点或换一种出行方式",
}

// checkAnswer reads an answer's envelope: nil when it succeeded, else AMap's refusal worded for people.
func checkAnswer(body []byte) *amapError {
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return &amapError{code: "", info: "", msg: errBadAnswer.Error()}
	}
	code, info := env.InfoCode.String(), env.Info.String()
	if env.Status.String() == "1" && (code == "" || code == "10000") {
		return nil
	}
	e := &amapError{code: code, info: info}
	switch {
	case e.kind() == kindBusy:
		e.msg = fmt.Sprintf("高德说请求太频繁（%s），过一会儿再试", info)
	case e.kind() == kindDaily || e.kind() == kindPlan:
		e.msg = fmt.Sprintf("高德说额度用完了（%s）", info)
	case keyWords[code] != "":
		e.msg = keyWords[code] + "。请在 设置 › 扩展 › 高德地图 换一把「Web服务」类型的 Key"
	case otherWords[code] != "":
		e.msg = otherWords[code]
		if code == "20000" || code == "20001" {
			e.msg += "（" + info + "）"
		}
	case strings.HasPrefix(code, "3"):
		e.msg = fmt.Sprintf("高德服务出错了（%s %s），稍后再试", code, info)
	default:
		e.msg = fmt.Sprintf("高德没查成：%s（%s）", info, code)
	}
	return e
}
