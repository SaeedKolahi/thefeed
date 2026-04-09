package server

import (
	"bytes"
	"context"
	cryptoRand "crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"

	_ "image/gif"
	_ "image/png"

	"github.com/sartoopjj/thefeed/internal/protocol"
)

// TelegramConfig holds Telegram API credentials.
type TelegramConfig struct {
	APIID       int
	APIHash     string
	Phone       string
	Password    string // 2FA password, empty if not used
	SessionPath string
	LoginOnly   bool // if true, authenticate and exit
	CodePrompt  func(ctx context.Context) (string, error)
}

// fileSessionStorage persists gotd session to a JSON file.
type fileSessionStorage struct {
	path string
}

func (f *fileSessionStorage) LoadSession(_ context.Context) ([]byte, error) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, session.ErrNotFound
		}
		return nil, err
	}
	return data, nil
}

func (f *fileSessionStorage) StoreSession(_ context.Context, data []byte) error {
	dir := filepath.Dir(f.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(f.path, data, 0600)
}

// TelegramReader fetches messages from Telegram channels.
type TelegramReader struct {
	cfg      TelegramConfig
	channels []string // channel usernames without @
	feed     *Feed
	msgLimit int // max messages to fetch per channel

	mu       sync.RWMutex
	cache    map[string]cachedMessages
	cacheTTL time.Duration

	// api is set once authenticated, used for sending messages.
	apiMu sync.RWMutex
	api   *tg.Client

	refreshCh chan struct{} // signals Run() to re-fetch immediately

	mediaMu    sync.RWMutex
	mediaIndex map[string]*mediaDescriptor
	enableMedia bool
}

type mediaDescriptor struct {
	location tg.InputFileLocationClass
	fileName string
	mimeType string
	preview  bool
}

// resolvedPeer holds the resolved Telegram peer along with its chat type.
type resolvedPeer struct {
	peer     tg.InputPeerClass
	chatType protocol.ChatType
	canSend  bool
}

type cachedMessages struct {
	msgs    []protocol.Message
	fetched time.Time
}

// NewTelegramReader creates a reader for the given channel usernames.
func NewTelegramReader(cfg TelegramConfig, channelUsernames []string, feed *Feed, msgLimit int, enableMedia bool) *TelegramReader {
	cleaned := make([]string, len(channelUsernames))
	for i, u := range channelUsernames {
		cleaned[i] = strings.TrimPrefix(strings.TrimSpace(u), "@")
	}
	if msgLimit <= 0 {
		msgLimit = 15
	}
	return &TelegramReader{
		cfg:       cfg,
		channels:  cleaned,
		feed:      feed,
		msgLimit:  msgLimit,
		cache:     make(map[string]cachedMessages),
		cacheTTL:  10 * time.Minute,
		refreshCh: make(chan struct{}, 1),
		mediaIndex: make(map[string]*mediaDescriptor),
		enableMedia: enableMedia,
	}
}

// Run starts the Telegram client, authenticates, and periodically fetches messages.
func (tr *TelegramReader) Run(ctx context.Context) error {
	opts := telegram.Options{}

	// Persist session to file if path is configured
	if tr.cfg.SessionPath != "" {
		opts.SessionStorage = &fileSessionStorage{path: tr.cfg.SessionPath}
	}

	client := telegram.NewClient(tr.cfg.APIID, tr.cfg.APIHash, opts)

	return client.Run(ctx, func(ctx context.Context) error {
		// Authenticate
		if err := tr.authenticate(ctx, client); err != nil {
			return fmt.Errorf("telegram auth: %w", err)
		}

		log.Println("[telegram] authenticated successfully")
		tr.feed.SetTelegramLoggedIn(true)

		// Login-only mode: just authenticate and return
		if tr.cfg.LoginOnly {
			log.Println("[telegram] login-only mode, session saved, exiting")
			return nil
		}

		api := client.API()

		tr.apiMu.Lock()
		tr.api = api
		tr.apiMu.Unlock()

		// Initial fetch
		tr.fetchAll(ctx, api)

		// Periodic fetch loop
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()

		tr.feed.SetNextFetch(uint32(time.Now().Add(10 * time.Minute).Unix()))

		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
				tr.fetchAll(ctx, api)
				tr.feed.SetNextFetch(uint32(time.Now().Add(10 * time.Minute).Unix()))
			case <-tr.refreshCh:
				// Invalidate cache so fetchAll re-fetches everything.
				tr.mu.Lock()
				tr.cache = make(map[string]cachedMessages)
				tr.mu.Unlock()
				tr.fetchAll(ctx, api)
				ticker.Reset(10 * time.Minute)
				tr.feed.SetNextFetch(uint32(time.Now().Add(10 * time.Minute).Unix()))
			}
		}
	})
}

