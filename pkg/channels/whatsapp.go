package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	// Pure-Go SQLite driver (no CGO needed)
	_ "modernc.org/sqlite"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/utils"
	"github.com/sipeed/picoclaw/pkg/voice"
)

// WhatsAppChannel supports two modes:
//   - Native mode (BridgeURL == ""): connects directly to WhatsApp Web via whatsmeow
//   - Bridge mode (BridgeURL != ""): connects to an external Node.js bridge via WebSocket
type WhatsAppChannel struct {
	*BaseChannel
	config      config.WhatsAppConfig
	transcriber *voice.GroqTranscriber

	// Native mode fields
	client    *whatsmeow.Client
	container *sqlstore.Container

	// Bridge mode fields
	conn      *websocket.Conn
	url       string
	connected bool

	mu sync.Mutex
}

func NewWhatsAppChannel(cfg config.WhatsAppConfig, bus *bus.MessageBus) (*WhatsAppChannel, error) {
	base := NewBaseChannel("whatsapp", cfg, bus, cfg.AllowFrom)

	return &WhatsAppChannel{
		BaseChannel: base,
		config:      cfg,
		url:         cfg.BridgeURL,
		connected:   false,
	}, nil
}

// SetTranscriber attaches a voice transcriber for voice message support.
func (c *WhatsAppChannel) SetTranscriber(transcriber *voice.GroqTranscriber) {
	c.transcriber = transcriber
}

// ---------------------------------------------------------------------------
// Start / Stop / Send — dispatch to native or bridge mode
// ---------------------------------------------------------------------------

func (c *WhatsAppChannel) Start(ctx context.Context) error {
	if c.config.BridgeURL != "" {
		return c.startBridge(ctx)
	}
	return c.startNative(ctx)
}

func (c *WhatsAppChannel) Stop(ctx context.Context) error {
	if c.config.BridgeURL != "" {
		return c.stopBridge(ctx)
	}
	return c.stopNative(ctx)
}

func (c *WhatsAppChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if c.config.BridgeURL != "" {
		return c.sendBridge(ctx, msg)
	}
	return c.sendNative(ctx, msg)
}

// ===========================================================================
// Native mode — whatsmeow
// ===========================================================================

func (c *WhatsAppChannel) startNative(ctx context.Context) error {
	storePath := expandHomePath(c.config.StorePath)
	if storePath == "" {
		storePath = filepath.Join(os.TempDir(), "picoclaw_whatsapp.db")
	}

	if err := os.MkdirAll(filepath.Dir(storePath), 0700); err != nil {
		return fmt.Errorf("failed to create WhatsApp store directory: %w", err)
	}

	dbLog := waLog.Noop
	container, err := sqlstore.New(ctx, "sqlite", fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", storePath), dbLog)
	if err != nil {
		return fmt.Errorf("failed to open WhatsApp store: %w", err)
	}
	c.container = container

	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		return fmt.Errorf("failed to get WhatsApp device: %w", err)
	}

	clientLog := waLog.Noop
	client := whatsmeow.NewClient(deviceStore, clientLog)
	c.client = client

	client.AddEventHandler(c.handleEvent)

	if client.Store.ID == nil {
		// No session — need QR code login
		qrChan, _ := client.GetQRChannel(ctx)
		if err := client.Connect(); err != nil {
			return fmt.Errorf("WhatsApp connect failed: %w", err)
		}

		logger.InfoC("whatsapp", "Scan the QR code below to log in to WhatsApp:")
		for evt := range qrChan {
			switch evt.Event {
			case "code":
				qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
				logger.InfoC("whatsapp", "QR code displayed — scan with WhatsApp on your phone")
			case "login":
				logger.InfoC("whatsapp", "WhatsApp login successful!")
			case "timeout":
				logger.ErrorC("whatsapp", "QR code timed out. Restart to try again.")
				return fmt.Errorf("WhatsApp QR code timed out")
			}
		}
	} else {
		// Existing session — just connect
		if err := client.Connect(); err != nil {
			return fmt.Errorf("WhatsApp connect failed: %w", err)
		}
		logger.InfoC("whatsapp", "WhatsApp connected (existing session)")
	}

	c.setRunning(true)
	return nil
}

func (c *WhatsAppChannel) stopNative(_ context.Context) error {
	logger.InfoC("whatsapp", "Stopping WhatsApp native channel...")

	if c.client != nil {
		c.client.Disconnect()
	}
	if c.container != nil {
		// sqlstore.Container doesn't expose Close, handled by GC
	}

	c.setRunning(false)
	return nil
}

func (c *WhatsAppChannel) sendNative(_ context.Context, msg bus.OutboundMessage) error {
	if c.client == nil || !c.client.IsConnected() {
		return fmt.Errorf("WhatsApp native client not connected")
	}

	jid, err := types.ParseJID(msg.ChatID)
	if err != nil {
		return fmt.Errorf("invalid WhatsApp JID %q: %w", msg.ChatID, err)
	}

	_, err = c.client.SendMessage(context.Background(), jid, &waE2E.Message{
		Conversation: strPtr(msg.Content),
	})
	if err != nil {
		return fmt.Errorf("failed to send WhatsApp message: %w", err)
	}

	return nil
}

