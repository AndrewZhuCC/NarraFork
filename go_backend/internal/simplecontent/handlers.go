package simplecontent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/config"
	"narrafork-go/internal/httpx"
)

const (
	maxSkillMarkdownBytes      = 256 * 1024
	maxSkillCompanionFileBytes = 512 * 1024
	maxGlobalPromptBytes       = 512 * 1024
)

type Handler struct {
	Store *config.Store
	DB    *sql.DB
}

func NewHandler(store ...*config.Store) *Handler {
	h := &Handler{}
	if len(store) > 0 {
		h.Store = store[0]
	}
	return h
}

func NewHandlerWithDB(store *config.Store, sqlDB *sql.DB) *Handler {
	return &Handler{Store: store, DB: sqlDB}
}

func writeSimpleContentFeatureDisabled(w http.ResponseWriter, feature, reason string) {
	httpx.WriteFeatureDisabled(w, feature, reason)
}

func writeProjectRoutinesDisabled(w http.ResponseWriter) {
	writeSimpleContentFeatureDisabled(w, "project.routines", "Project routine overrides require project database access in go_backend")
}

func writeProjectSkillsDisabled(w http.ResponseWriter) {
	writeSimpleContentFeatureDisabled(w, "project.skills", "Project skills require project database access in go_backend")
}

func (h *Handler) RegisterLearning(r chi.Router) {
	r.Get("/", httpx.Handler(h.learningIndex))
	r.Get("/search", httpx.Handler(h.learningSearch))
	r.Get("/{id}", httpx.Handler(h.learningDoc))
}

func (h *Handler) learningIndex(w http.ResponseWriter, r *http.Request) error {
	locale := learningLocale(r)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"categories": learningCategories(locale), "docs": learningSummaries(locale)})
	return nil
}

func (h *Handler) learningSearch(w http.ResponseWriter, r *http.Request) error {
	locale := learningLocale(r)
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	results := []map[string]any{}
	for _, doc := range learningSummaries(locale) {
		if query == "" || strings.Contains(strings.ToLower(learningSearchText(doc)), query) {
			results = append(results, doc)
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"results": results})
	return nil
}

func (h *Handler) learningDoc(w http.ResponseWriter, r *http.Request) error {
	locale := learningLocale(r)
	id := chi.URLParam(r, "id")
	for _, entry := range learningDocs {
		if entry.ID == id {
			httpx.WriteJSON(w, http.StatusOK, entry.Detail(locale))
			return nil
		}
	}
	return httpx.NotFound("Learning document")
}

type learningEntry struct {
	ID        string
	Category  string
	Tags      []string
	TitleEn   string
	TitleZh   string
	SummaryEn string
	SummaryZh string
}

var learningDocs = []learningEntry{
	{"overview", "start", []string{"intro", "concepts", "workflow"}, "NarraFork in one page", "一页理解 NarraFork", "Core concepts and the shortest path to productive use.", "核心概念与最短可用路径。"},
	{"narrators", "agent", []string{"narrator", "chat", "agent", "background"}, "Narrators and work sessions", "叙述者与工作会话", "Use narrators as AI work sessions for planning, coding, reviewing, and background tasks.", "用叙述者作为 AI 工作会话来规划、编码、评审与后台执行。"},
	{"subagents-and-plan-mode", "agent", []string{"subagent", "plan", "review", "parallel"}, "Subagents and plan mode", "子代理与计划模式", "Delegate focused work to subagents and use plan mode for larger changes.", "把聚焦任务交给子代理，并用计划模式处理较大改动。"},
	{"permissions-and-safety", "agent", []string{"permissions", "safety", "tools", "approval"}, "Permissions and safety", "权限与安全", "Understand permission modes, tool approvals, and safe autonomous work.", "理解权限模式、工具审批与安全自动化。"},
	{"routines", "automation", []string{"routines", "automation", "prompts", "permissions"}, "Routines", "例程", "Enable reusable built-in routines for commands, skills, and optional tools.", "启用可复用的内置例程，包括命令、技能和可选工具。"},
	{"skills", "automation", []string{"skills", "slash-command", "knowledge"}, "Skills", "技能", "Package reusable instructions and knowledge as skills for narrators.", "把可复用指令和知识封装为叙述者技能。"},
	{"mcp-and-hooks", "automation", []string{"mcp", "hooks", "integrations", "tools"}, "MCP and hooks", "MCP 与钩子", "Connect external tools and automate project workflows.", "连接外部工具并自动化项目工作流。"},
	{"terminal-and-containers", "runtime", []string{"terminal", "container", "podman", "runtime"}, "Terminals and containers", "终端与容器", "Use terminals and optional containers for runtime work.", "使用终端和可选容器执行运行时工作。"},
	{"files-snapshots-and-sharing", "runtime", []string{"files", "snapshot", "upload", "share", "storage"}, "Files, snapshots, and sharing", "文件、快照与分享", "Review file changes, snapshots, uploads, and shared artifacts.", "审阅文件改动、快照、上传内容和分享产物。"},
	{"search-and-navigation", "start", []string{"search", "navigation", "recent-tabs"}, "Search and navigation", "搜索与导航", "Find projects, chapters, narrators, and messages quickly.", "快速查找项目、章节、叙述者和消息。"},
	{"reviews-and-quality", "agent", []string{"review", "quality", "merge", "testing"}, "Reviews and quality", "评审与质量", "Use review chapters, tests, and validation before merging.", "合并前使用评审章节、测试和验证。"},
	{"providers-models-and-quotas", "admin", []string{"providers", "models", "anthropic", "openai", "codex", "kiro", "nkp", "quota"}, "Providers, models, and quotas", "提供商、模型与配额", "Configure AI providers, model lists, quotas, and visibility.", "配置 AI 提供商、模型列表、配额和可见性。"},
	{"notifications-and-gateway", "admin", []string{"notifications", "sound", "gateway", "webhook"}, "Notifications and gateway", "通知与网关", "Configure notification channels and gateway integrations.", "配置通知渠道和网关集成。"},
	{"settings-admin-and-updates", "admin", []string{"settings", "admin", "users", "updates", "storage", "profile"}, "Settings, admin, and updates", "设置、管理与更新", "Manage users, server settings, storage, and update information.", "管理用户、服务器设置、存储和更新信息。"},
}