// RequestRefresh signals the fetch loop to re-fetch immediately.
func (tr *TelegramReader) RequestRefresh() {
	select {
	case tr.refreshCh <- struct{}{}:
	default: // already pending
	}
}

// UpdateChannels replaces the channel list and updates the Feed accordingly.
func (tr *TelegramReader) UpdateChannels(channels []string) {
	cleaned := make([]string, len(channels))
	for i, u := range channels {
		cleaned[i] = strings.TrimPrefix(strings.TrimSpace(u), "@")
	}
	tr.mu.Lock()
	tr.channels = cleaned
	tr.mu.Unlock()
	tr.feed.SetChannels(cleaned)
}

func (tr *TelegramReader) authenticate(ctx context.Context, client *telegram.Client) error {
	status, err := client.Auth().Status(ctx)
	if err != nil {
		return err
	}
	if status.Authorized {
		return nil
	}

	codeAuth := auth.CodeAuthenticatorFunc(func(ctx context.Context, _ *tg.AuthSentCode) (string, error) {
		if tr.cfg.CodePrompt != nil {
			return tr.cfg.CodePrompt(ctx)
		}
		return "", fmt.Errorf("no code prompt configured")
	})

	var authConv auth.UserAuthenticator
	if tr.cfg.Password != "" {
		authConv = auth.Constant(tr.cfg.Phone, tr.cfg.Password, codeAuth)
	} else {
		authConv = auth.Constant(tr.cfg.Phone, "", codeAuth)
	}

	flow := auth.NewFlow(authConv, auth.SendCodeOptions{})
	return client.Auth().IfNecessary(ctx, flow)
}

func (tr *TelegramReader) fetchAll(ctx context.Context, api *tg.Client) {
	for i, username := range tr.channels {
		chNum := i + 1

		// Check cache
		tr.mu.RLock()
		cached, ok := tr.cache[username]
		tr.mu.RUnlock()
		if ok && time.Since(cached.fetched) < tr.cacheTTL {
			continue
		}

		// Resolve peer to get chat type info
		rp, err := tr.resolvePeer(ctx, api, username)
		if err != nil {
			log.Printf("[telegram] fetch %s: %v", username, err)
			continue
		}

		hist, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:  rp.peer,
			Limit: tr.msgLimit,
		})
		if err != nil {
			log.Printf("[telegram] fetch %s: get history: %v", username, err)
			continue
		}

		userNames := buildUserMap(hist)
		msgs, err := tr.extractMessages(hist, rp.chatType, userNames)
		if err != nil {
			log.Printf("[telegram] fetch %s: %v", username, err)
			continue
		}

		// Update cache
		tr.mu.Lock()
		tr.cache[username] = cachedMessages{msgs: msgs, fetched: time.Now()}
		tr.mu.Unlock()

		// Update feed with messages and chat type info
		tr.feed.UpdateChannel(chNum, msgs)
		tr.feed.SetChatInfo(chNum, rp.chatType, rp.canSend)
		log.Printf("[telegram] updated %s: %d messages (type=%d, canSend=%v)", username, len(msgs), rp.chatType, rp.canSend)
	}
}

// resolvePeer resolves a Telegram username to an InputPeer, handling channels,
// bots, and private chats.
func (tr *TelegramReader) resolvePeer(ctx context.Context, api *tg.Client, username string) (*resolvedPeer, error) {
	resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{
		Username: username,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", username, err)
	}

	// Check Chats first (channels, supergroups)
	for _, chat := range resolved.Chats {
		if ch, ok := chat.(*tg.Channel); ok {
			canSend := !ch.Broadcast || ch.Creator || ch.AdminRights.PostMessages
			return &resolvedPeer{
				peer: &tg.InputPeerChannel{
					ChannelID:  ch.ID,
					AccessHash: ch.AccessHash,
				},
				chatType: protocol.ChatTypeChannel,
				canSend:  canSend,
			}, nil
		}
	}

	// Check Users (bots, private chats)
	for _, u := range resolved.Users {
		if user, ok := u.(*tg.User); ok {
			return &resolvedPeer{
				peer: &tg.InputPeerUser{
					UserID:     user.ID,
					AccessHash: user.AccessHash,
				},
				chatType: protocol.ChatTypePrivate,
				canSend:  true,
			}, nil
		}
	}

	return nil, fmt.Errorf("%s not found in resolved chats or users", username)
}

