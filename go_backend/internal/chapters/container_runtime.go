package chapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"narrafork-go/internal/db"
	"narrafork-go/internal/httpx"
	"narrafork-go/internal/id"
)

const (
	containerCommandOutputLimit = 64 * 1024
	composeFileReadLimit        = 512 * 1024
)

var chapterContainerLocks = struct {
	sync.Mutex
	locks map[string]*sync.Mutex
}{locks: map[string]*sync.Mutex{}}

func withChapterContainerLock(chapterID string, fn func() error) error {
	key := strings.TrimSpace(chapterID)
	if key == "" {
		key = "__unknown__"
	}
	chapterContainerLocks.Lock()
	lock := chapterContainerLocks.locks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		chapterContainerLocks.locks[key] = lock
	}
	chapterContainerLocks.Unlock()

	lock.Lock()
	defer lock.Unlock()
	return fn()
}

type containerPortConfig struct {
	ContainerPort int    `json:"containerPort"`
	ServiceName   string `json:"serviceName"`
}

type chapterContainerConfig struct {
	ComposeFile string                `json:"composeFile"`
	Services    []string              `json:"services"`
	Ports       []containerPortConfig `json:"ports"`
	Env         map[string]string     `json:"env"`
}

type proxyRuntimeInfo struct {
	Domain         string
	Port           int
	ChapterShortID string
}

type limitedCombinedWriter struct {
	mu        sync.Mutex
	buf       strings.Builder
	limit     int
	truncated bool
}

func newLimitedCombinedWriter(limit int) *limitedCombinedWriter {
	if limit <= 0 {
		limit = containerCommandOutputLimit
	}
	return &limitedCombinedWriter{limit: limit}
}

func (w *limitedCombinedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf.Len() < w.limit {
		remaining := w.limit - w.buf.Len()
		if len(p) <= remaining {
			_, _ = w.buf.Write(p)
		} else {
			_, _ = w.buf.Write(p[:remaining])
			w.truncated = true
		}
	} else {
		w.truncated = true
	}
	return len(p), nil
}

func (w *limitedCombinedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := w.buf.String()
	if w.truncated {
		out += "\n[truncated]"
	}
	return out
}

func containerConfigFrom(value any) chapterContainerConfig {
	var cfg chapterContainerConfig
	if value == nil {
		return cfg
	}
	data, err := json.Marshal(value)
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(data, &cfg)
	return cfg
}

func containerSettingsInt(settingsRaw map[string]any, key string, fallback int) int {
	containers, _ := settingsRaw["containers"].(map[string]any)
	switch value := containers[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		parsed, err := strconv.Atoi(value.String())
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func containerProxyEnabled(settingsRaw map[string]any) bool {
	containers, _ := settingsRaw["containers"].(map[string]any)
	proxy, _ := containers["proxy"].(map[string]any)
	enabled, _ := proxy["enabled"].(bool)
	return enabled
}

func containerProxyPort(settingsRaw map[string]any) int {
	containers, _ := settingsRaw["containers"].(map[string]any)
	proxy, _ := containers["proxy"].(map[string]any)
	switch value := proxy["port"].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		parsed, err := strconv.Atoi(value.String())
		if err == nil {
			return parsed
		}
	}
	return 7780
}

func (h *Handler) containerSettingsRaw() map[string]any {
	if h == nil || h.Service == nil {
		return map[string]any{}
	}
	return h.Service.containerSettingsRaw()
}

func (s *Service) containerSettingsRaw() map[string]any {
	if s == nil || s.Settings == nil {
		return map[string]any{}
	}
	return s.Settings.Raw()
}

func (s *Service) allocateContainerPorts(ctx context.Context, chapterID string, ports []containerPortConfig) ([]map[string]any, error) {
	if len(ports) == 0 {
		return nil, nil
	}
	settingsRaw := map[string]any{}
	if s != nil && s.Settings != nil {
		settingsRaw = s.Settings.Raw()
	}
	start := containerSettingsInt(settingsRaw, "portRangeStart", 10000)
	end := containerSettingsInt(settingsRaw, "portRangeEnd", 20000)
	if start <= 0 || end < start || end > 65535 {
		return nil, httpx.BadRequest("invalid container port range")
	}
	if err := s.releaseContainerPorts(ctx, chapterID); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT port FROM port_allocations`)
	if err != nil {
		return nil, err
	}
	used := map[int]bool{}
	for rows.Next() {
		var port int
		if err := rows.Scan(&port); err != nil {
			rows.Close()
			return nil, err
		}
		used[port] = true
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	now := db.NowISO()
	allocated := []map[string]any{}
	for _, requested := range ports {
		serviceName := strings.TrimSpace(requested.ServiceName)
		if serviceName == "" || requested.ContainerPort <= 0 || requested.ContainerPort > 65535 {
			_ = s.releaseContainerPorts(ctx, chapterID)
			return nil, httpx.BadRequest("invalid container port mapping")
		}
		allocatedPort := 0
		for port := start; port <= end; port++ {
			if used[port] {
				continue
			}
			_, err := s.DB.ExecContext(ctx, `INSERT INTO port_allocations (port, chapter_id, service_name, allocated_at) VALUES (?, ?, ?, ?)`, port, chapterID, serviceName, now)
			if err != nil {
				used[port] = true
				continue
			}
			allocatedPort = port
			used[port] = true
			break
		}
		if allocatedPort == 0 {
			_ = s.releaseContainerPorts(ctx, chapterID)
			return nil, fmt.Errorf("port pool exhausted: no available ports in range %d-%d", start, end)
		}
		allocated = append(allocated, map[string]any{"hostPort": allocatedPort, "containerPort": requested.ContainerPort, "serviceName": serviceName})
	}
	return allocated, nil
}

func (s *Service) releaseContainerPorts(ctx context.Context, chapterID string) error {
	if s == nil || s.DB == nil {
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM port_allocations WHERE chapter_id = ?`, chapterID)
	return err
}

