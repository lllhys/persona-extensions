package main

import "strings"

// texts are the page's words; the hub sends the user's language with each request. Provider names and the fixes for a
// source come from the source itself, so this page has no service-specific words.
type texts struct {
	title                string
	allSources           string
	nowPlaying           string
	nothingPlaying       string
	recentlyPlayed       string
	noRecent             string
	playlists            string
	noPlaylists          string
	tracks               string
	noTracks             string
	showingTracks        string
	showingRange         string
	previousTracks       string
	nextTracks           string
	trackCount           string
	trackCountOne        string
	playlist             string
	refresh              string
	back                 string
	open                 string
	library              string
	tracksLabel          string
	albumsLabel          string
	artistsLabel         string
	playlistsLabel       string
	totalTime            string
	sourceError          string
	sourceNeedsAttention string
	connectNamed         string
	connectMusic         string
	connectDetail        string
	connectorOff         string
	connectorOffDetail   string
	sourceLabel          string
	playbackElsewhere    string
	macTurnOn            string
	macClosed            string
	macPrivacy           string
	macOpenMusic         string
	macCheck             string
	search               string
	searchTitle          string
	searchResults        string
	noResults            string
	pause                string
	play                 string
	previous             string
	next                 string
	lastSynced           string
	playing              string
	paused               string
	stopped              string
	unknown              string
	player               string
	lyrics               string
	noLyrics             string
	lyricsUnavailable    string
	phonePlayback        string
	iphoneGuide1         string
	iphoneGuide2         string
	iphoneStale          string
	iphoneSyncAgain      string
}

var english = texts{
	title:                "Music",
	allSources:           "All sources",
	nowPlaying:           "Now playing",
	nothingPlaying:       "Nothing is playing.",
	recentlyPlayed:       "Recently played",
	noRecent:             "Nothing played recently.",
	playlists:            "Your playlists",
	noPlaylists:          "No playlists found.",
	tracks:               "Tracks",
	noTracks:             "This playlist has no tracks.",
	showingTracks:        "Showing %d–%d of %d.",
	showingRange:         "Showing %d–%d.",
	previousTracks:       "Previous tracks",
	nextTracks:           "Next tracks",
	trackCount:           "%d tracks",
	trackCountOne:        "%d track",
	playlist:             "Playlist",
	refresh:              "Refresh",
	back:                 "Back",
	open:                 "Open",
	library:              "Library",
	tracksLabel:          "Tracks",
	albumsLabel:          "Albums",
	artistsLabel:         "Artists",
	playlistsLabel:       "Playlists",
	totalTime:            "Total time",
	sourceError:          "This source could not be read",
	sourceNeedsAttention: "This source needs attention",
	connectNamed:         "Connect %s",
	connectMusic:         "Connect a music service",
	connectDetail:        "Open Settings › Connectors to sign in, or set this up under Settings › Extensions › Music.",
	connectorOff:         "Music is off",
	connectorOffDetail:   "Turn on a music connector in Settings › Connectors.",
	sourceLabel:          "Source",
	playbackElsewhere:    "Playback stays in %s's own app.",
	macTurnOn:            "Turn on a Mac in Settings › Extensions › Music.",
	macClosed:            "Music is closed",
	macPrivacy:           "Open System Settings › Privacy & Security › Automation and allow Persona to control Music.",
	macOpenMusic:         "Open Music on the Mac and try again.",
	macCheck:             "Check that the Mac is online, Persona is running there, and the extension is on for it.",
	search:               "Search music",
	searchTitle:          "Results for “%s”",
	searchResults:        "Results",
	noResults:            "Nothing found.",
	pause:                "Pause",
	play:                 "Play",
	previous:             "Previous",
	next:                 "Next",
	lastSynced:           "Last synced %s",
	playing:              "Playing",
	paused:               "Paused",
	stopped:              "Stopped",
	unknown:              "Unknown",
	player:               "Now playing",
	lyrics:               "Lyrics",
	noLyrics:             "No lyrics for this track.",
	lyricsUnavailable:    "Lyrics aren't available from this device.",
	phonePlayback:        "Playback stays in the Music app on the iPhone.",
	iphoneGuide1:         "Open Persona on your iPhone and ask the assistant to sync your music.",
	iphoneGuide2:         "The phone's Music tool reads now playing, recently played and playlists; the page then shows the last snapshot.",
	iphoneStale:          "The iPhone snapshot is more than a week old.",
	iphoneSyncAgain:      "Ask the assistant to sync your iPhone music again.",
}

