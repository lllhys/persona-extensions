package ilink

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Wire constants (openclaw-weixin src/api/types.ts).
const (
	typeUser = 1
	typeBot  = 2

	stateFinish = 2

	itemText  = 1
	itemImage = 2
	itemVoice = 3
	itemFile  = 4
	itemVideo = 5

	typingOn     = 1
	typingCancel = 2
)

type wireMedia struct {
	EncryptQueryParam string `json:"encrypt_query_param"`
	AESKey            string `json:"aes_key"`
	FullURL           string `json:"full_url"`
}

type wireItem struct {
	Type   int `json:"type"`
	RefMsg *struct {
		Title       string    `json:"title"`
		MessageItem *wireItem `json:"message_item"`
	} `json:"ref_msg"`
	TextItem *struct {
		Text string `json:"text"`
	} `json:"text_item"`
	ImageItem *struct {
		Media  *wireMedia `json:"media"`
		AESKey string     `json:"aeskey"`
	} `json:"image_item"`
	VoiceItem *struct {
		Media *wireMedia `json:"media"`
		Text  string     `json:"text"`
	} `json:"voice_item"`
	FileItem *struct {
		Media    *wireMedia `json:"media"`
		FileName string     `json:"file_name"`
		Len      string     `json:"len"`
	} `json:"file_item"`
	VideoItem *struct {
		Media *wireMedia `json:"media"`
	} `json:"video_item"`
}

type wireMessage struct {
	Seq          *int64          `json:"seq"`
	MessageID    json.RawMessage `json:"message_id"`
	FromUserID   string          `json:"from_user_id"`
	ToUserID     string          `json:"to_user_id"`
	ClientID     string          `json:"client_id"`
	CreateTimeMS int64           `json:"create_time_ms"`
	MessageType  *int            `json:"message_type"`
	Items        []wireItem      `json:"item_list"`
	ContextToken string          `json:"context_token"`
}

// MediaKind is what a Media is.
type MediaKind string

const (
	MediaImage MediaKind = "image"
	MediaVoice MediaKind = "voice"
	MediaFile  MediaKind = "file"
	MediaVideo MediaKind = "video"
)

// Media is a reference to an encrypted file on WeChat's CDN; Download fetches and decrypts it.
type Media struct {
	Kind     MediaKind
	URL      string
	Query    string
	KeyB64   string
	KeyHex   string
	FileName string
	Size     int64
}

// Message is a message a user sent the bot.
type Message struct {
	// ID is stable across redeliveries: the client id, else the message id, else the sequence number.
	ID           string
	From         string
	ContextToken string
	At           int64 // milliseconds
	// Text joins the text items; a voice message contributes WeChat's own transcript.
	Text  string
	Media []Media
	// Quote is the message this one replies to, as text (its title and text), "" when none.
	Quote string
}

func decode(m wireMessage) (Message, bool) {
	if m.MessageType != nil && *m.MessageType != typeUser {
		return Message{}, false
	}
	out := Message{ID: messageID(m), From: strings.TrimSpace(m.FromUserID), ContextToken: strings.TrimSpace(m.ContextToken), At: m.CreateTimeMS}
	if out.ID == "" || out.From == "" || out.ContextToken == "" {
		return Message{}, false
	}
	var text []string
	for _, it := range m.Items {
		if out.Quote == "" && it.RefMsg != nil {
			out.Quote = quote(it)
		}
		switch it.Type {
		case itemText:
			if it.TextItem != nil && it.TextItem.Text != "" {
				text = append(text, it.TextItem.Text)
			}
		case itemVoice:
			if it.VoiceItem != nil && it.VoiceItem.Text != "" {
				text = append(text, it.VoiceItem.Text)
			}
		}
		if md, ok := media(it); ok {
			out.Media = append(out.Media, md)
		}
	}
	out.Text = strings.Join(text, "\n")
	return out, true
}

func messageID(m wireMessage) string {
	if s := strings.TrimSpace(m.ClientID); s != "" {
		return "client:" + s
	}
	if raw := strings.TrimSpace(string(m.MessageID)); raw != "" && raw != "null" {
		var s string
		if json.Unmarshal(m.MessageID, &s) == nil {
			if s = strings.TrimSpace(s); s != "" {
				return "message:" + s
			}
		} else if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return "message:" + strconv.FormatInt(n, 10)
		}
	}
	if m.Seq != nil {
		return "seq:" + strconv.FormatInt(*m.Seq, 10)
	}
	return ""
}

func quote(it wireItem) string {
	r := it.RefMsg
	var parts []string
	if t := strings.TrimSpace(r.Title); t != "" {
		parts = append(parts, t)
	}
	if q := r.MessageItem; q != nil {
		switch {
		case q.Type == itemText && q.TextItem != nil:
			parts = append(parts, q.TextItem.Text)
		case q.Type == itemVoice && q.VoiceItem != nil:
			parts = append(parts, q.VoiceItem.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func media(it wireItem) (Media, bool) {
	ref := func(k MediaKind, w *wireMedia) Media {
		m := Media{Kind: k}
		if w != nil {
			m.URL, m.Query, m.KeyB64 = w.FullURL, w.EncryptQueryParam, w.AESKey
		}
		return m
	}
	switch {
	case it.Type == itemImage && it.ImageItem != nil:
		m := ref(MediaImage, it.ImageItem.Media)
		m.KeyHex = it.ImageItem.AESKey
		return m, true
	case it.Type == itemVoice && it.VoiceItem != nil:
		return ref(MediaVoice, it.VoiceItem.Media), true
	case it.Type == itemFile && it.FileItem != nil:
		m := ref(MediaFile, it.FileItem.Media)
		m.FileName = it.FileItem.FileName
		m.Size, _ = strconv.ParseInt(strings.TrimSpace(it.FileItem.Len), 10, 64)
		return m, true
	case it.Type == itemVideo && it.VideoItem != nil:
		return ref(MediaVideo, it.VideoItem.Media), true
	}
	return Media{}, false
}