func (s *Service) clearContainerInstances(ctx context.Context, chapterID string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM container_instances WHERE chapter_id = ?`, chapterID)
	return err
}

func (s *Service) removeContainerRecordsAndPorts(ctx context.Context, chapterID string) error {
	if err := s.clearContainerInstances(ctx, chapterID); err != nil {
		return err
	}
	return s.releaseContainerPorts(ctx, chapterID)
}

func (s *Service) removeChapterContainersIfPossible(ctx context.Context, ch Chapter) {
	if ch.WorktreePath != nil && strings.TrimSpace(*ch.WorktreePath) != "" {
		cfg := containerConfigFrom(ch.ContainerConfig)
		if composeFile := resolveComposeFilePath(*ch.WorktreePath, cfg); composeFile != "" && commandExists("podman") {
			if cmd := composeCommandForFile(ctx, composeFile); len(cmd) > 0 {
				args := append(cmd[1:], "down", "--remove-orphans")
				_, _ = runLimited(ctx, *ch.WorktreePath, cmd[0], args...)
			}
		}
	}
	_ = s.removeContainerRecordsAndPorts(ctx, ch.ID)
}

func commandExists(name string) bool {
	_, err := os.Stat(name)
	if err == nil && strings.ContainsRune(name, filepath.Separator) {
		return true
	}
	_, err = filepath.Abs(name)
	_ = err
	return lookPath(name) != ""
}

var lookPath = func(name string) string {
	found, lookErr := executableLookPath(name)
	if lookErr != nil {
		return ""
	}
	return found
}

var executableLookPath = func(name string) (string, error) {
	return osLookPath(name)
}

var osLookPath = func(name string) (string, error) {
	return exec.LookPath(name)
}

func resolveComposeFilePath(worktreePath string, cfg chapterContainerConfig) string {
	if strings.TrimSpace(cfg.ComposeFile) != "" {
		candidate := filepath.Join(worktreePath, cfg.ComposeFile)
		absBase, errBase := filepath.Abs(worktreePath)
		absCandidate, errCandidate := filepath.Abs(candidate)
		if errBase != nil || errCandidate != nil || !isInsidePath(absBase, absCandidate) {
			return ""
		}
		if info, err := os.Stat(absCandidate); err == nil && !info.IsDir() {
			return absCandidate
		}
		return ""
	}
	for _, name := range []string{"compose.yml", "compose.yaml", "docker-compose.yml", "docker-compose.yaml"} {
		candidate := filepath.Join(worktreePath, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func composeCommandForFile(ctx context.Context, composeFile string) []string {
	return composeCommandForFiles(ctx, composeFile)
}

func composeCommandForFiles(ctx context.Context, composeFiles ...string) []string {
	probe := composeVersion(ctx)
	if len(probe.command) == 0 {
		return nil
	}
	cmd := append([]string{}, probe.command...)
	for _, composeFile := range composeFiles {
		if strings.TrimSpace(composeFile) == "" {
			continue
		}
		cmd = append(cmd, "-f", composeFile)
	}
	return cmd
}

func buildComposeEnv(chapterID string, cfg chapterContainerConfig, mappings []map[string]any, proxyInfo *proxyRuntimeInfo) map[string]string {
	env := map[string]string{
		"NARRAFORK_CHAPTER_ID":        chapterID,
		"NARRAFORK_VOLUME_PREFIX":     "nf_" + shortPrefix(chapterID, 12),
		"COMPOSE_PROJECT_NAME":        "narrafork_" + sanitizeComposeName(shortPrefix(chapterID, 12)),
		"PODMAN_COMPOSE_PROJECT_NAME": "narrafork_" + sanitizeComposeName(shortPrefix(chapterID, 12)),
	}
	if proxyInfo != nil {
		env["NARRAFORK_PROXY"] = "1"
		env["NARRAFORK_PROXY_DOMAIN"] = proxyInfo.Domain
		for _, port := range cfg.Ports {
			label := generateProxyLabel(proxyInfo.ChapterShortID, port.ServiceName, port.ContainerPort)
			env[fmt.Sprintf("NARRAFORK_PROXY_URL_%d", port.ContainerPort)] = buildProxyURL(label, proxyInfo.Domain, proxyInfo.Port)
		}
	} else {
		for _, mapping := range mappings {
			containerPort := intValue(mapping["containerPort"])
			hostPort := intValue(mapping["hostPort"])
			if containerPort > 0 && hostPort > 0 {
				env[fmt.Sprintf("PORT_%d", containerPort)] = strconv.Itoa(hostPort)
			}
		}
	}
	for key, value := range cfg.Env {
		env[key] = value
	}
	return env
}

func shortPrefix(value string, n int) string {
	if len(value) <= n {
		return value
	}
	return value[:n]
}

func sanitizeComposeName(value string) string {
	value = strings.ToLower(value)
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "chapter"
	}
	return b.String()
}

func generateProxyLabel(chapterShortID, serviceName string, containerPort int) string {
	serviceName = strings.ToLower(strings.TrimSpace(serviceName))
	var b strings.Builder
	lastDash := false
	for _, r := range serviceName {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	sanitized := strings.Trim(b.String(), "-")
	if sanitized == "" {
		sanitized = "service"
	}
	return fmt.Sprintf("%s-%s-%d", chapterShortID, sanitized, containerPort)
}

func buildProxyURL(label, domain string, port int) string {
	if port == 80 || port == 0 {
		return fmt.Sprintf("http://%s.%s", label, domain)
	}
	return fmt.Sprintf("http://%s.%s:%d", label, domain, port)
}

func (h *Handler) projectProxyDomain(ctx context.Context, projectID string) string {
	if h == nil || h.Service == nil {
		return ""
	}
	return h.Service.projectProxyDomain(ctx, projectID)
}

func (s *Service) projectProxyDomain(ctx context.Context, projectID string) string {
	if s == nil || s.DB == nil {
		return ""
	}
	var domain sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT proxy_domain FROM projects WHERE id = ?`, projectID).Scan(&domain)
	if err != nil || !domain.Valid {
		return ""
	}
	return strings.TrimSpace(domain.String)
}

