package terminalruntime

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

type Broadcaster func(terminalID string, payload any)

type Manager struct {
	DB        *sql.DB
	Broadcast Broadcaster

	mu       sync.Mutex
	sessions map[string]*Session

	socketsDir              string
	dtachAvailable          func() bool
	socketAlive             func(ctx context.Context, terminalID, socketPath string) bool
	processSnapshotProvider func(ctx context.Context) processSnapshot
}

type Session struct {
	id          string
	cmd         *exec.Cmd
	file        *os.File
	done        chan struct{}
	mu          sync.Mutex
	buf         bytes.Buffer
	cols        int
	rows        int
	useDtach    bool
	dtachSocket string
	orphan      bool
}

type terminalRecord struct {
	Cwd         sql.NullString
	DtachSocket sql.NullString
	Status      string
}

const maxSnapshotBytes = 1 << 20

func NewManager(sqlDB *sql.DB, broadcast Broadcaster) *Manager {
	return &Manager{DB: sqlDB, Broadcast: broadcast, sessions: map[string]*Session{}}
}

func (m *Manager) Ensure(ctx context.Context, terminalID string) error {
	return m.EnsureSize(ctx, terminalID, 80, 24)
}

func (m *Manager) EnsureSize(ctx context.Context, terminalID string, cols, rows int) error {
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	if err := m.ensureExisting(ctx, terminalID, cols, rows); err != errNoExistingSession {
		return err
	}

	record, cwd, err := m.terminalRecord(ctx, terminalID)
	if err != nil {
		return err
	}
	if record.Status == "exited" {
		return nil
	}

	if record.DtachSocket.Valid && strings.TrimSpace(record.DtachSocket.String) != "" {
		socketPath := strings.TrimSpace(record.DtachSocket.String)
		if m.dtachAvailableNow() && m.isDtachSocketAlive(ctx, terminalID, socketPath) {
			return m.attachDtach(ctx, terminalID, socketPath, cols, rows, nil)
		}
		m.markExited(context.Background(), terminalID, 0)
		return nil
	}

	if m.dtachAvailableNow() {
		socketPath, createErr := m.createDtachSession(ctx, terminalID, cwd)
		if createErr == nil {
			return m.attachDtach(ctx, terminalID, socketPath, cols, rows, nil)
		}
		m.emitDtachFallback(terminalID, createErr.Error())
	} else {
		m.emitDtachFallback(terminalID, "dtach is not available; using direct terminal runtime")
	}
	return m.startDirect(ctx, terminalID, cwd, cols, rows, nil)
}

var errNoExistingSession = errors.New("terminal session not found")

