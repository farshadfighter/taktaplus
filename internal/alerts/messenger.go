package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Messenger channel kinds: bots of the Iranian messengers Bale and Eitaa.
const (
	ChannelBale  = "bale"
	ChannelEitaa = "eitaa"
)

// Default API endpoints; ChannelConfig.APIBase overrides them (for a
// proxy, or in tests).
var messengerBase = map[string]string{
	ChannelBale:  "https://tapi.bale.ai",
	ChannelEitaa: "https://eitaayar.ir/api",
}

var messengerClient = &http.Client{Timeout: 60 * time.Second}

func validMessenger(c *ChannelConfig) error {
	if strings.TrimSpace(c.BotToken) == "" {
		return invalidf("bot token is required")
	}
	if strings.ContainsAny(c.BotToken, "/?# ") {
		return invalidf("bot token is not valid")
	}
	if strings.TrimSpace(c.ChatID) == "" {
		return invalidf("chat or channel ID is required")
	}
	if c.APIBase != "" {
		u, err := url.Parse(c.APIBase)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return invalidf("API address must be an http(s) URL")
		}
	}
	return nil
}

// messengerURL builds the method URL. Bale follows the Telegram layout
// (/bot<token>/<method>); Eitaayar puts the bare token in the path.
func messengerURL(kind string, c ChannelConfig, method string) string {
	base := strings.TrimRight(c.APIBase, "/")
	if base == "" {
		base = messengerBase[kind]
	}
	if kind == ChannelBale {
		return base + "/bot" + c.BotToken + "/" + method
	}
	return base + "/" + c.BotToken + "/" + method
}

func messengerDo(ctx context.Context, req *http.Request) error {
	resp, err := messengerClient.Do(req.WithContext(ctx))
	if err != nil {
		// The URL carries the bot token; never surface it in errors.
		if ue, ok := err.(*url.Error); ok {
			return fmt.Errorf("messenger: %v", ue.Err)
		}
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var out struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	_ = json.Unmarshal(body, &out)
	if resp.StatusCode/100 != 2 || !out.OK {
		msg := out.Description
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("messenger answered: %s", msg)
	}
	return nil
}

// sendMessenger posts a text message and then each file as a document.
func sendMessenger(ctx context.Context, kind string, c ChannelConfig, text string, files []Attachment) error {
	form := url.Values{"chat_id": {c.ChatID}, "text": {text}}
	req, err := http.NewRequest(http.MethodPost, messengerURL(kind, c, "sendMessage"), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := messengerDo(ctx, req); err != nil {
		return err
	}
	method, field := "sendDocument", "document"
	if kind == ChannelEitaa {
		method, field = "sendFile", "file"
	}
	for _, f := range files {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("chat_id", c.ChatID)
		_ = mw.WriteField("caption", f.Name)
		part, err := mw.CreateFormFile(field, f.Name)
		if err != nil {
			return err
		}
		_, _ = part.Write(f.Data)
		_ = mw.Close()
		req, err := http.NewRequest(http.MethodPost, messengerURL(kind, c, method), &buf)
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", mw.FormDataContentType())
		if err := messengerDo(ctx, req); err != nil {
			return fmt.Errorf("%s: %w", f.Name, err)
		}
	}
	return nil
}