func (h *Handler) proxyInfoForChapter(ctx context.Context, ch Chapter) (*proxyRuntimeInfo, bool, string) {
	if h == nil || h.Service == nil {
		return nil, false, ""
	}
	return h.Service.proxyInfoForChapter(ctx, ch)
}

func (s *Service) proxyInfoForChapter(ctx context.Context, ch Chapter) (*proxyRuntimeInfo, bool, string) {
	settingsRaw := s.containerSettingsRaw()
	if !containerProxyEnabled(settingsRaw) {
		return nil, false, ""
	}
	domain := s.projectProxyDomain(ctx, ch.ProjectID)
	if domain == "" {
		return nil, false, ""
	}
	if !isPastaBackend(ctx) {
		return nil, true, "Proxy mode requires Podman with pasta/passt rootless networking; disable container proxy mode or update Podman/passt."
	}
	return &proxyRuntimeInfo{Domain: domain, Port: containerProxyPort(settingsRaw), ChapterShortID: shortPrefix(ch.ID, 8)}, false, ""
}

func isPastaBackend(ctx context.Context) bool {
	out, err := runLimited(ctx, "", "podman", "info", "--format", "{{.Host.RootlessNetworkCmd}}")
	if err != nil {
		return false
	}
	backend := strings.ToLower(strings.Trim(strings.TrimSpace(out), "'\""))
	return backend == "pasta" || backend == "passt"
}

func writeProxyOverride(worktreePath string, services []map[string]any) (string, error) {
	if len(services) == 0 {
		return "", nil
	}
	lines := []string{"services:"}
	for _, service := range services {
		name := strings.TrimSpace(stringValue(service["name"]))
		if name == "" {
			continue
		}
		lines = append(lines, "  "+name+":", "    ports: !reset []")
	}
	if len(lines) == 1 {
		return "", nil
	}
	path := filepath.Join(worktreePath, ".narrafork-proxy-override.yml")
	return path, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

func parseComposeServicesFromFile(path string) []map[string]any {
	content, _, err := readComposeFileLimited(path)
	if err != nil {
		return []map[string]any{}
	}
	return parseComposeServicesFromData(content)
}

func readComposeFileLimited(path string) (string, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, composeFileReadLimit+1))
	if err != nil {
		return "", false, err
	}
	if len(data) > composeFileReadLimit {
		return string(data[:composeFileReadLimit]) + "\n[truncated]", true, nil
	}
	return string(data), false, nil
}

func parseComposeServicesFromData(data string) []map[string]any {
	lines := strings.Split(data, "\n")
	services := []map[string]any{}
	inServices := false
	current := -1
	section := ""
	for _, line := range lines {
		trimRight := strings.TrimRight(line, " \t\r")
		trimmed := strings.TrimSpace(trimRight)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(trimRight) - len(strings.TrimLeft(trimRight, " \t"))
		if indent == 0 {
			if strings.TrimSuffix(trimmed, " ") == "services:" {
				inServices = true
				current = -1
				section = ""
				continue
			}
			if inServices {
				break
			}
		}
		if !inServices {
			continue
		}
		if indent == 2 && strings.HasSuffix(trimmed, ":") && !strings.HasPrefix(trimmed, "-") {
			name := strings.TrimSuffix(trimmed, ":")
			services = append(services, map[string]any{"name": name, "ports": []any{}, "environment": map[string]any{}})
			current = len(services) - 1
			section = ""
			continue
		}
		if current < 0 {
			continue
		}
		if indent == 4 && !strings.HasPrefix(trimmed, "-") {
			key, value, _ := strings.Cut(trimmed, ":")
			key = strings.TrimSpace(key)
			value = strings.Trim(strings.TrimSpace(value), "'\"")
			switch key {
			case "ports", "environment":
				section = key
			case "image":
				services[current]["image"] = value
				section = ""
			default:
				section = ""
			}
			continue
		}
		if section == "ports" && strings.HasPrefix(trimmed, "-") {
			portText := strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "-")), "'\"")
			if host, container := parseComposePort(portText); host > 0 || container > 0 {
				ports, _ := services[current]["ports"].([]any)
				ports = append(ports, map[string]any{"host": nullableInt(host), "container": nullableInt(container)})
				services[current]["ports"] = ports
			}
			continue
		}
		if section == "environment" {
			env, _ := services[current]["environment"].(map[string]any)
			if strings.HasPrefix(trimmed, "-") {
				kv := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
				if key, value, ok := strings.Cut(kv, "="); ok {
					env[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), "'\"")
				}
			} else if key, value, ok := strings.Cut(trimmed, ":"); ok {
				env[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), "'\"")
			}
		}
	}
	return services
}