func (m *Manager) ensureExisting(ctx context.Context, terminalID string, cols, rows int) error {
	m.mu.Lock()
	existing := m.sessions[terminalID]
	m.mu.Unlock()
	if existing == nil {
		return errNoExistingSession
	}

	existing.mu.Lock()
	existing.cols = cols
	existing.rows = rows
	file := existing.file
	useDtach := existing.useDtach
	socketPath := existing.dtachSocket
	orphan := existing.orphan
	existing.mu.Unlock()

	if file != nil && !orphan {
		_ = pty.Setsize(file, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
		return nil
	}
	if useDtach && socketPath != "" && m.dtachAvailableNow() && m.isDtachSocketAlive(ctx, terminalID, socketPath) {
		return m.attachDtach(ctx, terminalID, socketPath, cols, rows, existing)
	}
	m.mu.Lock()
	delete(m.sessions, terminalID)
	m.mu.Unlock()
	m.markExited(context.Background(), terminalID, 0)
	return nil
}

func (m *Manager) Write(ctx context.Context, terminalID string, data string) error {
	if err := m.Ensure(ctx, terminalID); err != nil {
		return err
	}
	s := m.get(terminalID)
	if s == nil {
		return errors.New("terminal session not found")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil || s.orphan {
		return errors.New("terminal session is not attached")
	}
	_, err := s.file.Write([]byte(data))
	return err
}

func (m *Manager) Resize(ctx context.Context, terminalID string, cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	if err := m.EnsureSize(ctx, terminalID, cols, rows); err != nil {
		return err
	}
	s := m.get(terminalID)
	if s == nil {
		return errors.New("terminal session not found")
	}
	s.mu.Lock()
	s.cols = cols
	s.rows = rows
	file := s.file
	useDtach := s.useDtach
	pid := 0
	if s.cmd != nil && s.cmd.Process != nil {
		pid = s.cmd.Process.Pid
	}
	s.mu.Unlock()
	if file == nil {
		return nil
	}
	if err := pty.Setsize(file, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}); err != nil {
		return err
	}
	if useDtach && pid > 0 && runtime.GOOS != "windows" {
		_ = syscall.Kill(pid, syscall.SIGWINCH)
	}
	return nil
}

func (m *Manager) Kill(ctx context.Context, terminalID string) error {
	s := m.get(terminalID)
	if s != nil {
		s.mu.Lock()
		useDtach := s.useDtach
		socketPath := s.dtachSocket
		pid := 0
		if s.cmd != nil && s.cmd.Process != nil {
			pid = s.cmd.Process.Pid
		}
		file := s.file
		s.mu.Unlock()
		if useDtach && socketPath != "" {
			m.killDtachSession(ctx, terminalID, socketPath)
		} else if pid > 0 {
			m.killProcessTree(ctx, pid)
		}
		if file != nil {
			_ = file.Close()
		}
	}
	if s == nil && m.dtachAvailableNow() {
		if record, _, err := m.terminalRecord(ctx, terminalID); err == nil && record.DtachSocket.Valid {
			m.killDtachSession(ctx, terminalID, record.DtachSocket.String)
		}
	}
	m.mu.Lock()
	delete(m.sessions, terminalID)
	m.mu.Unlock()
	_, err := m.DB.ExecContext(ctx, `UPDATE terminals SET status = 'exited', exit_code = -1 WHERE id = ?`, terminalID)
	m.emit(terminalID, map[string]any{"type": "exit", "terminalId": terminalID, "code": -1})
	return err
}

func (m *Manager) Snapshot(ctx context.Context, terminalID string) (map[string]any, error) {
	s := m.get(terminalID)
	var status string
	var exitCode sql.NullInt64
	if m.DB != nil {
		if err := m.DB.QueryRowContext(ctx, `SELECT status, exit_code FROM terminals WHERE id = ?`, terminalID).Scan(&status, &exitCode); err != nil {
			return nil, err
		}
	}
	if status == "" {
		if s != nil {
			status = "running"
		} else {
			status = "unknown"
		}
	}
	payload := map[string]any{
		"type":          "snapshot",
		"terminalId":    terminalID,
		"status":        status,
		"data":          "",
		"cols":          80,
		"rows":          24,
		"mouseTracking": false,
		"cursorVisible": true,
	}
	if exitCode.Valid {
		payload["exitCode"] = int(exitCode.Int64)
	} else {
		payload["exitCode"] = nil
	}
	if s != nil {
		s.mu.Lock()
		payload["data"] = s.buf.String()
		if s.cols > 0 {
			payload["cols"] = s.cols
		}
		if s.rows > 0 {
			payload["rows"] = s.rows
		}
		s.mu.Unlock()
	}
	return payload, nil
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.mu.Unlock()
	for _, s := range sessions {
		s.mu.Lock()
		useDtach := s.useDtach
		socketPath := s.dtachSocket
		file := s.file
		pid := 0
		if s.cmd != nil && s.cmd.Process != nil {
			pid = s.cmd.Process.Pid
		}
		s.mu.Unlock()
		if useDtach {
			if file != nil {
				_ = file.Close()
			}
			if socketPath != "" && m.isDtachSocketAlive(context.Background(), s.id, socketPath) {
				m.markOrphan(context.Background(), s.id)
			} else {
				m.markExited(context.Background(), s.id, 0)
			}
			continue
		}
		if pid > 0 {
			m.killProcessTree(context.Background(), pid)
		}
		if file != nil {
			_ = file.Close()
		}
		m.markExited(context.Background(), s.id, -1)
		m.emit(s.id, map[string]any{"type": "exit", "terminalId": s.id, "code": -1})
	}
	m.mu.Lock()
	m.sessions = map[string]*Session{}
	m.mu.Unlock()
}

func (m *Manager) RecoverOnStartup(ctx context.Context) error {
	if m.DB == nil {
		return nil
	}
	if !m.dtachAvailableNow() {
		_, err := m.DB.ExecContext(ctx, `UPDATE terminals SET status = 'exited', exit_code = COALESCE(exit_code, 0) WHERE status IN ('running', 'orphan')`)
		return err
	}

	rows, err := m.DB.QueryContext(ctx, `SELECT id, dtach_socket FROM terminals WHERE status IN ('running', 'orphan')`)
	if err != nil {
		return err
	}
	type startupTerminal struct {
		id     string
		socket sql.NullString
	}
	terminals := []startupTerminal{}
	tracked := map[string]struct{}{}
	for rows.Next() {
		var terminalID string
		var socket sql.NullString
		if err := rows.Scan(&terminalID, &socket); err != nil {
			rows.Close()
			return err
		}
		tracked[terminalID] = struct{}{}
		terminals = append(terminals, startupTerminal{id: terminalID, socket: socket})
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, terminal := range terminals {
		if terminal.socket.Valid && strings.TrimSpace(terminal.socket.String) != "" && m.isDtachSocketAlive(ctx, terminal.id, terminal.socket.String) {
			m.markOrphan(ctx, terminal.id)
		} else {
			m.markExited(ctx, terminal.id, 0)
		}
	}

	for _, terminalID := range m.liveSocketTerminalIDs(ctx) {
		if _, ok := tracked[terminalID]; ok {
			continue
		}
		var exists int
		if err := m.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM terminals WHERE id = ?`, terminalID).Scan(&exists); err != nil {
			return fmt.Errorf("check recovered terminal existence: %w", err)
		}
		if exists > 0 {
			m.markOrphan(ctx, terminalID)
			continue
		}
		home, _ := os.UserHomeDir()
		if home == "" {
			home = "."
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := m.DB.ExecContext(ctx, `INSERT INTO terminals (id, chapter_id, narrator_id, name, cwd, dtach_socket, status, exit_code, graph_opened, created_at) VALUES (?, NULL, NULL, ?, ?, ?, 'orphan', NULL, 0, ?)`, terminalID, "Recovered "+shortID(terminalID), home, m.socketPath(terminalID), now); err != nil {
			return fmt.Errorf("import recovered terminal: %w", err)
		}
	}
	return nil
}

func (m *Manager) Capabilities() map[string]any {
	dtachSupported := runtime.GOOS != "windows"
	caps := map[string]any{
		"supported":             runtime.GOOS != "windows",
		"directPty":             runtime.GOOS != "windows",
		"dtachSupported":        dtachSupported,
		"dtachAvailable":        dtachSupported && m.dtachAvailableNow(),
		"detachedReattach":      dtachSupported && m.dtachAvailableNow(),
		"scrollbackReplay":      true,
		"bufferStateReplay":     true,
		"maxSnapshotBytes":      maxSnapshotBytes,
		"multiClientResizeMode": "last-input-client",
	}
	if runtime.GOOS == "windows" {
		caps["reason"] = "Go terminal PTY runtime is not enabled on Windows; use the TypeScript backend or a Unix-like host for interactive terminals."
	}
	return caps
}

func (m *Manager) ActiveIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (m *Manager) OrphanIDs(ctx context.Context) []string {
	active := map[string]bool{}
	for _, id := range m.ActiveIDs() {
		active[id] = true
	}
	ids := []string{}
	for _, id := range m.liveSocketTerminalIDs(ctx) {
		if id != "" && !active[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func (m *Manager) KillOrphan(ctx context.Context, terminalID string) error {
	if strings.TrimSpace(terminalID) == "" {
		return errors.New("terminal session not found")
	}
	if !m.dtachAvailableNow() {
		return nil
	}
	m.killDtachSession(ctx, terminalID, m.socketPath(terminalID))
	return nil
}

func (m *Manager) ShellPID(terminalID string) (int, bool) {
	s := m.get(terminalID)
	if s == nil {
		if m.DB == nil || !m.dtachAvailableNow() {
			return 0, false
		}
		record, _, err := m.terminalRecord(context.Background(), terminalID)
		if err != nil || !record.DtachSocket.Valid {
			return 0, false
		}
		socketPath := strings.TrimSpace(record.DtachSocket.String)
		if socketPath == "" || !m.isDtachSocketAlive(context.Background(), terminalID, socketPath) {
			return 0, false
		}
		if pid := m.dtachShellPID(context.Background(), socketPath); pid > 0 {
			return pid, true
		}
		return 0, false
	}
	s.mu.Lock()
	useDtach := s.useDtach
	socketPath := s.dtachSocket
	cmd := s.cmd
	s.mu.Unlock()
	if useDtach && socketPath != "" {
		if pid := m.dtachShellPID(context.Background(), socketPath); pid > 0 {
			return pid, true
		}
		return 0, false
	}
	if cmd == nil || cmd.Process == nil {
		return 0, false
	}
	return cmd.Process.Pid, true
}

func (m *Manager) get(terminalID string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[terminalID]
}

func (m *Manager) startDirect(ctx context.Context, terminalID, cwd string, cols, rows int, existing *Session) error {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	args := []string{}
	if runtime.GOOS == "windows" {
		shell = "cmd.exe"
	} else {
		args = []string{"-l"}
	}
	cmd := exec.CommandContext(context.Background(), shell, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return err
	}
	session := existing
	if session == nil {
		session = &Session{id: terminalID, done: make(chan struct{})}
	}
	session.mu.Lock()
	session.cmd = cmd
	session.file = ptmx
	session.cols = cols
	session.rows = rows
	session.useDtach = false
	session.dtachSocket = ""
	session.orphan = false
	session.done = make(chan struct{})
	session.mu.Unlock()
	if err := m.storeSession(terminalID, session, ptmx, cmd, cols, rows); err != nil {
		return err
	}
	if m.DB != nil {
		if _, err := m.DB.ExecContext(ctx, `UPDATE terminals SET status = 'running', exit_code = NULL, dtach_socket = NULL WHERE id = ?`, terminalID); err != nil {
			m.cleanupFailedStart(terminalID, session, ptmx, cmd)
			return err
		}
	}
	m.emit(terminalID, map[string]any{"type": "requestResize", "terminalId": terminalID})
	go m.readLoop(session, ptmx)
	go m.waitLoop(session, cmd, ptmx, false)
	return nil
}

func (m *Manager) attachDtach(ctx context.Context, terminalID, socketPath string, cols, rows int, existing *Session) error {
	cmd := exec.CommandContext(context.Background(), "sh", "-c", `stty -echoctl 2>/dev/null; exec dtach -a "$1" -z`, "_", socketPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return err
	}
	session := existing
	if session == nil {
		session = &Session{id: terminalID, done: make(chan struct{})}
	}
	session.mu.Lock()
	session.cmd = cmd
	session.file = ptmx
	session.cols = cols
	session.rows = rows
	session.useDtach = true
	session.dtachSocket = socketPath
	session.orphan = false
	session.done = make(chan struct{})
	session.mu.Unlock()
	if err := m.storeSession(terminalID, session, ptmx, cmd, cols, rows); err != nil {
		return err
	}
	if m.DB != nil {
		if _, err := m.DB.ExecContext(ctx, `UPDATE terminals SET status = 'running', exit_code = NULL, dtach_socket = ? WHERE id = ?`, socketPath, terminalID); err != nil {
			m.cleanupFailedStart(terminalID, session, ptmx, cmd)
			return err
		}
	}
	m.emit(terminalID, map[string]any{"type": "requestResize", "terminalId": terminalID})
	go m.readLoop(session, ptmx)
	go m.waitLoop(session, cmd, ptmx, true)
	return nil
}

func (m *Manager) cleanupFailedStart(terminalID string, session *Session, ptmx *os.File, cmd *exec.Cmd) {
	m.mu.Lock()
	if m.sessions[terminalID] == session {
		delete(m.sessions, terminalID)
	}
	m.mu.Unlock()
	_ = ptmx.Close()
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func (m *Manager) storeSession(terminalID string, session *Session, ptmx *os.File, cmd *exec.Cmd, cols, rows int) error {
	m.mu.Lock()
	if existing, ok := m.sessions[terminalID]; ok && existing != session {
		m.mu.Unlock()
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		existing.mu.Lock()
		existing.cols = cols
		existing.rows = rows
		file := existing.file
		existing.mu.Unlock()
		if file != nil {
			_ = pty.Setsize(file, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
		}
		return nil
	}
	m.sessions[terminalID] = session
	m.mu.Unlock()
	return nil
}

func (m *Manager) readLoop(s *Session, file *os.File) {
	buf := make([]byte, 8192)
	for {
		n, err := file.Read(buf)
		if n > 0 {
			data := string(buf[:n])
			s.mu.Lock()
			s.appendBufferLocked(data)
			s.mu.Unlock()
			m.emit(s.id, map[string]any{"type": "output", "terminalId": s.id, "data": data})
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
				m.emit(s.id, map[string]any{"type": "error", "terminalId": s.id, "message": err.Error()})
			}
			return
		}
	}
}

func (s *Session) appendBufferLocked(data string) {
	_, _ = s.buf.WriteString(data)
	if s.buf.Len() > maxSnapshotBytes {
		contents := s.buf.Bytes()
		keep := append([]byte(nil), contents[len(contents)-maxSnapshotBytes:]...)
		s.buf.Reset()
		_, _ = s.buf.Write(keep)
	}
}

func (m *Manager) waitLoop(s *Session, cmd *exec.Cmd, file *os.File, attachOnly bool) {
	err := cmd.Wait()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}
	_ = file.Close()

	if attachOnly {
		s.mu.Lock()
		if s.cmd == cmd {
			s.cmd = nil
			s.file = nil
			s.orphan = true
		}
		socketPath := s.dtachSocket
		s.mu.Unlock()
		if socketPath != "" && m.isDtachSocketAlive(context.Background(), s.id, socketPath) {
			m.markOrphan(context.Background(), s.id)
			m.emit(s.id, map[string]any{"type": "orphan", "terminalId": s.id})
			return
		}
	}

	m.mu.Lock()
	if current := m.sessions[s.id]; current == s {
		delete(m.sessions, s.id)
	}
	m.mu.Unlock()
	m.markExited(context.Background(), s.id, exitCode)
	m.emit(s.id, map[string]any{"type": "exit", "terminalId": s.id, "code": exitCode})
	close(s.done)
}

func (m *Manager) terminalRecord(ctx context.Context, terminalID string) (terminalRecord, string, error) {
	var record terminalRecord
	if err := m.DB.QueryRowContext(ctx, `SELECT cwd, dtach_socket, status FROM terminals WHERE id = ?`, terminalID).Scan(&record.Cwd, &record.DtachSocket, &record.Status); err != nil {
		return record, "", err
	}
	cwd := "."
	if record.Cwd.Valid && strings.TrimSpace(record.Cwd.String) != "" {
		candidate := strings.TrimSpace(record.Cwd.String)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			cwd = candidate
		}
	}
	if cwd == "." {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			cwd = home
		}
	}
	return record, cwd, nil
}

func (m *Manager) createDtachSession(ctx context.Context, terminalID, cwd string) (string, error) {
	if runtime.GOOS == "windows" {
		return "", errors.New("dtach is not supported on Windows")
	}
	socketPath := m.socketPath(terminalID)
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return "", err
	}
	_ = os.Remove(socketPath)
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.CommandContext(context.Background(), "dtach", "-n", socketPath, "-z", shell, "-li")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return "", err
	}
	if err := waitForPath(ctx, socketPath, 3*time.Second); err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return "", err
	}
	if err := m.persistDtachSocket(ctx, terminalID, socketPath); err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = os.Remove(socketPath)
		return "", err
	}
	_ = cmd.Process.Release()
	return socketPath, nil
}

func (m *Manager) persistDtachSocket(ctx context.Context, terminalID, socketPath string) error {
	_, err := m.DB.ExecContext(ctx, `UPDATE terminals SET dtach_socket = ? WHERE id = ?`, socketPath, terminalID)
	return err
}

func waitForPath(ctx context.Context, path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return fmt.Errorf("dtach socket did not appear: %s", path)
}

func (m *Manager) dtachAvailableNow() bool {
	if m.dtachAvailable != nil {
		return m.dtachAvailable()
	}
	if runtime.GOOS == "windows" {
		return false
	}
	_, err := exec.LookPath("dtach")
	return err == nil
}

func (m *Manager) isDtachSocketAlive(ctx context.Context, terminalID, socketPath string) bool {
	if m.socketAlive != nil {
		return m.socketAlive(ctx, terminalID, socketPath)
	}
	info, err := os.Stat(socketPath)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return false
	}
	return len(m.findProcessesByArg(ctx, socketPath)) > 0
}

func (m *Manager) socketPath(terminalID string) string {
	dir := m.socketsDir
	if dir == "" {
		home, _ := os.UserHomeDir()
		if home == "" {
			home = "."
		}
		dir = filepath.Join(home, ".narrafork", "sockets")
	}
	safeID := strings.NewReplacer("/", "_", string(filepath.Separator), "_").Replace(terminalID)
	return filepath.Join(dir, "terminal-"+safeID+".sock")
}

func (m *Manager) liveSocketTerminalIDs(ctx context.Context) []string {
	dir := m.socketsDir
	if dir == "" {
		dir = filepath.Dir(m.socketPath("placeholder"))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	ids := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "terminal-") || !strings.HasSuffix(name, ".sock") {
			continue
		}
		terminalID := strings.TrimSuffix(strings.TrimPrefix(name, "terminal-"), ".sock")
		if m.isDtachSocketAlive(ctx, terminalID, filepath.Join(dir, name)) {
			ids = append(ids, terminalID)
		}
	}
	return ids
}

func (m *Manager) markExited(ctx context.Context, terminalID string, exitCode int) {
	if m.DB != nil {
		if _, err := m.DB.ExecContext(ctx, `UPDATE terminals SET status = 'exited', exit_code = ? WHERE id = ?`, exitCode, terminalID); err != nil {
			m.emitStatusPersistError(terminalID, "exited", err)
		}
	}
}

func (m *Manager) markOrphan(ctx context.Context, terminalID string) {
	if m.DB != nil {
		if _, err := m.DB.ExecContext(ctx, `UPDATE terminals SET status = 'orphan', exit_code = NULL WHERE id = ?`, terminalID); err != nil {
			m.emitStatusPersistError(terminalID, "orphan", err)
		}
	}
}

func (m *Manager) emitStatusPersistError(terminalID, status string, err error) {
	m.emit(terminalID, map[string]any{
		"type":       "error",
		"terminalId": terminalID,
		"code":       "TERMINAL_STATUS_PERSIST_FAILED",
		"message":    err.Error(),
		"status":     status,
		"fallback":   true,
		"fatal":      false,
	})
}

func (m *Manager) killDtachSession(ctx context.Context, terminalID, socketPath string) {
	pids := m.findProcessesByArg(ctx, socketPath)
	for _, pid := range pids {
		m.killProcessTree(ctx, pid)
	}
	if socketPath == "" {
		socketPath = m.socketPath(terminalID)
	}
	_ = os.Remove(socketPath)
}

func (m *Manager) dtachShellPID(ctx context.Context, socketPath string) int {
	snapshot := m.processSnapshot(ctx)
	for _, pid := range snapshot.findByArg(socketPath) {
		children := snapshot.descendants(pid)
		if len(children) > 0 {
			return children[0]
		}
	}
	return 0
}

func (m *Manager) findProcessesByArg(ctx context.Context, searchArg string) []int {
	return m.processSnapshot(ctx).findByArg(searchArg)
}

type processInfo struct {
	pid  int
	ppid int
	args string
}

type processSnapshot struct {
	items    map[int]processInfo
	children map[int][]int
}

func (m *Manager) processSnapshot(ctx context.Context) processSnapshot {
	if m.processSnapshotProvider != nil {
		return m.processSnapshotProvider(ctx)
	}
	snap := processSnapshot{items: map[int]processInfo{}, children: map[int][]int{}}
	if runtime.GOOS == "windows" {
		return snap
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "ps", "-ax", "-o", "pid=,ppid=,args=")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return snap
	}
	if err := cmd.Start(); err != nil {
		return snap
	}
	data, readErr := io.ReadAll(io.LimitReader(stdout, 2<<20))
	waitErr := cmd.Wait()
	if readErr != nil || waitErr != nil {
		return snap
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid := atoi(fields[0])
		ppid := atoi(fields[1])
		if pid <= 0 {
			continue
		}
		argsStart := strings.Index(line, fields[2])
		args := fields[2]
		if argsStart >= 0 {
			args = strings.TrimSpace(line[argsStart:])
		}
		snap.items[pid] = processInfo{pid: pid, ppid: ppid, args: args}
		snap.children[ppid] = append(snap.children[ppid], pid)
	}
	return snap
}

func (s processSnapshot) findByArg(searchArg string) []int {
	if searchArg == "" {
		return nil
	}
	pids := []int{}
	for pid, info := range s.items {
		if strings.Contains(info.args, searchArg) {
			pids = append(pids, pid)
		}
	}
	return pids
}

func (s processSnapshot) descendants(pid int) []int {
	out := []int{}
	queue := append([]int{}, s.children[pid]...)
	seen := map[int]bool{}
	for len(queue) > 0 {
		child := queue[0]
		queue = queue[1:]
		if seen[child] {
			continue
		}
		seen[child] = true
		out = append(out, child)
		queue = append(queue, s.children[child]...)
	}
	return out
}

func (m *Manager) killProcessTree(ctx context.Context, pid int) {
	if pid <= 0 || runtime.GOOS == "windows" {
		return
	}
	snapshot := m.processSnapshot(ctx)
	descendants := snapshot.descendants(pid)
	for i := len(descendants) - 1; i >= 0; i-- {
		_ = syscall.Kill(descendants[i], syscall.SIGKILL)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

func atoi(value string) int {
	var out int
	_, _ = fmt.Sscanf(strings.TrimSpace(value), "%d", &out)
	return out
}

func shortID(value string) string {
	if len(value) <= 8 {
		return value
	}
	return value[:8]
}

func (m *Manager) emitDtachFallback(terminalID, reason string) {
	m.emit(terminalID, map[string]any{
		"type":       "dtachFallback",
		"terminalId": terminalID,
		"reason":     reason,
		"mode":       "direct",
	})
}

func (m *Manager) emit(terminalID string, payload any) {
	if m.Broadcast != nil {
		m.Broadcast(terminalID, payload)
	}
}
