package notifications

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type HubHookRegistrar interface {
	AddNarratorBroadcastHook(func(narratorID string, payload any, data []byte))
}

type PostFunc func(ctx context.Context, endpoint string, payload []byte, provider string) error

type Service struct {
	DB   *sql.DB
	Post PostFunc
}

type narratorInfo struct {
	ID        string
	Title     string
	ChapterID string
	Chapter   string
}

type preferenceRow struct {
	UserID          string
	RecentTabs      string
	NotifyOnDone    bool
	NotifyOnWaiting bool
	DingtalkEnabled bool
	DingtalkWebhook string
	DingtalkSecret  string
	FeishuEnabled   bool
	FeishuWebhook   string
	FeishuSecret    string
}

type recentTab struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	NarratorID string `json:"narratorId"`
}

func New(sqlDB *sql.DB) *Service {
	return &Service{DB: sqlDB, Post: postWebhook}
}

func (s *Service) AttachToHub(hub HubHookRegistrar) {
	if s == nil || hub == nil {
		return
	}
	hub.AddNarratorBroadcastHook(func(narratorID string, payload any, _ []byte) {
		payloadMap, ok := payload.(map[string]any)
		if !ok || stringFromAny(payloadMap["type"]) != "status_change" {
			return
		}
		status := stringFromAny(payloadMap["status"])
		substatus := stringSliceFromAny(payloadMap["substatus"])
		go func() {
			if err := s.NotifyStatusChanged(context.Background(), narratorID, status, substatus); err != nil {
				slog.Debug("notification status handler failed", "narratorId", narratorID, "error", err)
			}
		}()
	})
}

func (s *Service) NotifyStatusChanged(ctx context.Context, narratorID, status string, substatus []string) error {
	if s == nil || s.DB == nil || strings.TrimSpace(narratorID) == "" {
		return nil
	}
	if containsString(substatus, "silent_notification") {
		return nil
	}
	isDone := status == "idle" && containsString(substatus, "unread")
	isWaiting := status == "waiting"
	if !isDone && !isWaiting {
		return nil
	}
	info, ok := s.loadNarrator(ctx, narratorID)
	if !ok {
		return nil
	}
	prefs, err := s.loadPreferences(ctx)
	if err != nil {
		return err
	}
	displayStatus := status
	if isDone {
		displayStatus = "done"
	}
	chapterLine := ""
	if strings.TrimSpace(info.Chapter) != "" {
		chapterLine = "\n\n> Chapter: " + info.Chapter
	}
	text := fmt.Sprintf("**%s** status: **%s**%s", firstNonEmpty(info.Title, info.ID), displayStatus, chapterLine)
	for _, pref := range prefs {
		if !preferenceMatchesNarrator(pref.RecentTabs, narratorID) {
			continue
		}
		if isDone && !pref.NotifyOnDone {
			continue
		}
		if isWaiting && !pref.NotifyOnWaiting {
			continue
		}
		if pref.DingtalkEnabled && strings.TrimSpace(pref.DingtalkWebhook) != "" {
			if err := sendDingtalk(ctx, s.post, pref.DingtalkWebhook, pref.DingtalkSecret, "NarraFork: "+firstNonEmpty(info.Title, info.ID), text); err != nil {
				slog.Debug("dingtalk notification failed", "narratorId", narratorID, "userId", pref.UserID, "error", err)
			}
		}
		if pref.FeishuEnabled && strings.TrimSpace(pref.FeishuWebhook) != "" {
			if err := sendFeishu(ctx, s.post, pref.FeishuWebhook, pref.FeishuSecret, "NarraFork Notification", text); err != nil {
				slog.Debug("feishu notification failed", "narratorId", narratorID, "userId", pref.UserID, "error", err)
			}
		}
	}
	return nil
}

func (s *Service) post(ctx context.Context, endpoint string, payload []byte, provider string) error {
	post := s.Post
	if post == nil {
		post = postWebhook
	}
	return post(ctx, endpoint, payload, provider)
}