func parseComposePort(value string) (int, int) {
	value = strings.SplitN(value, "/", 2)[0]
	parts := strings.Split(value, ":")
	if len(parts) == 1 {
		container, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
		return 0, container
	}
	hostText := parts[len(parts)-2]
	containerText := parts[len(parts)-1]
	host, _ := strconv.Atoi(strings.TrimSpace(hostText))
	container, _ := strconv.Atoi(strings.TrimSpace(containerText))
	return host, container
}

func parseContainerJSONList(out string) []map[string]any {
	out = strings.TrimSpace(out)
	if out == "" {
		return nil
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(out), &items); err == nil {
		return items
	}
	var single map[string]any
	if err := json.Unmarshal([]byte(out), &single); err == nil {
		return []map[string]any{single}
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var item map[string]any
		if err := json.Unmarshal([]byte(line), &item); err == nil {
			items = append(items, item)
		}
	}
	return items
}

func containerPortsFromRaw(raw map[string]any) []int {
	values := map[int]bool{}
	for _, key := range []string{"Ports", "Publishers"} {
		collectContainerPorts(raw[key], values)
	}
	ports := make([]int, 0, len(values))
	for port := range values {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return ports
}

func collectContainerPorts(value any, values map[int]bool) {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			collectContainerPorts(item, values)
		}
	case map[string]any:
		port := intValue(firstAnyValue(typed, "container_port", "TargetPort", "ContainerPort", "target"))
		if port > 0 {
			values[port] = true
		}
	case string:
		_, container := parsePortMappingString(typed)
		if container > 0 {
			values[container] = true
		}
	}
}