func (tr *TelegramReader) fetchChannel(ctx context.Context, api *tg.Client, username string) ([]protocol.Message, error) {
	rp, err := tr.resolvePeer(ctx, api, username)
	if err != nil {
		return nil, err
	}

	hist, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer:  rp.peer,
		Limit: tr.msgLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("get history %s: %w", username, err)
	}

	userNames := buildUserMap(hist)
	return tr.extractMessages(hist, protocol.ChatTypeChannel, userNames)
}

// buildUserMap extracts a user ID → display name map from a history response.
func buildUserMap(hist tg.MessagesMessagesClass) map[int64]string {
	var users []tg.UserClass
	switch h := hist.(type) {
	case *tg.MessagesMessages:
		users = h.Users
	case *tg.MessagesMessagesSlice:
		users = h.Users
	case *tg.MessagesChannelMessages:
		users = h.Users
	}
	m := make(map[int64]string, len(users))
	for _, u := range users {
		if user, ok := u.(*tg.User); ok {
			name := user.FirstName
			if user.LastName != "" {
				name += " " + user.LastName
			}
			if name == "" {
				name = user.Username
			}
			if name != "" {
				m[user.ID] = name
			}
		}
	}
	return m
}

func (tr *TelegramReader) extractMessages(hist tg.MessagesMessagesClass, chatType protocol.ChatType, userNames map[int64]string) ([]protocol.Message, error) {
	var tgMsgs []tg.MessageClass

	switch h := hist.(type) {
	case *tg.MessagesMessages:
		tgMsgs = h.Messages
	case *tg.MessagesMessagesSlice:
		tgMsgs = h.Messages
	case *tg.MessagesChannelMessages:
		tgMsgs = h.Messages
	default:
		return nil, fmt.Errorf("unexpected messages type: %T", hist)
	}

	var msgs []protocol.Message
	for _, raw := range tgMsgs {
		msg, ok := raw.(*tg.Message)
		if !ok {
			continue
		}

		text := tr.extractText(msg)
		if text == "" {
			continue
		}

		// For private chats, prefix with the sender's name.
		if chatType == protocol.ChatTypePrivate {
			if fromID, ok := msg.GetFromID(); ok {
				if pu, ok := fromID.(*tg.PeerUser); ok {
					if name, ok := userNames[pu.UserID]; ok {
						text = name + ": " + text
					}
				}
			}
		}

		msgs = append(msgs, protocol.Message{
			ID:        uint32(msg.ID),
			Timestamp: uint32(msg.Date),
			Text:      text,
		})
	}

	return msgs, nil
}

func (tr *TelegramReader) extractText(msg *tg.Message) string {
	text := msg.Message

	mediaPrefix := ""
	if msg.Media != nil {
		switch msg.Media.(type) {
		case *tg.MessageMediaPhoto:
			mediaPrefix = protocol.MediaImage
		case *tg.MessageMediaDocument:
			mediaPrefix = tr.classifyDocument(msg.Media.(*tg.MessageMediaDocument))
		case *tg.MessageMediaGeo, *tg.MessageMediaGeoLive, *tg.MessageMediaVenue:
			mediaPrefix = protocol.MediaLocation
		case *tg.MessageMediaContact:
			mediaPrefix = protocol.MediaContact
		case *tg.MessageMediaPoll:
			mediaPrefix = protocol.MediaPoll
		}
	}

	if mediaPrefix != "" {
		if mediaToken, previewToken := tr.registerMedia(msg); mediaToken != "" {
			if text != "" {
				text += "\n"
			}
			text += "[MEDIA_TOKEN:" + mediaToken + "]"
			if previewToken != "" {
				text += "\n[MEDIA_PREVIEW_TOKEN:" + previewToken + "]"
			}
		}
		if text != "" {
			return mediaPrefix + "\n" + text
		}
		return mediaPrefix
	}

	return text
}