var simplifiedChinese = texts{
	title:                "音乐",
	allSources:           "全部来源",
	nowPlaying:           "正在播放",
	nothingPlaying:       "当前没有播放。",
	recentlyPlayed:       "最近播放",
	noRecent:             "最近没有播放记录。",
	playlists:            "你的播放列表",
	noPlaylists:          "没有找到播放列表。",
	tracks:               "曲目",
	noTracks:             "这个播放列表还没有曲目。",
	showingTracks:        "显示第 %d–%d 首，共 %d 首。",
	showingRange:         "显示第 %d–%d 首。",
	previousTracks:       "上一批曲目",
	nextTracks:           "下一批曲目",
	trackCount:           "%d 首",
	trackCountOne:        "%d 首",
	playlist:             "播放列表",
	refresh:              "刷新",
	back:                 "返回",
	open:                 "打开",
	library:              "资料库",
	tracksLabel:          "曲目",
	albumsLabel:          "专辑",
	artistsLabel:         "艺人",
	playlistsLabel:       "播放列表",
	totalTime:            "总时长",
	sourceError:          "无法读取这个来源",
	sourceNeedsAttention: "这个来源需要处理",
	connectNamed:         "连接 %s",
	connectMusic:         "连接音乐服务",
	connectDetail:        "在「设置 › 连接器」里登录，或在「设置 › 扩展 › 音乐」里完成配置。",
	connectorOff:         "音乐已关闭",
	connectorOffDetail:   "在「设置 › 连接器」里打开一个音乐连接器。",
	sourceLabel:          "来源",
	playbackElsewhere:    "播放仍由 %s 自己的 App 控制。",
	macTurnOn:            "在「设置 › 扩展 › 音乐」里打开一台 Mac。",
	macClosed:            "音乐已关闭",
	macPrivacy:           "打开「系统设置 › 隐私与安全性 › 自动化」，允许 Persona 控制「音乐」。",
	macOpenMusic:         "在 Mac 上打开「音乐」后重试。",
	macCheck:             "检查这台 Mac 是否在线、Persona 是否在运行，以及扩展是否已为它打开。",
	search:               "搜索音乐",
	searchTitle:          "“%s”的结果",
	searchResults:        "结果",
	noResults:            "没有找到内容。",
	pause:                "暂停",
	play:                 "播放",
	previous:             "上一首",
	next:                 "下一首",
	lastSynced:           "上次同步：%s",
	playing:              "播放中",
	paused:               "已暂停",
	stopped:              "已停止",
	unknown:              "未知",
	player:               "正在播放",
	lyrics:               "歌词",
	noLyrics:             "这首歌没有歌词。",
	lyricsUnavailable:    "这台设备暂时无法提供歌词。",
	phonePlayback:        "iPhone 上的播放仍由「音乐」App 控制。",
	iphoneGuide1:         "在 iPhone 上打开 Persona，让助手同步你的音乐。",
	iphoneGuide2:         "手机上的音乐工具会读取正在播放、最近播放和播放列表，页面随后展示上次的快照。",
	iphoneStale:          "iPhone 快照已超过一周。",
	iphoneSyncAgain:      "让助手重新同步 iPhone 音乐。",
}

var japanese = texts{
	title:                "ミュージック",
	allSources:           "すべてのソース",
	nowPlaying:           "再生中",
	nothingPlaying:       "再生中の曲はありません。",
	recentlyPlayed:       "最近再生した曲",
	noRecent:             "最近の再生履歴はありません。",
	playlists:            "プレイリスト",
	noPlaylists:          "プレイリストが見つかりません。",
	tracks:               "曲",
	noTracks:             "このプレイリストには曲がありません。",
	showingTracks:        "%d～%d / %d 曲を表示しています。",
	showingRange:         "%d～%d 曲を表示しています。",
	previousTracks:       "前の曲",
	nextTracks:           "次の曲",
	trackCount:           "%d 曲",
	trackCountOne:        "%d 曲",
	playlist:             "プレイリスト",
	refresh:              "更新",
	back:                 "戻る",
	open:                 "開く",
	library:              "ライブラリ",
	tracksLabel:          "曲",
	albumsLabel:          "アルバム",
	artistsLabel:         "アーティスト",
	playlistsLabel:       "プレイリスト",
	totalTime:            "合計時間",
	sourceError:          "このソースを読み取れませんでした",
	sourceNeedsAttention: "このソースの確認が必要です",
	connectNamed:         "%s に接続",
	connectMusic:         "音楽サービスに接続",
	connectDetail:        "「設定 › コネクタ」でサインインするか、「設定 › 拡張機能 › ミュージック」で設定してください。",
	connectorOff:         "ミュージックはオフです",
	connectorOffDetail:   "「設定 › コネクタ」で音楽コネクタをオンにしてください。",
	sourceLabel:          "ソース",
	playbackElsewhere:    "再生は %s のアプリが行います。",
	macTurnOn:            "「設定 › 拡張機能 › ミュージック」で Mac をオンにしてください。",
	macClosed:            "ミュージックは終了しています",
	macPrivacy:           "「システム設定 › プライバシーとセキュリティ › オートメーション」で Persona にミュージックの制御を許可してください。",
	macOpenMusic:         "Mac でミュージックを開いて、もう一度お試しください。",
	macCheck:             "Mac がオンラインか、Persona が動作しているか、この拡張機能が有効かを確認してください。",
	search:               "音楽を検索",
	searchTitle:          "「%s」の結果",
	searchResults:        "結果",
	noResults:            "見つかりませんでした。",
	pause:                "一時停止",
	play:                 "再生",
	previous:             "前へ",
	next:                 "次へ",
	lastSynced:           "最終同期: %s",
	playing:              "再生中",
	paused:               "一時停止中",
	stopped:              "停止中",
	unknown:              "不明",
	player:               "再生中",
	lyrics:               "歌詞",
	noLyrics:             "この曲には歌詞がありません。",
	lyricsUnavailable:    "このデバイスでは歌詞を取得できません。",
	phonePlayback:        "iPhone での再生はミュージックアプリが行います。",
	iphoneGuide1:         "iPhone で Persona を開き、アシスタントに音楽を同期してもらいます。",
	iphoneGuide2:         "iPhone のミュージックツールが再生中・最近再生した曲・プレイリストを読み取り、ページに最新のスナップショットを表示します。",
	iphoneStale:          "iPhone のスナップショットは 1 週間以上前のものです。",
	iphoneSyncAgain:      "アシスタントに iPhone の音楽をもう一度同期してもらってください。",
}

func textsFor(locale string) texts {
	switch {
	case strings.HasPrefix(locale, "zh"):
		return simplifiedChinese
	case strings.HasPrefix(locale, "ja"):
		return japanese
	default:
		return english
	}
}