// handleEvent is the whatsmeow event dispatcher.
func (c *WhatsAppChannel) handleEvent(rawEvt interface{}) {
	switch evt := rawEvt.(type) {
	case *events.Message:
		c.handleMessageEvent(evt)
	case *events.Connected:
		logger.InfoC("whatsapp", "WhatsApp connected")
	case *events.Disconnected:
		logger.WarnC("whatsapp", "WhatsApp disconnected (will auto-reconnect)")
	case *events.LoggedOut:
		logger.ErrorC("whatsapp", "WhatsApp logged out! Delete store and re-scan QR code.")
		c.setRunning(false)
	case *events.HistorySync:
		// Ignore history sync — don't process old messages as new
	}
}

// handleMessageEvent processes an incoming WhatsApp message.
func (c *WhatsAppChannel) handleMessageEvent(evt *events.Message) {
	// Skip self-sent messages
	if evt.Info.IsFromMe {
		return
	}

	// Skip status broadcasts
	if evt.Info.Chat.Server == "broadcast" {
		return
	}

	senderID := evt.Info.Sender.String()
	chatID := evt.Info.Chat.String()
	msg := evt.Message

	var content string
	var mediaPaths []string
	var localFiles []string

	defer func() {
		for _, f := range localFiles {
			os.Remove(f)
		}
	}()

	// Extract text content
	if text := msg.GetConversation(); text != "" {
		content = text
	} else if ext := msg.GetExtendedTextMessage(); ext != nil && ext.GetText() != "" {
		content = ext.GetText()
	}

	// Image message
	if imgMsg := msg.GetImageMessage(); imgMsg != nil {
		path := c.downloadMedia(imgMsg, ".jpg")
		if path != "" {
			localFiles = append(localFiles, path)
			mediaPaths = append(mediaPaths, path)
		}
		if caption := imgMsg.GetCaption(); caption != "" {
			content = appendWhatsAppContent(content, caption)
		}
	}

	// Video message
	if vidMsg := msg.GetVideoMessage(); vidMsg != nil {
		path := c.downloadMedia(vidMsg, ".mp4")
		if path != "" {
			localFiles = append(localFiles, path)
			mediaPaths = append(mediaPaths, path)
		}
		if caption := vidMsg.GetCaption(); caption != "" {
			content = appendWhatsAppContent(content, caption)
		}
	}

	// Document message
	if docMsg := msg.GetDocumentMessage(); docMsg != nil {
		ext := ".bin"
		if fn := docMsg.GetFileName(); fn != "" {
			ext = filepath.Ext(fn)
			if ext == "" {
				ext = ".bin"
			}
		}
		path := c.downloadMedia(docMsg, ext)
		if path != "" {
			localFiles = append(localFiles, path)
			mediaPaths = append(mediaPaths, path)
		}
		if caption := docMsg.GetCaption(); caption != "" {
			content = appendWhatsAppContent(content, caption)
		}
	}

	// Audio/voice message
	if audioMsg := msg.GetAudioMessage(); audioMsg != nil {
		path := c.downloadMedia(audioMsg, ".ogg")
		if path != "" {
			localFiles = append(localFiles, path)
			mediaPaths = append(mediaPaths, path)
			content = appendWhatsAppContent(content, c.handleVoiceMessage(path))
		}
	}

	// Sticker
	if msg.GetStickerMessage() != nil {
		content = appendWhatsAppContent(content, "[sticker]")
	}

	if content == "" && len(mediaPaths) == 0 {
		return
	}

	metadata := map[string]string{
		"message_id": evt.Info.ID,
		"sender_jid": senderID,
	}
	if evt.Info.PushName != "" {
		metadata["user_name"] = evt.Info.PushName
	}
	if evt.Info.IsGroup {
		metadata["is_group"] = "true"
	}

	logger.DebugCF("whatsapp", "Message received", map[string]interface{}{
		"from":    senderID,
		"content": utils.Truncate(content, 50),
	})

	c.HandleMessage(senderID, chatID, content, mediaPaths, metadata)
}

// downloadMedia downloads a whatsmeow-downloadable message to a temp file.
func (c *WhatsAppChannel) downloadMedia(msg whatsmeow.DownloadableMessage, ext string) string {
	if c.client == nil {
		return ""
	}

	data, err := c.client.Download(context.Background(), msg)
	if err != nil {
		logger.ErrorCF("whatsapp", "Failed to download media", map[string]interface{}{
			"error": err.Error(),
		})
		return ""
	}

	mediaDir := filepath.Join(os.TempDir(), "picoclaw_media")
	os.MkdirAll(mediaDir, 0700)

	tmpFile, err := os.CreateTemp(mediaDir, "wa_*"+ext)
	if err != nil {
		logger.ErrorCF("whatsapp", "Failed to create temp file", map[string]interface{}{
			"error": err.Error(),
		})
		return ""
	}

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return ""
	}
	tmpFile.Close()

	return tmpFile.Name()
}