func (tr *TelegramReader) registerMedia(msg *tg.Message) (string, string) {
	if !tr.enableMedia {
		return "", ""
	}
	if msg.Media == nil {
		return "", ""
	}

	tokenBase := fmt.Sprintf("%d:%d", msg.PeerID.TypeID(), msg.ID)
	sum := sha1.Sum([]byte(tokenBase))
	mediaToken := hex.EncodeToString(sum[:8])
	previewSum := sha1.Sum([]byte(tokenBase + ":preview"))
	previewToken := hex.EncodeToString(previewSum[:8])

	var mainDesc *mediaDescriptor
	var previewDesc *mediaDescriptor
	switch media := msg.Media.(type) {
	case *tg.MessageMediaPhoto:
		photo, ok := media.Photo.(*tg.Photo)
		if !ok {
			return "", ""
		}
		mainLoc := &tg.InputPhotoFileLocation{
			ID:            photo.ID,
			AccessHash:    photo.AccessHash,
			FileReference: photo.FileReference,
			ThumbSize:     bestPhotoSizeType(photo.Sizes, false),
		}
		mainDesc = &mediaDescriptor{
			location: mainLoc,
			fileName: fmt.Sprintf("photo_%d.jpg", photo.ID),
			mimeType: "image/jpeg",
		}
		previewType := bestPhotoSizeType(photo.Sizes, true)
		if previewType != "" {
			previewLoc := &tg.InputPhotoFileLocation{
				ID:            photo.ID,
				AccessHash:    photo.AccessHash,
				FileReference: photo.FileReference,
				ThumbSize:     previewType,
			}
			previewDesc = &mediaDescriptor{
				location: previewLoc,
				fileName: fmt.Sprintf("photo_%d_preview.jpg", photo.ID),
				mimeType: "image/jpeg",
				preview:  true,
			}
		}
	case *tg.MessageMediaDocument:
		doc, ok := media.Document.(*tg.Document)
		if !ok {
			return "", ""
		}
		mainDesc = &mediaDescriptor{
			location: doc.AsInputDocumentFileLocation(),
			fileName: documentFileName(doc),
			mimeType: doc.MimeType,
		}
		if thumbType := bestPhotoSizeType(doc.Thumbs, true); thumbType != "" {
			previewLoc := doc.AsInputDocumentFileLocation()
			previewLoc.ThumbSize = thumbType
			previewDesc = &mediaDescriptor{
				location: previewLoc,
				fileName: "preview_" + documentFileName(doc) + ".jpg",
				mimeType: "image/jpeg",
				preview:  true,
			}
		}
	default:
		return "", ""
	}

	tr.mediaMu.Lock()
	tr.mediaIndex[mediaToken] = mainDesc
	if previewDesc != nil {
		tr.mediaIndex[previewToken] = previewDesc
	}
	tr.mediaMu.Unlock()
	if previewDesc != nil {
		return mediaToken, previewToken
	}
	return mediaToken, ""
}

func bestPhotoSizeType(sizes []tg.PhotoSizeClass, small bool) string {
	bestType := ""
	bestArea := 0
	if small {
		bestArea = 1 << 30
	}
	for _, sz := range sizes {
		switch s := sz.(type) {
		case *tg.PhotoSize:
			area := s.W * s.H
			if (!small && area > bestArea) || (small && area > 0 && area < bestArea) {
				bestArea = area
				bestType = s.Type
			}
		case *tg.PhotoSizeProgressive:
			area := s.W * s.H
			if (!small && area > bestArea) || (small && area > 0 && area < bestArea) {
				bestArea = area
				bestType = s.Type
			}
		}
	}
	if small && bestArea == 1<<30 {
		return ""
	}
	return bestType
}

func documentFileName(doc *tg.Document) string {
	for _, attr := range doc.Attributes {
		if f, ok := attr.(*tg.DocumentAttributeFilename); ok && strings.TrimSpace(f.FileName) != "" {
			return f.FileName
		}
	}
	return fmt.Sprintf("file_%d", doc.ID)
}