func configuredPortsForService(cfg chapterContainerConfig, serviceName string) []int {
	values := map[int]bool{}
	for _, port := range cfg.Ports {
		if strings.TrimSpace(port.ServiceName) == serviceName && port.ContainerPort > 0 {
			values[port.ContainerPort] = true
		}
	}
	ports := make([]int, 0, len(values))
	for port := range values {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return ports
}

func mergePortHints(groups ...[]int) []int {
	values := map[int]bool{}
	for _, group := range groups {
		for _, port := range group {
			if port > 0 {
				values[port] = true
			}
		}
	}
	ports := make([]int, 0, len(values))
	for port := range values {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return ports
}

func (h *Handler) inspectContainerIP(ctx context.Context, containerID string) string {
	return inspectContainerIP(ctx, containerID)
}

func inspectContainerIP(ctx context.Context, containerID string) string {
	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return ""
	}
	out, err := runLimited(ctx, "", "podman", "inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}", containerID)
	if err != nil {
		return ""
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func (h *Handler) inspectContainerExposedPorts(ctx context.Context, containerID string) []int {
	return inspectContainerExposedPorts(ctx, containerID)
}

func inspectContainerExposedPorts(ctx context.Context, containerID string) []int {
	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return nil
	}
	out, err := runLimited(ctx, "", "podman", "inspect", "--format", "{{json .Config.ExposedPorts}}", containerID)
	if err != nil {
		return nil
	}
	out = strings.TrimSpace(out)
	if out == "" || out == "<nil>" || out == "null" {
		return nil
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		return nil
	}
	ports := []int{}
	for key := range parsed {
		port, _ := strconv.Atoi(strings.SplitN(key, "/", 2)[0])
		if port > 0 {
			ports = append(ports, port)
		}
	}
	sort.Ints(ports)
	return ports
}

func enrichContainerProxyURL(items []map[string]any, domain string, port int) []map[string]any {
	if domain == "" {
		return items
	}
	for _, item := range items {
		label := stringValue(item["proxyLabel"])
		if label != "" {
			item["proxyUrl"] = buildProxyURL(label, domain, port)
		}
	}
	return items
}

func (s *Service) liveComposeContainerInstances(ctx context.Context, chapterID, cwd string, composeCmd []string, env map[string]string, cfg chapterContainerConfig, proxyInfo *proxyRuntimeInfo) ([]map[string]any, error) {
	args := append(append([]string{}, composeCmd[1:]...), "ps", "--format", "json")
	out, err := runLimitedEnv(ctx, cwd, env, composeCmd[0], args...)
	if err != nil {
		return nil, err
	}
	raw := parseContainerJSONList(out)
	items := []map[string]any{}
	for _, item := range raw {
		base := normalizeContainerInstance(chapterID, item)
		serviceName := firstNonEmpty(stringValue(base["serviceName"]), "container")
		containerID := stringValue(base["containerId"])
		if proxyInfo == nil {
			items = append(items, base)
			continue
		}
		containerIP := inspectContainerIP(ctx, containerID)
		ports := mergePortHints(containerPortsFromRaw(item), configuredPortsForService(cfg, serviceName), inspectContainerExposedPorts(ctx, containerID))
		if len(ports) == 0 {
			continue
		}
		for _, containerPort := range ports {
			proxyLabel := generateProxyLabel(proxyInfo.ChapterShortID, serviceName, containerPort)
			entry := map[string]any{}
			for key, value := range base {
				entry[key] = value
			}
			entry["id"] = firstNonEmpty(containerID, serviceName) + "-" + strconv.Itoa(containerPort)
			entry["hostPort"] = nil
			entry["containerPort"] = containerPort
			entry["proxyLabel"] = proxyLabel
			entry["proxyUrl"] = buildProxyURL(proxyLabel, proxyInfo.Domain, proxyInfo.Port)
			entry["containerIp"] = nullableString(containerIP)
			items = append(items, entry)
		}
	}
	return items, nil
}

func (s *Service) persistedContainerInstances(ctx context.Context, chapterID string) ([]map[string]any, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, chapter_id, service_name, container_id, status, host_port, container_port, proxy_label, container_ip, volume_name, created_at, updated_at FROM container_instances WHERE chapter_id = ? ORDER BY service_name ASC, id ASC`, chapterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var idValue, chapterIDValue, serviceName, status, createdAt, updatedAt string
		var containerID, proxyLabel, containerIP, volumeName sql.NullString
		var hostPort, containerPort sql.NullInt64
		if err := rows.Scan(&idValue, &chapterIDValue, &serviceName, &containerID, &status, &hostPort, &containerPort, &proxyLabel, &containerIP, &volumeName, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": idValue, "chapterId": chapterIDValue, "serviceName": serviceName, "containerId": nullSQLString(containerID), "containerName": serviceName, "status": status, "hostPort": nullSQLInt(hostPort), "containerPort": nullSQLInt(containerPort), "proxyLabel": nullSQLString(proxyLabel), "containerIp": nullSQLString(containerIP), "volumeName": nullSQLString(volumeName), "createdAt": createdAt, "updatedAt": updatedAt})
	}
	return items, rows.Err()
}

func (s *Service) persistContainerInstances(ctx context.Context, chapterID string, items []map[string]any) error {
	now := db.NowISO()
	portAllocationUpsert := db.InsertOnConflictUpdateSQL(
		"port_allocations",
		[]string{"port", "chapter_id", "service_name", "allocated_at"},
		[]string{"port"},
		[]string{"chapter_id", "service_name", "allocated_at"},
	)
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM port_allocations WHERE chapter_id = ?`, chapterID); err != nil {
		return err
	}
	seen := []string{}
	for _, item := range items {
		idValue := stringValue(item["id"])
		if idValue == "" {
			continue
		}
		seen = append(seen, idValue)
		serviceName := firstNonEmpty(stringValue(item["serviceName"]), "container")
		containerID := nullableString(stringValue(item["containerId"]))
		status := normalizeContainerStatus(firstNonEmpty(stringValue(item["status"]), "stopped"))
		hostPort := nullableIntForDB(intValue(item["hostPort"]))
		containerPort := nullableIntForDB(intValue(item["containerPort"]))
		proxyLabel := nullableString(stringValue(item["proxyLabel"]))
		containerIP := nullableString(stringValue(item["containerIp"]))
		_, err := s.DB.ExecContext(ctx, `INSERT INTO container_instances (id, chapter_id, service_name, container_id, status, host_port, container_port, proxy_label, container_ip, volume_name, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?) ON CONFLICT(id) DO UPDATE SET chapter_id = excluded.chapter_id, service_name = excluded.service_name, container_id = excluded.container_id, status = excluded.status, host_port = excluded.host_port, container_port = excluded.container_port, proxy_label = excluded.proxy_label, container_ip = excluded.container_ip, updated_at = excluded.updated_at`, idValue, chapterID, serviceName, containerID, status, hostPort, containerPort, proxyLabel, containerIP, now, now)
		if err != nil {
			return err
		}
		if hostPort != nil {
			_, err = s.DB.ExecContext(ctx, portAllocationUpsert, intValue(item["hostPort"]), chapterID, serviceName, now)
			if err != nil {
				return err
			}
		}
	}
	if len(seen) == 0 {
		_, err := s.DB.ExecContext(ctx, `DELETE FROM container_instances WHERE chapter_id = ?`, chapterID)
		return err
	}
	placeholders, args := chapterInPlaceholders(seen)
	args = append([]any{chapterID}, args...)
	_, err := s.DB.ExecContext(ctx, `DELETE FROM container_instances WHERE chapter_id = ? AND id NOT IN (`+placeholders+`)`, args...)
	return err
}

func (s *Service) pauseChapterContainersLocked(ctx context.Context, ch Chapter) (bool, error) {
	if s == nil || s.DB == nil || ch.ContainerConfig == nil || ch.WorktreePath == nil || strings.TrimSpace(*ch.WorktreePath) == "" {
		return false, nil
	}
	cwd := strings.TrimSpace(*ch.WorktreePath)
	if commandVersion(ctx, "podman", "--version") == "" {
		return false, nil
	}
	cfg := containerConfigFrom(ch.ContainerConfig)
	composeFile := resolveComposeFilePath(cwd, cfg)
	if composeFile == "" {
		return false, nil
	}
	cmd := composeCommandForFile(ctx, composeFile)
	if len(cmd) == 0 {
		return false, nil
	}
	env := buildComposeEnv(ch.ID, cfg, nil, nil)
	args := append(append([]string{}, cmd[1:]...), "pause")
	out, err := runLimitedEnv(ctx, cwd, env, cmd[0], args...)
	if err != nil {
		return true, fmt.Errorf("pause containers: %w: %s", err, strings.TrimSpace(out))
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE container_instances SET status = 'paused', updated_at = ? WHERE chapter_id = ?`, db.NowISO(), ch.ID)
	return true, err
}

func (s *Service) unpauseOrStartChapterContainersLocked(ctx context.Context, chapterID string) (string, error) {
	ch, err := s.Get(ctx, chapterID)
	if err != nil {
		return "", err
	}
	if ch.ContainerConfig == nil || ch.WorktreePath == nil || strings.TrimSpace(*ch.WorktreePath) == "" {
		return "", nil
	}
	cwd := strings.TrimSpace(*ch.WorktreePath)
	if commandVersion(ctx, "podman", "--version") == "" {
		return "", nil
	}
	cfg := containerConfigFrom(ch.ContainerConfig)
	composeFile := resolveComposeFilePath(cwd, cfg)
	if composeFile == "" {
		return "", nil
	}
	cmd := composeCommandForFile(ctx, composeFile)
	if len(cmd) == 0 {
		return "", nil
	}
	env := buildComposeEnv(ch.ID, cfg, nil, nil)
	args := append(append([]string{}, cmd[1:]...), "unpause")
	out, runErr := runLimitedEnv(ctx, cwd, env, cmd[0], args...)
	if runErr == nil {
		_, err := s.DB.ExecContext(ctx, `UPDATE container_instances SET status = 'running', updated_at = ? WHERE chapter_id = ?`, db.NowISO(), ch.ID)
		return "container:resumed", err
	}
	_ = out
	if err := s.removeChapterContainersLocked(ctx, ch, false); err != nil {
		return "", err
	}
	if err := s.startChapterContainersLocked(ctx, ch); err != nil {
		return "", err
	}
	return "container:started", nil
}

func (s *Service) startChapterContainersBackground(chapterID string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		_ = withChapterContainerLock(chapterID, func() error {
			ch, err := s.Get(ctx, chapterID)
			if err != nil {
				s.broadcastContainerLifecycle(map[string]any{"type": "container:error", "chapterId": chapterID, "error": err.Error()})
				return err
			}
			s.broadcastContainerLifecycle(map[string]any{"type": "container:starting", "chapterId": chapterID})
			if err := s.startChapterContainersLocked(ctx, ch); err != nil {
				s.broadcastContainerLifecycle(map[string]any{"type": "container:error", "chapterId": chapterID, "error": err.Error()})
				return err
			}
			_ = s.handleContainerReadyNarratorIntegration(ctx, chapterID)
			s.broadcastContainerLifecycle(map[string]any{"type": "container:started", "chapterId": chapterID})
			return nil
		})
	}()
}

func (s *Service) startChapterContainersLocked(ctx context.Context, ch Chapter) error {
	if s == nil || s.DB == nil || ch.ContainerConfig == nil || ch.WorktreePath == nil || strings.TrimSpace(*ch.WorktreePath) == "" {
		return nil
	}
	cwd := strings.TrimSpace(*ch.WorktreePath)
	if commandVersion(ctx, "podman", "--version") == "" {
		return nil
	}
	cfg := containerConfigFrom(ch.ContainerConfig)
	composeFile := resolveComposeFilePath(cwd, cfg)
	if composeFile == "" {
		return nil
	}
	proxyInfo, proxyUnsupported, proxyMessage := s.proxyInfoForChapter(ctx, ch)
	if proxyUnsupported {
		return httpx.BadRequest(proxyMessage)
	}
	overrideFile := ""
	if proxyInfo != nil {
		var overrideErr error
		overrideFile, overrideErr = writeProxyOverride(cwd, parseComposeServicesFromFile(composeFile))
		if overrideErr != nil {
			return overrideErr
		}
		if overrideFile != "" {
			defer os.Remove(overrideFile)
		}
	}
	cmd := composeCommandForFiles(ctx, composeFile, overrideFile)
	if len(cmd) == 0 {
		return nil
	}
	mappings := []map[string]any{}
	if proxyInfo != nil {
		if err := s.releaseContainerPorts(ctx, ch.ID); err != nil {
			return err
		}
	} else {
		var err error
		mappings, err = s.allocateContainerPorts(ctx, ch.ID, cfg.Ports)
		if err != nil {
			return err
		}
	}
	env := buildComposeEnv(ch.ID, cfg, mappings, proxyInfo)
	fullArgs := append(append([]string{}, cmd[1:]...), "up", "-d")
	if len(cfg.Services) > 0 {
		fullArgs = append(fullArgs, cfg.Services...)
	}
	out, runErr := runLimitedEnvTimeout(ctx, cwd, env, 10*time.Minute, cmd[0], fullArgs...)
	if runErr != nil {
		if proxyInfo == nil {
			_ = s.releaseContainerPorts(ctx, ch.ID)
		}
		return fmt.Errorf("start containers: %w: %s", runErr, strings.TrimSpace(out))
	}
	items, err := s.liveComposeContainerInstances(ctx, ch.ID, cwd, cmd, env, cfg, proxyInfo)
	if err != nil && proxyInfo == nil && len(mappings) > 0 {
		nowItems := make([]map[string]any, 0, len(mappings))
		for _, mapping := range mappings {
			serviceName := stringValue(mapping["serviceName"])
			nowItems = append(nowItems, map[string]any{
				"id":            id.New(),
				"chapterId":     ch.ID,
				"serviceName":   firstNonEmpty(serviceName, "container"),
				"containerName": firstNonEmpty(serviceName, "container"),
				"status":        "running",
				"hostPort":      intValue(mapping["hostPort"]),
				"containerPort": intValue(mapping["containerPort"]),
			})
		}
		items = nowItems
		err = nil
	}
	if err != nil {
		return err
	}
	if proxyInfo != nil && len(items) == 0 {
		return httpx.BadRequest("No proxy routes resolved from running containers. Check compose service ports or image EXPOSE settings.")
	}
	return s.persistContainerInstances(ctx, ch.ID, items)
}

func (s *Service) removeChapterContainersLocked(ctx context.Context, ch Chapter, deleteVolumes bool) error {
	if s == nil || s.DB == nil {
		return nil
	}
	if ch.WorktreePath != nil && strings.TrimSpace(*ch.WorktreePath) != "" && commandVersion(ctx, "podman", "--version") != "" {
		cwd := strings.TrimSpace(*ch.WorktreePath)
		cfg := containerConfigFrom(ch.ContainerConfig)
		composeFile := resolveComposeFilePath(cwd, cfg)
		if composeFile != "" {
			cmd := composeCommandForFile(ctx, composeFile)
			if len(cmd) > 0 {
				args := append(append([]string{}, cmd[1:]...), "down", "--remove-orphans")
				if deleteVolumes {
					args = append(args, "-v")
				}
				env := buildComposeEnv(ch.ID, cfg, nil, nil)
				_, _ = runLimitedEnv(ctx, cwd, env, cmd[0], args...)
			}
		}
	}
	if err := s.removeContainerRecordsAndPorts(ctx, ch.ID); err != nil {
		if isMissingContainerRuntimeTable(err) {
			return nil
		}
		return err
	}
	return nil
}

func isMissingContainerRuntimeTable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no such table: container_instances") || strings.Contains(msg, "no such table: port_allocations")
}

type containerReadyServiceInfo struct {
	ServiceName   string `json:"serviceName"`
	URL           string `json:"url"`
	ContainerPort int    `json:"containerPort"`
}

func (s *Service) handleContainerReadyNarratorIntegration(ctx context.Context, chapterID string) error {
	if s == nil || s.DB == nil || strings.TrimSpace(chapterID) == "" {
		return nil
	}
	narratorID, enabledToolsRaw, toolsColumnAvailable, err := s.primaryNarratorForContainerReady(ctx, chapterID)
	if err != nil || narratorID == "" {
		return err
	}
	services, err := s.containerReadyAccessInfo(ctx, chapterID)
	if err != nil || len(services) == 0 {
		return err
	}
	browserAutoEnabled := false
	if s.BrowserToolAutoEnableEnabled {
		var enableErr error
		browserAutoEnabled, enableErr = s.enableBrowserToolForContainerReady(ctx, narratorID, enabledToolsRaw, toolsColumnAvailable)
		if enableErr != nil {
			return enableErr
		}
	}
	if err := s.persistContainerReadyMessage(ctx, narratorID, services, browserAutoEnabled); err != nil {
		if isMissingNarratorReadyStorage(err) {
			return nil
		}
		return err
	}
	return nil
}

func (s *Service) primaryNarratorForContainerReady(ctx context.Context, chapterID string) (string, string, bool, error) {
	var narratorID string
	var enabledTools sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT id, enabled_tools FROM narrators WHERE chapter_id = ? AND variant = 'primary' ORDER BY created_at ASC LIMIT 1`, chapterID).Scan(&narratorID, &enabledTools)
	if err == nil {
		return strings.TrimSpace(narratorID), strings.TrimSpace(sqlNullString(enabledTools)), true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", true, nil
	}
	if !isMissingColumn(err, "enabled_tools") {
		return "", "", false, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM narrators WHERE chapter_id = ? AND variant = 'primary' ORDER BY created_at ASC LIMIT 1`, chapterID).Scan(&narratorID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", false, nil
		}
		return "", "", false, err
	}
	return strings.TrimSpace(narratorID), "", false, nil
}

func (s *Service) containerReadyAccessInfo(ctx context.Context, chapterID string) ([]containerReadyServiceInfo, error) {
	var projectID string
	if err := s.DB.QueryRowContext(ctx, `SELECT project_id FROM chapters WHERE id = ?`, chapterID).Scan(&projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	settingsRaw := s.containerSettingsRaw()
	proxyEnabled := containerProxyEnabled(settingsRaw)
	proxyDomain := ""
	proxyPort := containerProxyPort(settingsRaw)
	if proxyEnabled {
		proxyDomain = s.projectProxyDomain(ctx, projectID)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT service_name, host_port, container_port, proxy_label FROM container_instances WHERE chapter_id = ? AND status = 'running' ORDER BY service_name ASC, id ASC`, chapterID)
	if err != nil {
		if isMissingContainerRuntimeTable(err) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	services := []containerReadyServiceInfo{}
	for rows.Next() {
		var serviceName string
		var hostPort, containerPort sql.NullInt64
		var proxyLabel sql.NullString
		if err := rows.Scan(&serviceName, &hostPort, &containerPort, &proxyLabel); err != nil {
			return nil, err
		}
		url := ""
		if proxyEnabled && proxyDomain != "" && proxyLabel.Valid && strings.TrimSpace(proxyLabel.String) != "" {
			url = buildProxyURL(strings.TrimSpace(proxyLabel.String), proxyDomain, proxyPort)
		} else if hostPort.Valid && hostPort.Int64 > 0 {
			url = fmt.Sprintf("http://localhost:%d", hostPort.Int64)
		}
		if url == "" {
			continue
		}
		services = append(services, containerReadyServiceInfo{ServiceName: firstNonEmpty(strings.TrimSpace(serviceName), "container"), URL: url, ContainerPort: int(containerPort.Int64)})
	}
	return services, rows.Err()
}

func (s *Service) enableBrowserToolForContainerReady(ctx context.Context, narratorID, enabledToolsRaw string, toolsColumnAvailable bool) (bool, error) {
	if !toolsColumnAvailable {
		return false, nil
	}
	tools := decodeEnabledToolNames(enabledToolsRaw)
	for _, tool := range tools {
		if tool == "Browser" {
			return false, nil
		}
	}
	tools = append(tools, "Browser")
	payload, _ := json.Marshal(tools)
	_, err := s.DB.ExecContext(ctx, `UPDATE narrators SET enabled_tools = ?, updated_at = ? WHERE id = ?`, string(payload), db.NowISO(), narratorID)
	if err != nil && isMissingColumn(err, "enabled_tools") {
		return false, nil
	}
	return err == nil, err
}

func decodeEnabledToolNames(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "null" {
		return []string{}
	}
	var decoded []string
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return []string{}
	}
	out := []string{}
	seen := map[string]bool{}
	for _, item := range decoded {
		name := strings.TrimSpace(item)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func (s *Service) persistContainerReadyMessage(ctx context.Context, narratorID string, services []containerReadyServiceInfo, browserAutoEnabled bool) error {
	text := buildContainerReadyText(services, browserAutoEnabled)
	blocks := []map[string]any{
		{"type": "text", "text": text},
		{"type": "container_ready", "services": services, "browserAutoEnabled": browserAutoEnabled},
	}
	contentJSON, _ := json.Marshal(blocks)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if err := s.persistContainerReadyMessageOnce(ctx, narratorID, string(contentJSON), text); err != nil {
			lastErr = err
			if !isRetryableContainerReadyMessageInsertError(err) {
				return err
			}
			continue
		}
		return nil
	}
	return lastErr
}

func (s *Service) persistContainerReadyMessageOnce(ctx context.Context, narratorID, contentJSON, text string) error {
	now := db.NowISO()
	messageID := id.New()
	refID := id.New()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Lock the narrator row before allocating seq so concurrent ready messages for the
	// same narrator serialize instead of racing on a stale next-sequence snapshot.
	result, err := tx.ExecContext(ctx, `UPDATE narrators SET updated_at = ? WHERE id = ?`, now, narratorID)
	if err != nil {
		return err
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr == nil && affected == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, created_at) VALUES (?, ?, 'system', ?, ?, ?)`, messageID, narratorID, contentJSON, text, now); err != nil {
		return err
	}
	seq := 1
	var lastSeq sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT seq FROM narrator_message_refs WHERE narrator_id = ? ORDER BY seq DESC LIMIT 1`, narratorID).Scan(&lastSeq); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	} else if lastSeq.Valid {
		seq = int(lastSeq.Int64) + 1
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq, is_compact) VALUES (?, ?, ?, ?, 0)`, refID, narratorID, messageID, seq); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE narrators SET message_count = (SELECT COUNT(*) FROM narrator_message_refs WHERE narrator_id = ?), message_version = COALESCE(message_version, 0) + 1, last_message_at = ?, updated_at = ? WHERE id = ?`, narratorID, now, now, narratorID); err != nil {
		return err
	}
	return tx.Commit()
}