func learningLocale(r *http.Request) string {
	if r.URL.Query().Get("lang") == "zh-CN" {
		return "zh-CN"
	}
	return "en"
}

func learningCategories(locale string) []map[string]any {
	if locale == "zh-CN" {
		return []map[string]any{
			{"id": "start", "label": "从这里开始", "description": "核心概念与最短可用路径。"},
			{"id": "agent", "label": "AI 协作", "description": "叙述者、子代理、权限、工具与安全自动化。"},
			{"id": "automation", "label": "自动化", "description": "例程、技能、钩子、MCP 与可复用工作流。"},
			{"id": "runtime", "label": "运行资源", "description": "终端、容器、文件、快照、分享与储存空间。"},
			{"id": "admin", "label": "运维与设置", "description": "提供商、模型、通知、更新、用户与系统设置。"},
		}
	}
	return []map[string]any{
		{"id": "start", "label": "Start here", "description": "Core concepts and the shortest path to productive use."},
		{"id": "agent", "label": "AI collaboration", "description": "Narrators, subagents, permissions, tools, and safe autonomous work."},
		{"id": "automation", "label": "Automation", "description": "Routines, skills, hooks, MCP, and reusable workflows."},
		{"id": "runtime", "label": "Runtime", "description": "Terminals, containers, files, snapshots, sharing, and storage."},
		{"id": "admin", "label": "Operations", "description": "Providers, models, notifications, updates, users, and system settings."},
	}
}

func learningSummaries(locale string) []map[string]any {
	out := make([]map[string]any, 0, len(learningDocs))
	for _, entry := range learningDocs {
		out = append(out, entry.Summary(locale))
	}
	return out
}

func (e learningEntry) title(locale string) string {
	if locale == "zh-CN" {
		return e.TitleZh
	}
	return e.TitleEn
}

func (e learningEntry) summary(locale string) string {
	if locale == "zh-CN" {
		return e.SummaryZh
	}
	return e.SummaryEn
}

func (e learningEntry) Summary(locale string) map[string]any {
	return map[string]any{"id": e.ID, "category": e.Category, "tags": e.Tags, "title": e.title(locale), "summary": e.summary(locale), "actions": learningActions(locale)}
}

func (e learningEntry) Detail(locale string) map[string]any {
	doc := e.Summary(locale)
	if locale == "zh-CN" {
		doc["sections"] = []map[string]any{{"title": "如何使用", "body": e.summary(locale) + " 在相关页面完成配置后，回到叙述者或项目页面验证实际效果。"}}
		doc["workflow"] = []string{"阅读本页要点。", "打开对应功能页完成配置或操作。", "回到项目/叙述者页面验证结果。"}
		doc["bestPractices"] = []string{"优先小步启用并验证。", "涉及自动化或权限时保留可审计记录。"}
		doc["pitfalls"] = []string{"不要在未验证的情况下把自动化结果视为最终结果。"}
		doc["agentHints"] = []string{"给叙述者明确目标、约束和完成标准。"}
	} else {
		doc["sections"] = []map[string]any{{"title": "How to use this", "body": e.summary(locale) + " After configuring the relevant page, return to a narrator or project view and validate the outcome."}}
		doc["workflow"] = []string{"Read the key points.", "Open the related feature page and configure or run it.", "Validate the result from a project or narrator page."}
		doc["bestPractices"] = []string{"Enable features in small steps and validate them.", "Keep automation and permission changes auditable."}
		doc["pitfalls"] = []string{"Do not treat automation output as final without validation."}
		doc["agentHints"] = []string{"Give narrators a concrete objective, constraints, and a definition of done."}
	}
	doc["actions"] = learningActions(locale)
	return doc
}