// DownloadMedia downloads media bytes for a previously indexed media token.
func (tr *TelegramReader) DownloadMedia(ctx context.Context, token string) ([]byte, string, string, error) {
	tr.apiMu.RLock()
	api := tr.api
	tr.apiMu.RUnlock()
	if api == nil {
		return nil, "", "", fmt.Errorf("not authenticated")
	}

	tr.mediaMu.RLock()
	desc, ok := tr.mediaIndex[token]
	tr.mediaMu.RUnlock()
	if !ok || desc == nil {
		return nil, "", "", fmt.Errorf("media token not found")
	}

	var out bytes.Buffer
	dl := downloader.NewDownloader().WithPartSize(64 * 1024)
	if _, err := dl.Download(api, desc.location).Stream(ctx, &out); err != nil {
		return nil, "", "", fmt.Errorf("download media: %w", err)
	}
	data := out.Bytes()
	if desc.preview {
		data = shrinkPreview(data)
	}

	return data, desc.fileName, desc.mimeType, nil
}

func shrinkPreview(data []byte) []byte {
	const maxSide = 72
	const jpegQuality = 30

	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data
	}
	_ = format // keep decode format for future tuning

	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return data
	}

	nw, nh := w, h
	if w > maxSide || h > maxSide {
		if w >= h {
			nw = maxSide
			nh = h * maxSide / w
		} else {
			nh = maxSide
			nw = w * maxSide / h
		}
		if nw < 1 {
			nw = 1
		}
		if nh < 1 {
			nh = 1
		}
	}

	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	scaleNearest(dst, img, b)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return data
	}
	if buf.Len() == 0 {
		return data
	}
	// Keep original if re-encode unexpectedly gets bigger.
	if buf.Len() >= len(data) {
		return data
	}
	return buf.Bytes()
}

func scaleNearest(dst *image.RGBA, src image.Image, srcBounds image.Rectangle) {
	dw := dst.Bounds().Dx()
	dh := dst.Bounds().Dy()
	sw := srcBounds.Dx()
	sh := srcBounds.Dy()
	if dw <= 0 || dh <= 0 || sw <= 0 || sh <= 0 {
		return
	}
	for y := 0; y < dh; y++ {
		sy := srcBounds.Min.Y + (y*sh)/dh
		for x := 0; x < dw; x++ {
			sx := srcBounds.Min.X + (x*sw)/dw
			c := color.RGBAModel.Convert(src.At(sx, sy)).(color.RGBA)
			dst.SetRGBA(x, y, c)
		}
	}
}


func (tr *TelegramReader) classifyDocument(media *tg.MessageMediaDocument) string {
	doc, ok := media.Document.(*tg.Document)
	if !ok {
		return protocol.MediaFile
	}

	for _, attr := range doc.Attributes {
		switch attr.(type) {
		case *tg.DocumentAttributeVideo:
			return protocol.MediaVideo
		case *tg.DocumentAttributeAudio:
			return protocol.MediaAudio
		case *tg.DocumentAttributeSticker:
			return protocol.MediaSticker
		case *tg.DocumentAttributeAnimated:
			return protocol.MediaGIF
		}
	}

	return protocol.MediaFile
}

// SendMessage sends a text message to the given channel/chat (1-indexed).
func (tr *TelegramReader) SendMessage(ctx context.Context, channelNum int, text string) error {
	if channelNum < 1 || channelNum > len(tr.channels) {
		return fmt.Errorf("invalid channel number: %d", channelNum)
	}

	tr.apiMu.RLock()
	api := tr.api
	tr.apiMu.RUnlock()
	if api == nil {
		return fmt.Errorf("not authenticated")
	}

	username := tr.channels[channelNum-1]
	rp, err := tr.resolvePeer(ctx, api, username)
	if err != nil {
		return fmt.Errorf("send resolve: %w", err)
	}

	var ridBuf [8]byte
	if _, ridErr := cryptoRand.Read(ridBuf[:]); ridErr != nil {
		return fmt.Errorf("generate random id: %w", ridErr)
	}
	randomID := int64(ridBuf[0]) | int64(ridBuf[1])<<8 | int64(ridBuf[2])<<16 | int64(ridBuf[3])<<24 |
		int64(ridBuf[4])<<32 | int64(ridBuf[5])<<40 | int64(ridBuf[6])<<48 | int64(ridBuf[7])<<56

	_, err = api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
		Peer:     rp.peer,
		Message:  text,
		RandomID: randomID,
	})
	if err != nil {
		return fmt.Errorf("send to %s: %w", username, err)
	}

	log.Printf("[telegram] sent message to %s (%d chars)", username, len(text))
	return nil
}
