package gatewayruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type telegramAdapter struct {
	cfg       platformConfig
	client    *http.Client
	onMessage func(context.Context, InboundMessage) error
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	offset    int64
	allowed   map[string]bool
}

func newTelegramAdapter(cfg platformConfig, onMessage func(context.Context, InboundMessage) error) *telegramAdapter {
	allowed := map[string]bool{}
	for _, user := range cfg.AllowedUsers {
		if strings.TrimSpace(user) != "" {
			allowed[strings.TrimSpace(user)] = true
		}
	}
	return &telegramAdapter{cfg: cfg, client: &http.Client{Timeout: 40 * time.Second}, onMessage: onMessage, allowed: allowed}
}

func (a *telegramAdapter) Platform() string { return "telegram" }

func (a *telegramAdapter) Start(ctx context.Context) error {
	if strings.TrimSpace(a.cfg.Token) == "" {
		return fmt.Errorf("telegram token is required")
	}
	runCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	a.wg.Add(1)
	go a.poll(runCtx)
	return nil
}

func (a *telegramAdapter) Stop() error {
	if a.cancel != nil {
		a.cancel()
	}
	a.wg.Wait()
	return nil
}

func (a *telegramAdapter) Send(ctx context.Context, chatID, text string) error {
	if len(text) > 4096 {
		text = text[:4090] + "…"
	}
	payload := map[string]any{"chat_id": chatID, "text": text, "disable_web_page_preview": true}
	return a.post(ctx, "sendMessage", payload, nil)
}

func (a *telegramAdapter) poll(ctx context.Context) {
	defer a.wg.Done()
	backoff := time.Second
	for ctx.Err() == nil {
		updates, err := a.getUpdates(ctx)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		for _, update := range updates {
			if update.UpdateID >= a.offset {
				a.offset = update.UpdateID + 1
			}
			msg := update.Message
			if strings.TrimSpace(msg.Text) == "" {
				continue
			}
			userID := strconv.FormatInt(msg.From.ID, 10)
			if len(a.allowed) > 0 && !a.allowed[userID] {
				continue
			}
			inbound := InboundMessage{Platform: "telegram", ChatID: strconv.FormatInt(msg.Chat.ID, 10), UserID: userID, Username: firstNonEmptyString(msg.From.Username, msg.From.FirstName, userID), Text: msg.Text}
			if a.onMessage != nil {
				_ = a.onMessage(ctx, inbound)
			}
		}
	}
}

func (a *telegramAdapter) getUpdates(ctx context.Context) ([]telegramUpdate, error) {
	payload := map[string]any{"timeout": 30, "allowed_updates": []string{"message"}}
	if a.offset > 0 {
		payload["offset"] = a.offset
	}
	var resp telegramUpdatesResponse
	if err := a.post(ctx, "getUpdates", payload, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("telegram getUpdates returned ok=false")
	}
	return resp.Result, nil
}

func (a *telegramAdapter) post(ctx context.Context, method string, payload map[string]any, out any) error {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.apiURL(method), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram %s failed: %s", method, resp.Status)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (a *telegramAdapter) apiURL(method string) string {
	base := strings.TrimRight(firstNonEmptyString(a.cfg.APIBase, "https://api.telegram.org"), "/")
	return base + "/bot" + a.cfg.Token + "/" + method
}

type telegramUpdatesResponse struct {
	OK     bool             `json:"ok"`
	Result []telegramUpdate `json:"result"`
}

type telegramUpdate struct {
	UpdateID int64           `json:"update_id"`
	Message  telegramMessage `json:"message"`
}

type telegramMessage struct {
	Text string       `json:"text"`
	Chat telegramChat `json:"chat"`
	From telegramUser `json:"from"`
}

type telegramChat struct {
	ID int64 `json:"id"`
}

type telegramUser struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}