func (s *Service) loadNarrator(ctx context.Context, narratorID string) (narratorInfo, bool) {
	var info narratorInfo
	var title, chapterID sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT id, title, chapter_id FROM narrators WHERE id = ?`, narratorID).Scan(&info.ID, &title, &chapterID)
	if err != nil {
		return narratorInfo{}, false
	}
	if title.Valid {
		info.Title = title.String
	}
	if chapterID.Valid {
		info.ChapterID = chapterID.String
		var chapterTitle sql.NullString
		if err := s.DB.QueryRowContext(ctx, `SELECT title FROM chapters WHERE id = ?`, chapterID.String).Scan(&chapterTitle); err == nil && chapterTitle.Valid {
			info.Chapter = chapterTitle.String
		}
	}
	return info, true
}

func (s *Service) loadPreferences(ctx context.Context) ([]preferenceRow, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT user_id, COALESCE(recent_tabs, '[]'), COALESCE(notify_on_done, 0), COALESCE(notify_on_waiting, 0), COALESCE(notify_dingtalk_enabled, 0), COALESCE(notify_dingtalk_webhook, ''), COALESCE(notify_dingtalk_secret, ''), COALESCE(notify_feishu_enabled, 0), COALESCE(notify_feishu_webhook, ''), COALESCE(notify_feishu_secret, '') FROM user_preferences`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	prefs := []preferenceRow{}
	for rows.Next() {
		var pref preferenceRow
		var done, waiting, dingEnabled, feishuEnabled int
		if err := rows.Scan(&pref.UserID, &pref.RecentTabs, &done, &waiting, &dingEnabled, &pref.DingtalkWebhook, &pref.DingtalkSecret, &feishuEnabled, &pref.FeishuWebhook, &pref.FeishuSecret); err != nil {
			continue
		}
		pref.NotifyOnDone = done != 0
		pref.NotifyOnWaiting = waiting != 0
		pref.DingtalkEnabled = dingEnabled != 0
		pref.FeishuEnabled = feishuEnabled != 0
		prefs = append(prefs, pref)
	}
	return prefs, rows.Err()
}

func preferenceMatchesNarrator(rawTabs, narratorID string) bool {
	var tabs []recentTab
	if err := json.Unmarshal([]byte(rawTabs), &tabs); err != nil {
		return false
	}
	for _, tab := range tabs {
		if (tab.Type == "narrator" && tab.ID == narratorID) || tab.NarratorID == narratorID {
			return true
		}
	}
	return false
}

func sendDingtalk(ctx context.Context, post PostFunc, webhook, secret, title, text string) error {
	endpoint := webhook
	if strings.TrimSpace(secret) != "" {
		timestamp := fmt.Sprintf("%d", time.Now().UnixMilli())
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(timestamp + "\n" + secret))
		sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))
		separator := "&"
		if !strings.Contains(endpoint, "?") {
			separator = "?"
		}
		endpoint = endpoint + separator + "timestamp=" + url.QueryEscape(timestamp) + "&sign=" + url.QueryEscape(sign)
	}
	payload, _ := json.Marshal(map[string]any{"msgtype": "markdown", "markdown": map[string]any{"title": title, "text": text}})
	return post(ctx, endpoint, payload, "DingTalk")
}

func sendFeishu(ctx context.Context, post PostFunc, webhook, secret, title, content string) error {
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	body := map[string]any{
		"msg_type": "interactive",
		"card": map[string]any{
			"header":   map[string]any{"title": map[string]any{"tag": "plain_text", "content": title}},
			"elements": []map[string]any{{"tag": "markdown", "content": content}},
		},
	}
	if strings.TrimSpace(secret) != "" {
		mac := hmac.New(sha256.New, []byte(timestamp+"\n"+secret))
		_, _ = mac.Write(nil)
		body["timestamp"] = timestamp
		body["sign"] = base64.StdEncoding.EncodeToString(mac.Sum(nil))
	}
	payload, _ := json.Marshal(body)
	return post(ctx, webhook, payload, "Feishu")
}

func postWebhook(ctx context.Context, endpoint string, payload []byte, provider string) error {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return fmt.Errorf("%s webhook failed: %d %s", provider, res.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func stringFromAny(value any) string {
	if value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	return fmt.Sprint(value)
}

func stringSliceFromAny(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		items := []string{}
		for _, item := range typed {
			if s, ok := item.(string); ok {
				items = append(items, s)
			}
		}
		return items
	default:
		return nil
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