func learningActions(locale string) []map[string]any {
	if locale == "zh-CN" {
		return []map[string]any{{"label": "打开设置", "description": "配置提供商、模型、外观与运行资源。", "href": "/settings"}, {"label": "新建叙述者", "description": "启动不绑定具体叙事线的 AI 会话。", "href": "/narrators"}}
	}
	return []map[string]any{{"label": "Open settings", "description": "Configure providers, models, appearance, and runtime.", "href": "/settings"}, {"label": "New narrator", "description": "Start an AI session not bound to a specific project.", "href": "/narrators"}}
}

func learningSearchText(doc map[string]any) string {
	parts := []string{stringValue(doc["id"]), stringValue(doc["category"]), stringValue(doc["title"]), stringValue(doc["summary"])}
	if tags, ok := doc["tags"].([]string); ok {
		parts = append(parts, tags...)
	}
	return strings.Join(parts, "\n")
}

func LearningGuideOutput(mode, query, idValue, lang string) string {
	locale := "en"
	if lang == "zh-CN" || strings.HasPrefix(strings.ToLower(lang), "zh") {
		locale = "zh-CN"
	}
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = "search"
	}
	switch mode {
	case "list":
		return renderLearningSummaries(learningSummaries(locale), locale)
	case "get":
		idValue = strings.TrimSpace(idValue)
		if idValue == "" {
			if locale == "zh-CN" {
				return "请提供要查阅的学习文档 id。"
			}
			return "Please provide the learning document id to read."
		}
		for _, entry := range learningDocs {
			if entry.ID == idValue {
				return renderLearningDetail(entry.Detail(locale), locale)
			}
		}
		if locale == "zh-CN" {
			return "未找到学习文档：" + idValue
		}
		return "Learning document not found: " + idValue
	case "search":
		query = strings.ToLower(strings.TrimSpace(query))
		if query == "" {
			if locale == "zh-CN" {
				return "请提供搜索关键词，或使用 mode=list 查看全部文档。"
			}
			return "Please provide a search query, or use mode=list to see all documents."
		}
		results := []map[string]any{}
		for _, doc := range learningSummaries(locale) {
			if strings.Contains(strings.ToLower(learningSearchText(doc)), query) {
				results = append(results, doc)
			}
		}
		return renderLearningSummaries(results, locale)
	default:
		return "mode must be one of: list, search, get"
	}
}

