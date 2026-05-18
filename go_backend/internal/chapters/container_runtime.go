package chapters

import (
	"context"
	"database/sql"
	"encoding/json"
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

	"narrafork-go/internal/db"
	"narrafork-go/internal/httpx"
)

const (
	containerCommandOutputLimit = 64 * 1024
	composeFileReadLimit        = 512 * 1024
)

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
	if h == nil || h.Service == nil || h.Service.Settings == nil {
		return map[string]any{}
	}
	return h.Service.Settings.Raw()
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
	var domain sql.NullString
	err := h.Service.DB.QueryRowContext(ctx, `SELECT proxy_domain FROM projects WHERE id = ?`, projectID).Scan(&domain)
	if err != nil || !domain.Valid {
		return ""
	}
	return strings.TrimSpace(domain.String)
}

func (h *Handler) proxyInfoForChapter(ctx context.Context, ch Chapter) (*proxyRuntimeInfo, bool, string) {
	settingsRaw := h.containerSettingsRaw()
	if !containerProxyEnabled(settingsRaw) {
		return nil, false, ""
	}
	domain := h.projectProxyDomain(ctx, ch.ProjectID)
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

func containerRuntimeFallback(code, message string) map[string]any {
	return map[string]any{"ok": false, "supported": false, "fallback": true, "code": code, "error": message, "message": message}
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