// handleVoiceMessage transcribes a voice message if a transcriber is available.
func (c *WhatsAppChannel) handleVoiceMessage(audioPath string) string {
	if c.transcriber == nil || !c.transcriber.IsAvailable() {
		return "[voice]"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := c.transcriber.Transcribe(ctx, audioPath)
	if err != nil {
		logger.ErrorCF("whatsapp", "Voice transcription failed", map[string]interface{}{
			"error": err.Error(),
		})
		return "[voice (transcription failed)]"
	}

	return fmt.Sprintf("[voice transcription: %s]", result.Text)
}

// ===========================================================================
// Bridge mode — WebSocket to external Node.js bridge (backward compatibility)
// ===========================================================================

func (c *WhatsAppChannel) startBridge(ctx context.Context) error {
	logger.InfoCF("whatsapp", "Starting WhatsApp bridge mode", map[string]interface{}{
		"url": c.url,
	})

	dialer := websocket.DefaultDialer
	dialer.HandshakeTimeout = 10 * time.Second

	conn, _, err := dialer.Dial(c.url, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to WhatsApp bridge: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.mu.Unlock()

	c.setRunning(true)
	logger.InfoC("whatsapp", "WhatsApp bridge connected")

	go c.listenBridge(ctx)

	return nil
}

func (c *WhatsAppChannel) stopBridge(_ context.Context) error {
	logger.InfoC("whatsapp", "Stopping WhatsApp bridge channel...")

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			logger.ErrorCF("whatsapp", "Error closing WhatsApp connection", map[string]interface{}{
				"error": err.Error(),
			})
		}
		c.conn = nil
	}

	c.connected = false
	c.setRunning(false)

	return nil
}

func (c *WhatsAppChannel) sendBridge(_ context.Context, msg bus.OutboundMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return fmt.Errorf("whatsapp bridge connection not established")
	}

	payload := map[string]interface{}{
		"type":    "message",
		"to":      msg.ChatID,
		"content": msg.Content,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	return nil
}

func (c *WhatsAppChannel) listenBridge(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			c.mu.Lock()
			conn := c.conn
			c.mu.Unlock()

			if conn == nil {
				time.Sleep(1 * time.Second)
				continue
			}

			_, message, err := conn.ReadMessage()
			if err != nil {
				logger.ErrorCF("whatsapp", "Bridge read error", map[string]interface{}{
					"error": err.Error(),
				})
				time.Sleep(2 * time.Second)
				continue
			}

			var msg map[string]interface{}
			if err := json.Unmarshal(message, &msg); err != nil {
				logger.ErrorCF("whatsapp", "Failed to unmarshal bridge message", map[string]interface{}{
					"error": err.Error(),
				})
				continue
			}

			msgType, ok := msg["type"].(string)
			if !ok {
				continue
			}

			if msgType == "message" {
				c.handleBridgeMessage(msg)
			}
		}
	}
}

func (c *WhatsAppChannel) handleBridgeMessage(msg map[string]interface{}) {
	senderID, ok := msg["from"].(string)
	if !ok {
		return
	}

	chatID, ok := msg["chat"].(string)
	if !ok {
		chatID = senderID
	}

	content, ok := msg["content"].(string)
	if !ok {
		content = ""
	}

	var mediaPaths []string
	if mediaData, ok := msg["media"].([]interface{}); ok {
		mediaPaths = make([]string, 0, len(mediaData))
		for _, m := range mediaData {
			if path, ok := m.(string); ok {
				mediaPaths = append(mediaPaths, path)
			}
		}
	}

	metadata := make(map[string]string)
	if messageID, ok := msg["id"].(string); ok {
		metadata["message_id"] = messageID
	}
	if userName, ok := msg["from_name"].(string); ok {
		metadata["user_name"] = userName
	}

	logger.DebugCF("whatsapp", "Bridge message received", map[string]interface{}{
		"from":    senderID,
		"content": utils.Truncate(content, 50),
	})

	c.HandleMessage(senderID, chatID, content, mediaPaths, metadata)
}

// ===========================================================================
// Helpers
// ===========================================================================

func appendWhatsAppContent(content, suffix string) string {
	if content == "" {
		return suffix
	}
	return content + "\n" + suffix
}

func strPtr(s string) *string {
	return &s
}

func expandHomePath(path string) string {
	if path == "" {
		return path
	}
	if path[0] == '~' {
		home, _ := os.UserHomeDir()
		if len(path) > 1 && path[1] == '/' {
			return home + path[1:]
		}
		return home
	}
	return path
}