func renderLearningSummaries(docs []map[string]any, locale string) string {
	if len(docs) == 0 {
		if locale == "zh-CN" {
			return "未找到匹配的学习文档。"
		}
		return "No matching learning documents found."
	}
	lines := []string{}
	for _, doc := range docs {
		line := "- " + stringValue(doc["title"]) + " (" + stringValue(doc["id"]) + ")\n  " + stringValue(doc["summary"])
		tags := learningStringSlice(doc["tags"])
		if len(tags) > 0 {
			label := "Tags"
			if locale == "zh-CN" {
				label = "标签"
			}
			line += "\n  " + label + ": " + strings.Join(tags, ", ")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func renderLearningDetail(doc map[string]any, locale string) string {
	lines := []string{"# " + stringValue(doc["title"]), "", stringValue(doc["summary"]), ""}
	if sections, ok := doc["sections"].([]map[string]any); ok {
		for _, section := range sections {
			lines = append(lines, "## "+stringValue(section["title"]), stringValue(section["body"]), "")
		}
	}
	appendList := func(key, titleEn, titleZh string) {
		items := learningStringSlice(doc[key])
		if len(items) == 0 {
			return
		}
		title := titleEn
		if locale == "zh-CN" {
			title = titleZh
		}
		lines = append(lines, "## "+title)
		for i, item := range items {
			if key == "workflow" {
				lines = append(lines, fmt.Sprintf("%d. %s", i+1, item))
			} else {
				lines = append(lines, "- "+item)
			}
		}
		lines = append(lines, "")
	}
	appendList("workflow", "Recommended workflow", "推荐使用流程")
	appendList("bestPractices", "Best practices", "最佳实践")
	appendList("pitfalls", "Common pitfalls", "常见坑")
	appendList("agentHints", "Agent hints", "Agent 查阅提示")
	lines = append(lines, "Document ID: "+stringValue(doc["id"]))
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func learningStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := []string{}
		for _, item := range typed {
			if s := strings.TrimSpace(stringValue(item)); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func (h *Handler) RegisterRoutines(r chi.Router) {
	r.Get("/", httpx.Handler(h.listRoutines))
	r.Post("/{id}/toggle", httpx.Handler(h.toggleRoutine))
	r.Get("/project/{projectId}", httpx.Handler(h.listProjectRoutines))
	r.Post("/project/{projectId}/{id}/toggle", httpx.Handler(h.toggleProjectRoutine))
	r.Get("/global-prompt", httpx.Handler(h.getGlobalPrompt))
	r.Put("/global-prompt", httpx.Handler(h.putGlobalPrompt))
}

var builtinRoutines = []map[string]any{
	{"id": "terminal", "type": "tool", "category": "tools", "name": "Terminal", "descriptionEn": "Interactive terminal — read buffer, send input, list terminals", "descriptionZh": "交互式终端 — 读取缓冲区、发送输入、列出终端"},
	{"id": "share_file", "type": "tool", "category": "tools", "name": "ShareFile", "descriptionEn": "Share files/directories — generate temporary download links", "descriptionZh": "分享文件/目录 — 生成临时下载链接"},
	{"id": "recall", "type": "tool", "category": "tools", "name": "Recall", "descriptionEn": "Search and browse all narrator conversations", "descriptionZh": "搜索和浏览所有叙述者对话"},
	{"id": "browser", "type": "tool", "category": "tools", "name": "Browser", "descriptionEn": "Interactive browser — navigate, click, fill forms, take screenshots", "descriptionZh": "交互式浏览器 — 导航、点击、填写表单、截图"},
	{"id": "fork_narrator", "type": "tool", "category": "tools", "name": "ForkNarrator", "descriptionEn": "Fork the current narrator session into a new chapter", "descriptionZh": "将当前叙述者会话分叉到新章节"},
	{"id": "narrafork_admin", "type": "tool", "category": "tools", "name": "NarraForkAdmin", "descriptionEn": "Admin settings management — get and modify all NarraFork settings", "descriptionZh": "管理员设置管理 — 获取和修改所有 NarraFork 设置"},
}

func (h *Handler) listRoutines(w http.ResponseWriter, r *http.Request) error {
	routines := make([]map[string]any, 0, len(builtinRoutines))
	for _, routine := range builtinRoutines {
		item := cloneMap(routine)
		item["enabled"] = h.globalRoutineEnabled(stringValue(item["id"]))
		routines = append(routines, item)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"routines": routines})
	return nil
}

func (h *Handler) toggleRoutine(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	routineID := safeName(chi.URLParam(r, "id"))
	if !routineExists(routineID) {
		return httpx.NotFound("Routine")
	}
	enabled := h.globalRoutineList("enabledRoutines")
	disabled := h.globalRoutineList("disabledRoutines")
	if body.Enabled {
		enabled = appendMissing(enabled, routineID)
		disabled = removeString(disabled, routineID)
	} else {
		enabled = removeString(enabled, routineID)
		disabled = appendMissing(disabled, routineID)
	}
	if h.Store != nil {
		_, err := h.Store.Patch(map[string]any{"routines": map[string]any{"enabledRoutines": anyStrings(enabled), "disabledRoutines": anyStrings(disabled)}})
		if err != nil {
			return err
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) listProjectRoutines(w http.ResponseWriter, r *http.Request) error {
	if h.DB == nil {
		writeProjectRoutinesDisabled(w)
		return nil
	}
	projectConf, err := h.projectRoutineConfig(r.Context(), chi.URLParam(r, "projectId"))
	if err != nil {
		return err
	}
	projectEnabled := mapFromList(stringList(projectConf["enabledRoutines"]))
	projectDisabled := mapFromList(stringList(projectConf["disabledRoutines"]))
	routines := make([]map[string]any, 0, len(builtinRoutines))
	for _, routine := range builtinRoutines {
		item := cloneMap(routine)
		id := stringValue(item["id"])
		globalEnabled := h.globalRoutineEnabled(id)
		enabled := globalEnabled
		override := "global"
		if projectEnabled[id] {
			enabled = true
			override = "enabled"
		}
		if projectDisabled[id] {
			enabled = false
			override = "disabled"
		}
		item["enabled"] = enabled
		item["globalEnabled"] = globalEnabled
		item["override"] = override
		routines = append(routines, item)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"routines": routines})
	return nil
}

func (h *Handler) toggleProjectRoutine(w http.ResponseWriter, r *http.Request) error {
	if h.DB == nil {
		writeProjectRoutinesDisabled(w)
		return nil
	}
	var body struct {
		Action string `json:"action"`
	}
	if err := httpx.DecodeLooseJSON(r, &body); err != nil {
		return err
	}
	if body.Action != "enable" && body.Action != "disable" && body.Action != "reset" {
		return httpx.BadRequest("action must be 'enable', 'disable', or 'reset'")
	}
	routineID := safeName(chi.URLParam(r, "id"))
	if !routineExists(routineID) {
		return httpx.NotFound("Routine")
	}
	projectID := chi.URLParam(r, "projectId")
	chapterSettings, routines, err := h.projectSettingsAndRoutines(r.Context(), projectID)
	if err != nil {
		return err
	}
	enabled := stringList(routines["enabledRoutines"])
	disabled := stringList(routines["disabledRoutines"])
	switch body.Action {
	case "enable":
		enabled = appendMissing(enabled, routineID)
		disabled = removeString(disabled, routineID)
	case "disable":
		enabled = removeString(enabled, routineID)
		disabled = appendMissing(disabled, routineID)
	case "reset":
		enabled = removeString(enabled, routineID)
		disabled = removeString(disabled, routineID)
	}
	routines["enabledRoutines"] = anyStrings(enabled)
	routines["disabledRoutines"] = anyStrings(disabled)
	chapterSettings["routines"] = routines
	data, err := json.Marshal(chapterSettings)
	if err != nil {
		return err
	}
	if h.DB == nil {
		return httpx.NotImplemented("project routines")
	}
	res, err := h.DB.ExecContext(r.Context(), `UPDATE projects SET chapter_settings = ?, updated_at = datetime('now') WHERE id = ?`, string(data), projectID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.NotFound("Project")
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (h *Handler) globalRoutineList(key string) []string {
	if h.Store == nil {
		return []string{}
	}
	raw := h.Store.Raw()
	if routines, ok := raw["routines"].(map[string]any); ok {
		return stringList(routines[key])
	}
	return []string{}
}

func (h *Handler) globalRoutineEnabled(routineID string) bool {
	enabled := mapFromList(h.globalRoutineList("enabledRoutines"))
	disabled := mapFromList(h.globalRoutineList("disabledRoutines"))
	if routineDefaultEnabled(routineID) {
		return !disabled[routineID]
	}
	return enabled[routineID]
}

func routineDefaultEnabled(routineID string) bool {
	for _, routine := range builtinRoutines {
		if stringValue(routine["id"]) == routineID {
			return boolValue(routine["defaultEnabled"])
		}
	}
	return false
}

func (h *Handler) projectRoutineConfig(ctx context.Context, projectID string) (map[string]any, error) {
	_, routines, err := h.projectSettingsAndRoutines(ctx, projectID)
	return routines, err
}

func (h *Handler) projectSettingsAndRoutines(ctx context.Context, projectID string) (map[string]any, map[string]any, error) {
	if h.DB == nil {
		return nil, nil, httpx.NotImplemented("project routines")
	}
	var raw sql.NullString
	err := h.DB.QueryRowContext(ctx, `SELECT chapter_settings FROM projects WHERE id = ?`, projectID).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil, httpx.NotFound("Project")
	}
	if err != nil {
		return nil, nil, err
	}
	settings := map[string]any{}
	if raw.Valid && strings.TrimSpace(raw.String) != "" {
		_ = json.Unmarshal([]byte(raw.String), &settings)
	}
	routines, _ := settings["routines"].(map[string]any)
	if routines == nil {
		routines = map[string]any{}
	}
	return settings, routines, nil
}

func routineExists(id string) bool {
	for _, routine := range builtinRoutines {
		if stringValue(routine["id"]) == id {
			return true
		}
	}
	return false
}

func mapFromList(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		out[value] = true
	}
	return out
}

func appendMissing(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func removeString(values []string, value string) []string {
	out := values[:0]
	for _, existing := range values {
		if existing != value {
			out = append(out, existing)
		}
	}
	return out
}

func anyStrings(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func boolValue(value any) bool {
	if b, ok := value.(bool); ok {
		return b
	}
	return false
}

func stringList(value any) []string {
	out := []string{}
	switch raw := value.(type) {
	case []string:
		for _, item := range raw {
			if value := strings.TrimSpace(item); value != "" {
				out = append(out, value)
			}
		}
	case []any:
		for _, item := range raw {
			if value := strings.TrimSpace(stringValue(item)); value != "" {
				out = append(out, value)
			}
		}
	}
	return out
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func (h *Handler) RegisterSkills(r chi.Router) {
	r.Get("/global", httpx.Handler(h.listGlobalSkills))
	r.Get("/global/{name}", httpx.Handler(h.getGlobalSkill))
	r.Post("/global", httpx.Handler(h.createGlobalSkill))
	r.Put("/global/{name}", httpx.Handler(h.updateGlobalSkill))
	r.Delete("/global/{name}", httpx.Handler(h.deleteGlobalSkill))
	r.Post("/global/{name}/toggle", httpx.Handler(h.toggleGlobalSkill))
	r.Get("/", httpx.Handler(h.listProjectSkills))
	r.Get("/{name}/files/*", httpx.Handler(h.getSkillFile))
	r.Get("/{name}", httpx.Handler(h.getProjectSkill))
}

func (h *Handler) listGlobalSkills(w http.ResponseWriter, r *http.Request) error {
	httpx.WriteJSON(w, http.StatusOK, skillSummaries(loadGlobalSkills()))
	return nil
}

func (h *Handler) listProjectSkills(w http.ResponseWriter, r *http.Request) error {
	if h.DB == nil {
		writeProjectSkillsDisabled(w)
		return nil
	}
	gitPath, err := h.projectGitPath(r.Context(), r.URL.Query().Get("projectId"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, skillSummaries(loadProjectSkills(gitPath)))
	return nil
}

func (h *Handler) getGlobalSkill(w http.ResponseWriter, r *http.Request) error {
	item, err := findSkill(loadGlobalSkills(), chi.URLParam(r, "name"))
	if err != nil {
		return httpx.NotFound("Skill")
	}
	httpx.WriteJSON(w, http.StatusOK, item)
	return nil
}

func (h *Handler) getProjectSkill(w http.ResponseWriter, r *http.Request) error {
	if h.DB == nil {
		writeProjectSkillsDisabled(w)
		return nil
	}
	gitPath, err := h.projectGitPath(r.Context(), r.URL.Query().Get("projectId"))
	if err != nil {
		return err
	}
	item, err := findSkill(loadAllSkills(gitPath), chi.URLParam(r, "name"))
	if err != nil {
		return httpx.NotFound("Skill")
	}
	httpx.WriteJSON(w, http.StatusOK, item)
	return nil
}

func (h *Handler) createGlobalSkill(w http.ResponseWriter, r *http.Request) error {
	return h.writeSkill(w, r, "")
}
func (h *Handler) updateGlobalSkill(w http.ResponseWriter, r *http.Request) error {
	return h.writeSkill(w, r, chi.URLParam(r, "name"))
}

func (h *Handler) writeSkill(w http.ResponseWriter, r *http.Request, currentName string) error {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Content     string `json:"content"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	name := safeName(body.Name)
	description := strings.TrimSpace(body.Description)
	if name == "" {
		return httpx.BadRequest("name is required")
	}
	if description == "" {
		return httpx.BadRequest("description is required")
	}
	if currentName != "" && currentName != name {
		if existing, err := findSkill(loadGlobalSkills(), currentName); err == nil {
			_ = os.RemoveAll(filepath.Dir(stringValue(existing["location"])))
		} else {
			_ = os.RemoveAll(filepath.Join(skillsRoot(), safeName(currentName)))
		}
	}
	dir := filepath.Join(skillsRoot(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	content := buildSkillMarkdown(name, description, body.Content)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		return err
	}
	item, _ := findSkill(loadGlobalSkills(), name)
	httpx.WriteJSON(w, http.StatusOK, item)
	return nil
}

func (h *Handler) deleteGlobalSkill(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	if existing, err := findSkill(loadGlobalSkills(), name); err == nil {
		_ = os.RemoveAll(filepath.Dir(stringValue(existing["location"])))
	} else {
		_ = os.RemoveAll(filepath.Join(skillsRoot(), safeName(name)))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}
func (h *Handler) toggleGlobalSkill(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := httpx.DecodeOptionalLooseJSON(r, &body); err != nil {
		return err
	}
	item, err := findSkill(loadGlobalSkills(), name)
	if err != nil {
		return httpx.NotFound("Skill")
	}
	location := stringValue(item["location"])
	dir := filepath.Dir(location)
	enabledPath := filepath.Join(dir, "SKILL.md")
	disabledPath := filepath.Join(dir, "SKILL.md.disabled")
	if body.Enabled && boolValue(item["disabled"]) {
		_ = os.Rename(disabledPath, enabledPath)
		_ = os.Remove(filepath.Join(dir, ".disabled"))
	} else if !body.Enabled && !boolValue(item["disabled"]) {
		_ = os.Rename(enabledPath, disabledPath)
	}
	updated, _ := findSkill(loadGlobalSkills(), name)
	httpx.WriteJSON(w, http.StatusOK, updated)
	return nil
}
func (h *Handler) getSkillFile(w http.ResponseWriter, r *http.Request) error {
	if h.DB == nil {
		writeProjectSkillsDisabled(w)
		return nil
	}
	gitPath, err := h.projectGitPath(r.Context(), r.URL.Query().Get("projectId"))
	if err != nil {
		return err
	}
	item, err := findSkill(loadAllSkills(gitPath), chi.URLParam(r, "name"))
	if err != nil {
		return httpx.NotFound("Skill")
	}
	content, err := readSkillCompanionFile(stringValue(item["location"]), chi.URLParam(r, "*"))
	if err != nil {
		return httpx.NotFound("Skill file")
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"content": content})
	return nil
}

func (h *Handler) projectGitPath(ctx context.Context, projectID string) (string, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return "", httpx.BadRequest("projectId is required")
	}
	if h.DB == nil {
		return "", httpx.NotImplemented("project skills")
	}
	var gitPath sql.NullString
	if err := h.DB.QueryRowContext(ctx, `SELECT git_path FROM projects WHERE id = ?`, projectID).Scan(&gitPath); err != nil {
		if err == sql.ErrNoRows {
			return "", httpx.NotFound("Project")
		}
		return "", err
	}
	if !gitPath.Valid || strings.TrimSpace(gitPath.String) == "" {
		return "", httpx.BadRequest("Project has no git path")
	}
	return gitPath.String, nil
}

func loadGlobalSkills() []map[string]any {
	home, _ := os.UserHomeDir()
	return mergeSkillsByName(scanSkillDirs(home))
}

func loadProjectSkills(gitPath string) []map[string]any {
	return mergeSkillsByName(scanSkillDirs(gitPath))
}

func SkillToolOutput(skillName, skillArgs, gitPath string) string {
	skillName = strings.TrimSpace(skillName)
	if skillName == "" {
		return "No skill name provided. Please specify a skill name."
	}
	skills := []map[string]any{}
	available := []string{}
	for _, skill := range loadAllSkills(gitPath) {
		if boolValue(skill["disabled"]) {
			continue
		}
		skills = append(skills, skill)
		available = append(available, stringValue(skill["name"]))
	}
	found, err := findSkill(skills, skillName)
	if err != nil {
		if len(available) == 0 {
			return "Skill \"" + skillName + "\" not found. Available skills: (none)"
		}
		return "Skill \"" + skillName + "\" not found. Available skills: " + strings.Join(available, ", ")
	}
	skillDir := filepath.Dir(stringValue(found["location"]))
	lines := []string{"<skill_content name=\"" + escapeSkillXML(stringValue(found["name"])) + "\">", "# Skill: " + stringValue(found["name"]), ""}
	if content := strings.TrimSpace(stringValue(found["content"])); content != "" {
		lines = append(lines, content, "")
	}
	lines = append(lines, "Base directory for this skill: "+skillDir)
	if strings.TrimSpace(skillArgs) != "" {
		lines = append(lines, "", "<skill_args>"+escapeSkillXML(skillArgs)+"</skill_args>")
	}
	if files := learningStringSlice(found["files"]); len(files) > 0 {
		lines = append(lines, "", "<skill_files>")
		for _, file := range files {
			lines = append(lines, "<file>"+escapeSkillXML(filepath.Join(skillDir, file))+"</file>")
		}
		lines = append(lines, "</skill_files>")
	}
	lines = append(lines, "</skill_content>")
	return strings.Join(lines, "\n")
}

func escapeSkillXML(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	value = strings.ReplaceAll(value, "\"", "&quot;")
	return value
}

func loadAllSkills(gitPath string) []map[string]any {
	merged := map[string]map[string]any{}
	order := []string{}
	sources := scanSkillDirs(homeDir())
	if strings.TrimSpace(gitPath) != "" {
		sources = append(sources, scanSkillDirs(gitPath)...)
	}
	for _, skill := range sources {
		name := stringValue(skill["name"])
		if name == "" {
			continue
		}
		if _, exists := merged[name]; !exists {
			order = append(order, name)
		}
		merged[name] = skill
	}
	items := make([]map[string]any, 0, len(order))
	for _, name := range order {
		items = append(items, merged[name])
	}
	return items
}

func homeDir() string {
	home, _ := os.UserHomeDir()
	return home
}

func mergeSkillsByName(skills []map[string]any) []map[string]any {
	merged := map[string]map[string]any{}
	order := []string{}
	for _, skill := range skills {
		name := stringValue(skill["name"])
		if name == "" {
			continue
		}
		if _, exists := merged[name]; !exists {
			order = append(order, name)
		}
		merged[name] = skill
	}
	items := make([]map[string]any, 0, len(order))
	for _, name := range order {
		items = append(items, merged[name])
	}
	sort.Slice(items, func(i, j int) bool { return stringValue(items[i]["name"]) < stringValue(items[j]["name"]) })
	return items
}

func skillSummaries(skills []map[string]any) []map[string]any {
	items := []map[string]any{}
	for _, skill := range skills {
		items = append(items, map[string]any{"name": skill["name"], "description": skill["description"], "location": skill["location"], "files": skill["files"], "disabled": boolValue(skill["disabled"])})
	}
	return items
}

func SlashSkillSummaries(gitPath string) []map[string]any {
	items := []map[string]any{}
	for _, skill := range loadAllSkills(gitPath) {
		if boolValue(skill["disabled"]) {
			continue
		}
		source := "global"
		location := stringValue(skill["location"])
		if strings.TrimSpace(gitPath) != "" && strings.HasPrefix(location, filepath.Clean(gitPath)+string(os.PathSeparator)) {
			source = "project"
		}
		items = append(items, map[string]any{"name": skill["name"], "description": skill["description"], "source": source})
	}
	return items
}

func findSkill(skills []map[string]any, name string) (map[string]any, error) {
	name = strings.TrimSpace(name)
	for _, skill := range skills {
		if stringValue(skill["name"]) == name || safeName(stringValue(skill["name"])) == safeName(name) {
			return skill, nil
		}
	}
	return nil, os.ErrNotExist
}

func scanSkillDirs(basePath string) []map[string]any {
	items := []map[string]any{}
	for _, root := range []string{filepath.Join(basePath, ".narrafork", "skills"), filepath.Join(basePath, ".narrafork", "skill"), filepath.Join(basePath, ".claude", "skills"), filepath.Join(basePath, ".agents", "skills")} {
		walkForSkills(root, 0, &items)
	}
	return items
}

func walkForSkills(root string, depth int, items *[]map[string]any) {
	if depth > 5 {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "node_modules" || entry.Name() == ".git" || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		if skill, err := loadSkillFile(filepath.Join(dir, "SKILL.md"), dir, false); err == nil {
			*items = append(*items, skill)
			continue
		}
		if skill, err := loadSkillFile(filepath.Join(dir, "SKILL.md.disabled"), dir, true); err == nil {
			*items = append(*items, skill)
			continue
		}
		walkForSkills(dir, depth+1, items)
	}
}

func loadSkillFile(path string, dir string, disabled bool) (map[string]any, error) {
	raw, truncated, err := readTextFileLimited(path, maxSkillMarkdownBytes)
	if err != nil {
		return nil, err
	}
	name, description, content := parseSkillMarkdown(raw, filepath.Base(dir))
	if name == "" {
		name = filepath.Base(dir)
	}
	if description == "" {
		description = firstParagraph(content)
	}
	return map[string]any{"name": name, "description": description, "location": path, "content": strings.TrimSpace(content), "files": collectSkillFiles(dir, "", 0, []string{}), "disabled": disabled || fileExists(filepath.Join(dir, ".disabled")), "truncated": truncated}, nil
}

func parseSkillMarkdown(raw string, fallbackName string) (string, string, string) {
	trimmed := strings.TrimPrefix(raw, "\ufeff")
	if !strings.HasPrefix(trimmed, "---") {
		return fallbackName, firstParagraph(trimmed), trimmed
	}
	lines := strings.Split(trimmed, "\n")
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return fallbackName, firstParagraph(trimmed), trimmed
	}
	name := fallbackName
	description := ""
	for _, line := range lines[1:end] {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.TrimSpace(key) {
		case "name":
			name = value
		case "description":
			description = value
		}
	}
	content := strings.Join(lines[end+1:], "\n")
	return strings.TrimSpace(name), strings.TrimSpace(description), strings.TrimSpace(content)
}

func collectSkillFiles(dir, prefix string, depth int, files []string) []string {
	if depth > 2 || len(files) >= 20 {
		return files
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return files
	}
	for _, entry := range entries {
		if len(files) >= 20 {
			break
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(dir, name)
		rel := filepath.ToSlash(filepath.Join(prefix, name))
		if entry.IsDir() {
			files = collectSkillFiles(full, rel, depth+1, files)
			continue
		}
		if name == "SKILL.md" || name == "SKILL.md.disabled" {
			continue
		}
		files = append(files, rel)
	}
	return files
}

func readSkillCompanionFile(location, filePath string) (string, error) {
	dir := filepath.Dir(location)
	clean := filepath.Clean(strings.TrimSpace(filePath))
	if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", os.ErrNotExist
	}
	resolved := filepath.Join(dir, clean)
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	absFile, err := filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	if absFile != absDir && !strings.HasPrefix(absFile, absDir+string(os.PathSeparator)) {
		return "", os.ErrPermission
	}
	text, _, err := readTextFileLimited(absFile, maxSkillCompanionFileBytes)
	return text, err
}

func readTextFileLimited(path string, limit int) (string, bool, error) {
	if limit <= 0 {
		limit = maxSkillCompanionFileBytes
	}
	file, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return "", false, err
	}
	if len(data) > limit {
		return string(data[:limit]) + "\n[truncated]", true, nil
	}
	return string(data), false, nil
}

func buildSkillMarkdown(name, description, content string) string {
	return "---\nname: \"" + strings.ReplaceAll(name, "\"", "\\\"") + "\"\ndescription: \"" + strings.ReplaceAll(description, "\"", "\\\"") + "\"\n---\n\n" + strings.TrimSpace(content) + "\n"
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func skillsRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".narrafork", "skills")
}
func safeName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "..", "")
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, "\\", "-")
	return name
}
func firstParagraph(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.Trim(strings.TrimSpace(line), "# ")
		if line != "" {
			return line
		}
	}
	return ""
}

func (h *Handler) getGlobalPrompt(w http.ResponseWriter, r *http.Request) error {
	candidates := globalPromptCandidates()
	resultCandidates := []map[string]any{}
	var content any
	var filePath any
	contentTruncated := false
	for _, candidate := range candidates {
		_, err := os.Stat(candidate)
		exists := err == nil
		resultCandidates = append(resultCandidates, map[string]any{"path": candidate, "exists": exists})
		if exists && filePath == nil {
			text, truncated, err := readTextFileLimited(candidate, maxGlobalPromptBytes)
			if err == nil {
				content = text
				contentTruncated = truncated
				filePath = candidate
			}
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"content": content, "filePath": filePath, "contentTruncated": contentTruncated, "candidates": resultCandidates})
	return nil
}

func (h *Handler) putGlobalPrompt(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Content  string `json:"content"`
		FilePath string `json:"filePath"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	candidates := globalPromptCandidates()
	target := body.FilePath
	if target == "" {
		target = candidates[0]
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	allowed := false
	for _, candidate := range candidates {
		if abs == candidate {
			allowed = true
			break
		}
	}
	if !allowed {
		return httpx.BadRequest("filePath must be one of the known global prompt paths")
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(abs, []byte(body.Content), 0o644); err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "filePath": abs})
	return nil
}

func globalPromptCandidates() []string {
	home, _ := os.UserHomeDir()
	items := []string{filepath.Join(home, ".agents", "AGENT.md"), filepath.Join(home, ".claude", "CLAUDE.md")}
	out := make([]string, 0, len(items))
	for _, item := range items {
		abs, err := filepath.Abs(item)
		if err == nil {
			out = append(out, abs)
		}
	}
	return out
}

func notImplemented(feature string) httpx.HandlerFunc {
	return func(http.ResponseWriter, *http.Request) error { return httpx.NotImplemented(feature) }
}
