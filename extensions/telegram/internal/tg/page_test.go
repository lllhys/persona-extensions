package telegram

import (
	"testing"

	"github.com/gotd/td/tg"
)

// Where a page's pictures are kept comes with them: a sender's picture's data center from their profile photo, a
// photo's and a document's own (dc.go fetches each there).
func TestPagePicturesKnowTheirDataCenter(t *testing.T) {
	e := entitiesOf([]tg.UserClass{&tg.User{ID: 1, AccessHash: 11, FirstName: "示例", Photo: &tg.UserProfilePhoto{PhotoID: 501, DCID: 4}}}, nil)
	m := pageMessageOf(&tg.Message{ID: 7, PeerID: &tg.PeerUser{UserID: 1}, FromID: &tg.PeerUser{UserID: 1},
		Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 9, DCID: 2, Sizes: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "x", W: 800, H: 600}}}}}, e)
	if m.FromPhoto != 501 || m.FromDC != 4 || m.Media == nil || m.Media.Full == nil || m.Media.Full.dc != 2 {
		t.Fatalf("message %+v, media %+v", m, m.Media)
	}
	video := pageMediaOf(&tg.MessageMediaDocument{Document: &tg.Document{ID: 3, DCID: 1, MimeType: "video/mp4",
		Thumbs: []tg.PhotoSizeClass{&tg.PhotoSize{Type: "m", W: 320, H: 180}}}})
	if video == nil || video.Thumb == nil || video.Thumb.dc != 1 {
		t.Fatalf("video %+v", video)
	}
	if loc := avatarLoc(peer{Kind: 'u', ID: 1, Hash: 11}, 501, 4); loc.dc != 4 {
		t.Fatalf("avatar %+v", loc)
	}
}