func isRetryableContainerReadyMessageInsertError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "database is locked") || strings.Contains(msg, "database table is locked") || strings.Contains(msg, "busy") || strings.Contains(msg, "deadlock") || strings.Contains(msg, "serialization") {
		return true
	}
	if strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate") || strings.Contains(msg, "constraint") {
		return strings.Contains(msg, "narrator_message_refs") || strings.Contains(msg, "seq") || strings.Contains(msg, "narrator_id")
	}
	return false
}

func buildContainerReadyText(services []containerReadyServiceInfo, browserAutoEnabled bool) string {
	var builder strings.Builder
	builder.WriteString("Container services are now running:\n\n")
	for _, service := range services {
		builder.WriteString(fmt.Sprintf("- **%s** (port %d): %s\n", service.ServiceName, service.ContainerPort, service.URL))
	}
	if browserAutoEnabled {
		builder.WriteString("\nThe Browser tool has been automatically enabled. ")
	}
	builder.WriteString("\nYou can use the Browser tool to test these services, or access them via HTTP requests.")
	return builder.String()
}

func isMissingNarratorReadyStorage(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such table: narrator_messages") || strings.Contains(msg, "no such table: narrator_message_refs")
}

func isMissingColumn(err error, column string) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such column") && strings.Contains(msg, strings.ToLower(column))
}

func sqlNullString(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func (s *Service) broadcastContainerLifecycle(payload map[string]any) {
	if s == nil || s.BroadcastMergeProgress == nil || payload == nil {
		return
	}
	s.BroadcastMergeProgress(payload)
}

func containerRuntimeFallback(code, message string) map[string]any {
	return map[string]any{"ok": false, "supported": false, "fallback": true, "code": code, "reason": message, "error": message, "message": message}
}

func containerRuntimeFallbackWithItems(items []map[string]any, code, message string) map[string]any {
	payload := containerRuntimeFallback(code, message)
	payload["items"] = items
	return payload
}

func containerStateCounts(items []map[string]any) (int, int, int) {
	running, stopped, paused := 0, 0, 0
	for _, item := range items {
		switch normalizeContainerStatus(stringValue(item["status"])) {
		case "running":
			running++
		case "paused":
			paused++
		default:
			stopped++
		}
	}
	return running, stopped, paused
}

func localAddresses() []string {
	addrs := []string{}
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		values, _ := iface.Addrs()
		for _, value := range values {
			addrs = append(addrs, value.String())
		}
	}
	sort.Strings(addrs)
	return addrs
}
