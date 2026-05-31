package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxOutputBytes       = 1024 * 1024
	maxConflictScanBytes = 512 * 1024
)

type Service struct {
	Timeout time.Duration
}

type CloneCredentials struct {
	Username string
	Password string
}

var (
	worktreeLocksMu sync.Mutex
	worktreeLocks   = map[string]*sync.Mutex{}
)

func (s *Service) WithWorktreeLock(path string, fn func() error) error {
	unlock := acquireWorktreeLock(path)
	defer unlock()
	return fn()
}

func acquireWorktreeLock(path string) func() {
	key := filepath.Clean(path)
	if abs, err := filepath.Abs(path); err == nil {
		key = abs
	}
	worktreeLocksMu.Lock()
	lock := worktreeLocks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		worktreeLocks[key] = lock
	}
	worktreeLocksMu.Unlock()

	lock.Lock()
	return func() { lock.Unlock() }
}

type Commit struct {
	SHA               string `json:"sha"`
	Message           string `json:"message"`
	FullMessage       string `json:"fullMessage,omitempty"`
	AuthorName        string `json:"authorName,omitempty"`
	AuthorEmail       string `json:"authorEmail,omitempty"`
	AuthoredAt        string `json:"authoredAt"`
	Source            string `json:"source,omitempty"`
	NarratorID        string `json:"narratorId,omitempty"`
	NarratorMessageID string `json:"narratorMessageId,omitempty"`
	FilesChanged      *int   `json:"filesChanged,omitempty"`
	LinesAdded        *int   `json:"linesAdded,omitempty"`
	LinesRemoved      *int   `json:"linesRemoved,omitempty"`
}

type CommitFile struct {
	Path         string `json:"path"`
	OldPath      string `json:"oldPath,omitempty"`
	Status       string `json:"status"`
	LinesAdded   int    `json:"linesAdded"`
	LinesRemoved int    `json:"linesRemoved"`
	Diff         string `json:"diff,omitempty"`
}

type Status struct {
	Branch  string   `json:"branch"`
	Clean   bool     `json:"clean"`
	Changed []string `json:"changed"`
	Raw     string   `json:"raw"`
}

type StatusFile struct {
	Status               string `json:"status"`
	Path                 string `json:"path"`
	LinesAdded           int    `json:"linesAdded"`
	LinesRemoved         int    `json:"linesRemoved"`
	StagedLinesAdded     int    `json:"stagedLinesAdded"`
	StagedLinesRemoved   int    `json:"stagedLinesRemoved"`
	UnstagedLinesAdded   int    `json:"unstagedLinesAdded"`
	UnstagedLinesRemoved int    `json:"unstagedLinesRemoved"`
}

type ConflictFile struct {
	File          string `json:"file"`
	ConflictLines int    `json:"conflictLines"`
}

type RebaseResult struct {
	Success       bool           `json:"success"`
	CommitSHA     string         `json:"commitSha,omitempty"`
	ConflictFiles []ConflictFile `json:"conflictFiles,omitempty"`
	Output        string         `json:"output,omitempty"`
}

type MergeResult struct {
	Success       bool     `json:"success"`
	CommitSHA     string   `json:"commitSha,omitempty"`
	ConflictFiles []string `json:"conflictFiles,omitempty"`
	Output        string   `json:"output,omitempty"`
	IsFastForward bool     `json:"isFastForward,omitempty"`
}

type StatusSummary struct {
	HasChanges   bool         `json:"hasChanges"`
	Staged       int          `json:"staged"`
	Unstaged     int          `json:"unstaged"`
	Untracked    int          `json:"untracked"`
	Files        []StatusFile `json:"files"`
	TotalFiles   int          `json:"totalFiles"`
	HeadSHA      string       `json:"headSha"`
	Branch       string       `json:"branch"`
	LinesAdded   int          `json:"linesAdded"`
	LinesRemoved int          `json:"linesRemoved"`
}

func NewService() *Service {
	return &Service{Timeout: 30 * time.Second}
}

func (s *Service) Available(ctx context.Context) bool {
	_, err := s.run(ctx, "", "git", "--version")
	return err == nil
}

func (s *Service) Clone(ctx context.Context, url, path, branch string) error {
	return s.CloneStreaming(ctx, url, path, branch, nil)
}

func (s *Service) CloneStreaming(ctx context.Context, url, path, branch string, onProgress func(string)) error {
	return s.CloneStreamingWithCredentials(ctx, url, path, branch, nil, onProgress)
}

func (s *Service) CloneStreamingWithCredentials(ctx context.Context, rawURL, path, branch string, credentials *CloneCredentials, onProgress func(string)) error {
	cloneURL := strings.TrimSpace(rawURL)
	if credentials == nil {
		credentials = cloneCredentialsFromURL(cloneURL)
	}
	cloneURL = RemoteURLWithoutCredentials(cloneURL)
	args := []string{"clone", "--progress"}
	if strings.TrimSpace(branch) != "" {
		args = append(args, "--branch", branch)
	}
	args = append(args, cloneURL, path)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	askpassPath := ""
	if credentials != nil && strings.TrimSpace(credentials.Username) != "" {
		file, err := os.CreateTemp("", "narrafork-askpass-*.sh")
		if err != nil {
			return err
		}
		askpassPath = file.Name()
		if _, err := file.WriteString("#!/bin/sh\ncase \"$1\" in\n*[Uu]sername*) printf '%s\\n' \"$NARRAFORK_GIT_USERNAME\";;\n*) printf '%s\\n' \"$NARRAFORK_GIT_PASSWORD\";;\nesac\n"); err != nil {
			_ = file.Close()
			_ = os.Remove(askpassPath)
			return err
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(askpassPath)
			return err
		}
		if err := os.Chmod(askpassPath, 0o700); err != nil {
			_ = os.Remove(askpassPath)
			return err
		}
		defer os.Remove(askpassPath)
		cmd.Env = append(cmd.Env,
			"GIT_ASKPASS="+askpassPath,
			"NARRAFORK_GIT_USERNAME="+credentials.Username,
			"NARRAFORK_GIT_PASSWORD="+credentials.Password,
		)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var wg sync.WaitGroup
	var linesMu sync.Mutex
	recentLines := []string{}
	scan := func(r io.Reader) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		scanner.Split(scanGitProgressLines)
		scanner.Buffer(make([]byte, 1024), 256*1024)
		for scanner.Scan() {
			line := maskCloneSecrets(strings.TrimSpace(scanner.Text()), rawURL, credentials)
			if line == "" {
				continue
			}
			linesMu.Lock()
			recentLines = append(recentLines, line)
			if len(recentLines) > 8 {
				recentLines = recentLines[len(recentLines)-8:]
			}
			linesMu.Unlock()
			if onProgress != nil {
				onProgress(line)
			}
		}
	}
	wg.Add(2)
	go scan(stdout)
	go scan(stderr)
	err = cmd.Wait()
	wg.Wait()
	if err == nil {
		return nil
	}
	linesMu.Lock()
	detail := strings.TrimSpace(strings.Join(recentLines, "\n"))
	linesMu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if detail == "" {
		detail = err.Error()
	}
	if isCloneAuthError(detail) {
		return fmt.Errorf("git authentication required: %s", detail)
	}
	return fmt.Errorf("failed to clone repo: %s", detail)
}

func (s *Service) InitRepo(ctx context.Context, path string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	_, err := s.run(ctx, path, "git", "init")
	return err
}

func (s *Service) IsRepo(ctx context.Context, path string) bool {
	out, err := s.run(ctx, path, "git", "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

func (s *Service) CurrentBranch(ctx context.Context, path string) (string, error) {
	out, err := s.run(ctx, path, "git", "branch", "--show-current")
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(out)
	if branch == "" {
		return "main", nil
	}
	return branch, nil
}

func (s *Service) BranchExists(ctx context.Context, repoPath, branch string) bool {
	_, err := s.run(ctx, repoPath, "git", "rev-parse", "--verify", "refs/heads/"+branch)
	return err == nil
}

func (s *Service) ValidateBranch(ctx context.Context, branch string) error {
	if strings.TrimSpace(branch) == "" || strings.Contains(branch, "..") || strings.ContainsAny(branch, " \t\n\r") {
		return fmt.Errorf("invalid branch name")
	}
	_, err := s.run(ctx, "", "git", "check-ref-format", "--branch", branch)
	return err
}

func (s *Service) CreateBranch(ctx context.Context, repoPath, branch, base string) error {
	if err := s.ValidateBranch(ctx, branch); err != nil {
		return err
	}
	args := []string{"branch", branch}
	if base != "" {
		args = append(args, base)
	}
	_, err := s.run(ctx, repoPath, "git", args...)
	return err
}

func (s *Service) DeleteBranch(ctx context.Context, repoPath, branch string) error {
	_, err := s.run(ctx, repoPath, "git", "branch", "-D", branch)
	return err
}

func (s *Service) CreateWorktree(ctx context.Context, repoPath, worktreePath, branch string) error {
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return err
	}
	_, err := s.run(ctx, repoPath, "git", "worktree", "add", worktreePath, branch)
	return err
}

func (s *Service) RemoveWorktree(ctx context.Context, repoPath, worktreePath string) error {
	_, err := s.run(ctx, repoPath, "git", "worktree", "remove", "--force", worktreePath)
	return err
}

func (s *Service) PruneWorktrees(ctx context.Context, repoPath string) error {
	_, err := s.run(ctx, repoPath, "git", "worktree", "prune")
	return err
}

func (s *Service) HeadCommit(ctx context.Context, path string) (string, error) {
	return s.HeadCommitRef(ctx, path, "HEAD")
}

func (s *Service) HeadCommitRef(ctx context.Context, path, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		ref = "HEAD"
	}
	out, err := s.run(ctx, path, "git", "rev-parse", ref)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (s *Service) CommitCount(ctx context.Context, path string) (int, error) {
	return s.CommitCountRef(ctx, path, "HEAD")
}

func (s *Service) CommitCountRef(ctx context.Context, path, ref string) (int, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		ref = "HEAD"
	}
	out, err := s.run(ctx, path, "git", "rev-list", "--count", ref)
	if err != nil {
		return 0, err
	}
	count, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, nil
	}
	return count, nil
}

func (s *Service) Status(ctx context.Context, path string) (Status, error) {
	branch, _ := s.CurrentBranch(ctx, path)
	out, err := s.run(ctx, path, "git", "status", "--porcelain=v1")
	if err != nil {
		return Status{}, err
	}
	lines := splitLines(out)
	changed := make([]string, 0, len(lines))
	for _, line := range lines {
		if len(line) > 3 {
			changed = append(changed, strings.TrimSpace(line[3:]))
		}
	}
	return Status{Branch: branch, Clean: len(lines) == 0, Changed: changed, Raw: out}, nil
}

func (s *Service) Log(ctx context.Context, path string, limit int) ([]Commit, error) {
	return s.LogRef(ctx, path, "", limit)
}

func (s *Service) LogRef(ctx context.Context, path, ref string, limit int) ([]Commit, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	format := "%H%x1f%s%x1f%an%x1f%ae%x1f%aI"
	args := []string{"log", "--max-count", strconv.Itoa(limit), "--pretty=format:" + format}
	if ref = strings.TrimSpace(ref); ref != "" {
		args = append(args, ref)
	}
	out, err := s.run(ctx, path, "git", args...)
	if err != nil {
		return nil, err
	}
	lines := splitLines(out)
	commits := make([]Commit, 0, len(lines))
	for _, line := range lines {
		parts := strings.Split(line, "\x1f")
		if len(parts) < 5 {
			continue
		}
		commits = append(commits, Commit{SHA: parts[0], Message: parts[1], AuthorName: parts[2], AuthorEmail: parts[3], AuthoredAt: parts[4]})
	}
	return commits, nil
}

func (s *Service) ShowCommit(ctx context.Context, path, sha string) (Commit, error) {
	format := "%H%x1f%s%x1f%B%x1f%an%x1f%ae%x1f%aI"
	out, err := s.run(ctx, path, "git", "show", "--quiet", "--pretty=format:"+format, sha)
	if err != nil {
		return Commit{}, err
	}
	parts := strings.SplitN(out, "\x1f", 6)
	if len(parts) < 6 {
		return Commit{}, fmt.Errorf("unexpected git show output")
	}
	return Commit{SHA: parts[0], Message: parts[1], FullMessage: strings.TrimSpace(parts[2]), AuthorName: parts[3], AuthorEmail: parts[4], AuthoredAt: strings.TrimSpace(parts[5])}, nil
}

func (s *Service) CheckMerge(ctx context.Context, path, sourceBranch string) (string, error) {
	out, err := s.run(ctx, path, "git", "merge-tree", "HEAD", sourceBranch)
	return out, err
}

func (s *Service) GetRefCommit(ctx context.Context, repoPath, ref string) (string, error) {
	out, err := s.run(ctx, repoPath, "git", "rev-parse", ref)
	return strings.TrimSpace(out), err
}

func (s *Service) MergeTree(ctx context.Context, repoPath, baseSHA, ourRef, theirRef string) (bool, []string, string, error) {
	out, err := s.run(ctx, repoPath, "git", "merge-tree", baseSHA, ourRef, theirRef)
	if err != nil {
		return false, nil, out, err
	}
	conflicts := parseMergeTreeConflictFiles(out)
	return len(conflicts) > 0, conflicts, out, nil
}

func parseMergeTreeConflictFiles(out string) []string {
	seen := map[string]bool{}
	files := []string{}
	add := func(file string) {
		file = strings.TrimSpace(file)
		file = strings.TrimPrefix(file, "b/")
		if file == "" || seen[file] {
			return
		}
		seen[file] = true
		files = append(files, file)
	}
	inConflictSection := false
	for _, line := range splitLines(out) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "changed in both" || strings.HasPrefix(trimmed, "CONFLICT ") {
			inConflictSection = true
		}
		if strings.HasPrefix(line, "+++") {
			add(strings.TrimSpace(strings.TrimPrefix(line, "+++")))
			continue
		}
		if strings.HasPrefix(trimmed, "CONFLICT ") {
			if idx := strings.LastIndex(trimmed, " in "); idx >= 0 {
				add(trimmed[idx+4:])
				continue
			}
			fields := strings.Fields(trimmed)
			if len(fields) > 0 {
				add(fields[len(fields)-1])
			}
			continue
		}
		if inConflictSection && (strings.HasPrefix(trimmed, "our ") || strings.HasPrefix(trimmed, "their ")) {
			fields := strings.Fields(trimmed)
			if len(fields) > 0 {
				add(fields[len(fields)-1])
			}
			continue
		}
		if strings.HasPrefix(trimmed, "@@ ") {
			inConflictSection = false
		}
	}
	return files
}

func (s *Service) Merge(ctx context.Context, path, sourceBranch string) (string, error) {
	result, err := s.MergeWithStrategy(ctx, path, sourceBranch, "merge", "", false)
	return result.Output, err
}

func (s *Service) MergeWithStrategy(ctx context.Context, path, sourceBranch, strategy, message string, fastForward bool) (MergeResult, error) {
	if strings.TrimSpace(message) == "" {
		message = "Merge " + sourceBranch
	}
	args := []string{}
	if strategy == "squash" {
		args = []string{"merge", "--squash", sourceBranch}
	} else if fastForward {
		args = []string{"merge", "--ff-only", sourceBranch}
	} else {
		args = []string{"merge", "--no-ff", "-m", message, sourceBranch}
	}
	out, err := s.run(ctx, path, "git", args...)
	if err != nil && fastForward && strategy != "squash" {
		out, err = s.run(ctx, path, "git", "merge", "--no-ff", "-m", message, sourceBranch)
		fastForward = false
	}
	if err != nil {
		combined := strings.TrimSpace(out + "\n" + err.Error())
		conflicts, conflictErr := s.conflictFileNames(ctx, path)
		if conflictErr == nil && (len(conflicts) > 0 || strings.Contains(combined, "CONFLICT")) {
			return MergeResult{Success: false, ConflictFiles: conflicts, Output: combined}, nil
		}
		return MergeResult{Output: combined}, err
	}
	if strategy == "squash" {
		commitOut, commitErr := s.run(ctx, path, "git", "commit", "-m", message)
		out = strings.TrimSpace(out + "\n" + commitOut)
		if commitErr != nil {
			return MergeResult{Output: strings.TrimSpace(out + "\n" + commitErr.Error())}, commitErr
		}
	}
	head, _ := s.HeadCommit(ctx, path)
	return MergeResult{Success: true, CommitSHA: head, Output: out, IsFastForward: fastForward}, nil
}

func (s *Service) GetMergeBase(ctx context.Context, repoPath, branchA, branchB string) (string, error) {
	out, err := s.run(ctx, repoPath, "git", "merge-base", branchA, branchB)
	return strings.TrimSpace(out), err
}

func (s *Service) MergeNoCommit(ctx context.Context, path, sourceBranch, strategy string) (MergeResult, error) {
	args := []string{"merge", "--no-ff", "--no-commit", sourceBranch}
	if strategy == "squash" {
		args = []string{"merge", "--squash", "--no-commit", sourceBranch}
	}
	out, err := s.run(ctx, path, "git", args...)
	if err == nil {
		return MergeResult{Success: true, Output: out}, nil
	}
	combined := strings.TrimSpace(out + "\n" + err.Error())
	conflicts, conflictErr := s.conflictFileNames(ctx, path)
	if conflictErr == nil && (len(conflicts) > 0 || strings.Contains(combined, "CONFLICT")) {
		return MergeResult{Success: false, ConflictFiles: conflicts, Output: combined}, nil
	}
	return MergeResult{Output: combined}, err
}

func (s *Service) AutoCommit(ctx context.Context, path, message string) (string, error) {
	if strings.TrimSpace(message) == "" {
		message = "Apply changes"
	}
	if _, err := s.run(ctx, path, "git", "add", "-A"); err != nil {
		return "", err
	}
	if _, err := s.run(ctx, path, "git", "diff", "--cached", "--quiet"); err == nil {
		return s.HeadCommit(ctx, path)
	}
	if _, err := s.run(ctx, path, "git", "commit", "-m", message); err != nil {
		return "", err
	}
	return s.HeadCommit(ctx, path)
}

func (s *Service) IsAncestor(ctx context.Context, repoPath, ancestor, descendant string) bool {
	_, err := s.run(ctx, repoPath, "git", "merge-base", "--is-ancestor", ancestor, descendant)
	return err == nil
}

func (s *Service) CherryPick(ctx context.Context, worktreePath, repoPath, sourceBranch, baseSHA string) (MergeResult, error) {
	out, err := s.run(ctx, repoPath, "git", "rev-list", "--reverse", baseSHA+".."+sourceBranch)
	if err != nil {
		return MergeResult{Output: out}, err
	}
	commits := splitLines(out)
	if len(commits) == 0 {
		return MergeResult{Success: true, Output: out}, nil
	}
	combined := strings.Builder{}
	for _, commit := range commits {
		pickOut, pickErr := s.run(ctx, worktreePath, "git", "cherry-pick", commit)
		if pickOut != "" {
			combined.WriteString(pickOut)
			combined.WriteString("\n")
		}
		if pickErr != nil {
			message := strings.TrimSpace(combined.String() + "\n" + pickErr.Error())
			conflicts, conflictErr := s.conflictFileNames(ctx, worktreePath)
			if conflictErr == nil && (len(conflicts) > 0 || strings.Contains(message, "CONFLICT")) {
				_, _ = s.run(ctx, worktreePath, "git", "cherry-pick", "--abort")
				return MergeResult{Success: false, ConflictFiles: conflicts, Output: message}, nil
			}
			return MergeResult{Output: message}, pickErr
		}
	}
	head, _ := s.HeadCommit(ctx, worktreePath)
	return MergeResult{Success: true, CommitSHA: head, Output: strings.TrimSpace(combined.String())}, nil
}

func (s *Service) AbortMerge(ctx context.Context, path string) {
	_, _ = s.run(ctx, path, "git", "merge", "--abort")
}

func (s *Service) Rebase(ctx context.Context, path, upstream string) (RebaseResult, error) {
	if strings.TrimSpace(upstream) == "" {
		return RebaseResult{}, fmt.Errorf("upstream is required")
	}
	out, err := s.run(ctx, path, "git", "rebase", upstream)
	if err == nil {
		head, _ := s.HeadCommit(ctx, path)
		return RebaseResult{Success: true, CommitSHA: head, Output: out}, nil
	}
	conflicts, conflictsErr := s.ConflictFiles(ctx, path)
	if conflictsErr != nil {
		return RebaseResult{}, conflictsErr
	}
	if len(conflicts) == 0 {
		return RebaseResult{}, err
	}
	return RebaseResult{Success: false, ConflictFiles: conflicts, Output: strings.TrimSpace(out + "\n" + err.Error())}, nil
}

func (s *Service) RebaseAbort(ctx context.Context, path string) error {
	_, err := s.run(ctx, path, "git", "rebase", "--abort")
	return err
}

func (s *Service) ConflictFiles(ctx context.Context, path string) ([]ConflictFile, error) {
	out, err := s.run(ctx, path, "git", "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	files := []ConflictFile{}
	for _, file := range splitLines(out) {
		clean := filepath.Clean(file)
		if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
			continue
		}
		conflictLines := countConflictMarkersLimited(filepath.Join(path, clean))
		files = append(files, ConflictFile{File: clean, ConflictLines: conflictLines})
	}
	return files, nil
}

func countConflictMarkersLimited(path string) int {
	file, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxConflictScanBytes+1))
	if err != nil {
		return 0
	}
	if len(data) > maxConflictScanBytes {
		data = data[:maxConflictScanBytes]
	}
	return strings.Count(string(data), "<<<<<<<")
}

func (s *Service) conflictFileNames(ctx context.Context, path string) ([]string, error) {
	conflicts, err := s.ConflictFiles(ctx, path)
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(conflicts))
	for _, conflict := range conflicts {
		files = append(files, conflict.File)
	}
	return files, nil
}

func (s *Service) GetStatus(ctx context.Context, path string) (string, error) {
	return s.run(ctx, path, "git", "status", "--porcelain=v1")
}

func (s *Service) ShowFileAtRef(ctx context.Context, path, ref, filePath string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		ref = "HEAD"
	}
	filePath = strings.TrimSpace(filePath)
	if filePath == "" || filepath.IsAbs(filePath) || strings.HasPrefix(filepath.Clean(filePath), "..") {
		return "", fmt.Errorf("invalid git file path")
	}
	return s.run(ctx, path, "git", "show", ref+":"+filepath.ToSlash(filePath))
}

func (s *Service) StatusSummary(ctx context.Context, path string) (StatusSummary, error) {
	out, err := s.GetStatus(ctx, path)
	if err != nil {
		return StatusSummary{}, err
	}
	branch, _ := s.CurrentBranch(ctx, path)
	head, _ := s.HeadCommit(ctx, path)
	lines := splitLines(out)
	summary := StatusSummary{HasChanges: len(lines) > 0, Files: []StatusFile{}, TotalFiles: len(lines), HeadSHA: head, Branch: branch}
	for _, line := range lines {
		if len(line) < 3 {
			continue
		}
		status := line[:2]
		filePath := strings.TrimSpace(line[3:])
		if len(summary.Files) < 200 {
			summary.Files = append(summary.Files, StatusFile{Status: status, Path: filePath})
		}
		x, y := status[0], status[1]
		if y == '?' {
			summary.Untracked++
			continue
		}
		if x != ' ' && x != '?' {
			summary.Staged++
		}
		if y != ' ' && y != '?' {
			summary.Unstaged++
		}
	}
	return summary, nil
}

func (s *Service) StageAll(ctx context.Context, path string) error {
	_, err := s.run(ctx, path, "git", "add", "-A")
	return err
}

func (s *Service) StageFiles(ctx context.Context, path string, files []string) error {
	args := append([]string{"add", "--"}, files...)
	_, err := s.run(ctx, path, "git", args...)
	return err
}

func (s *Service) UnstageAll(ctx context.Context, path string) error {
	_, err := s.run(ctx, path, "git", "reset")
	return err
}

func (s *Service) UnstageFiles(ctx context.Context, path string, files []string) error {
	args := append([]string{"restore", "--staged", "--"}, files...)
	_, err := s.run(ctx, path, "git", args...)
	return err
}

func (s *Service) Commit(ctx context.Context, path, message string) (string, error) {
	if strings.TrimSpace(message) == "" {
		return "", fmt.Errorf("commit message is required")
	}
	if _, err := s.run(ctx, path, "git", "commit", "-m", message); err != nil {
		return "", err
	}
	return s.HeadCommit(ctx, path)
}

func (s *Service) DiscardAll(ctx context.Context, path string) error {
	if _, err := s.run(ctx, path, "git", "checkout", "HEAD", "--", "."); err != nil {
		return err
	}
	_, err := s.run(ctx, path, "git", "clean", "-fd")
	return err
}

func (s *Service) DiscardFiles(ctx context.Context, path string, files []string) error {
	if len(files) == 0 {
		return nil
	}
	args := append([]string{"status", "--porcelain", "--"}, files...)
	out, err := s.run(ctx, path, "git", args...)
	if err != nil {
		return err
	}
	tracked := []string{}
	untracked := []string{}
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		filePath := strings.TrimSpace(line[3:])
		if filePath == "" {
			continue
		}
		if line[0] == '?' && line[1] == '?' {
			untracked = append(untracked, filePath)
		} else {
			tracked = append(tracked, filePath)
		}
	}
	if len(tracked) > 0 {
		restoreArgs := append([]string{"checkout", "HEAD", "--"}, tracked...)
		if _, err := s.run(ctx, path, "git", restoreArgs...); err != nil {
			return err
		}
	}
	if len(untracked) > 0 {
		cleanArgs := append([]string{"clean", "-f", "--"}, untracked...)
		if _, err := s.run(ctx, path, "git", cleanArgs...); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) FileDiff(ctx context.Context, path, file string, staged bool) (map[string]any, error) {
	args := []string{"diff", "--", file}
	if staged {
		args = []string{"diff", "--cached", "--", file}
	}
	out, err := s.run(ctx, path, "git", args...)
	if err != nil {
		return nil, err
	}
	truncated := false
	if len(out) > 200000 {
		out = out[:200000]
		truncated = true
	}
	return map[string]any{"diff": out, "truncated": truncated}, nil
}

func (s *Service) CommitFileDiff(ctx context.Context, path, sha, file string) (map[string]any, error) {
	out, err := s.run(ctx, path, "git", "show", "--format=", "--no-ext-diff", "--unified=3", sha, "--", file)
	if err != nil {
		return nil, err
	}
	truncated := false
	if len(out) > 200000 {
		out = out[:200000]
		truncated = true
	}
	return map[string]any{"diff": out, "truncated": truncated}, nil
}

func (s *Service) CommitDiffStats(ctx context.Context, path, sha string) (filesChanged, linesAdded, linesRemoved int, err error) {
	out, err := s.run(ctx, path, "git", "show", "--shortstat", "--format=", sha)
	if err != nil {
		return 0, 0, 0, err
	}
	for _, part := range strings.Split(out, ",") {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) < 2 {
			continue
		}
		value, parseErr := strconv.Atoi(fields[0])
		if parseErr != nil {
			continue
		}
		text := strings.Join(fields[1:], " ")
		switch {
		case strings.Contains(text, "file") && strings.Contains(text, "changed"):
			filesChanged = value
		case strings.Contains(text, "insertion"):
			linesAdded = value
		case strings.Contains(text, "deletion"):
			linesRemoved = value
		}
	}
	return filesChanged, linesAdded, linesRemoved, nil
}

func (s *Service) CommitFiles(ctx context.Context, path, sha string) ([]CommitFile, error) {
	numstat, err := s.run(ctx, path, "git", "show", "--numstat", "--format=", sha)
	if err != nil {
		return nil, err
	}
	nameStatus, err := s.run(ctx, path, "git", "show", "--name-status", "--format=", sha)
	if err != nil {
		return nil, err
	}
	files := []CommitFile{}
	byPath := map[string]int{}
	for _, line := range splitLines(numstat) {
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}
		added := parseGitStatInt(parts[0])
		removed := parseGitStatInt(parts[1])
		pathValue := parts[len(parts)-1]
		if pathValue == "" {
			continue
		}
		byPath[pathValue] = len(files)
		files = append(files, CommitFile{Path: pathValue, Status: "modified", LinesAdded: added, LinesRemoved: removed})
	}
	for _, line := range splitLines(nameStatus) {
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		code := parts[0]
		pathValue := parts[len(parts)-1]
		oldPath := ""
		if strings.HasPrefix(code, "R") && len(parts) >= 3 {
			oldPath = parts[1]
			pathValue = parts[2]
		}
		idx, ok := byPath[pathValue]
		if !ok {
			idx = len(files)
			byPath[pathValue] = idx
			files = append(files, CommitFile{Path: pathValue})
		}
		files[idx].Status = commitStatusFromCode(code)
		files[idx].OldPath = oldPath
	}
	return files, nil
}

func (s *Service) CherryPickCommits(ctx context.Context, path string, commits []string) (MergeResult, error) {
	if len(commits) == 0 {
		return MergeResult{Success: true}, nil
	}
	combined := strings.Builder{}
	for _, sha := range commits {
		sha = strings.TrimSpace(sha)
		if sha == "" {
			continue
		}
		out, err := s.run(ctx, path, "git", "cherry-pick", sha)
		if strings.TrimSpace(out) != "" {
			combined.WriteString(out)
			combined.WriteString("\n")
		}
		if err != nil {
			message := strings.TrimSpace(combined.String() + "\n" + err.Error())
			conflicts, conflictErr := s.conflictFileNames(ctx, path)
			if conflictErr == nil && (len(conflicts) > 0 || strings.Contains(message, "CONFLICT")) {
				return MergeResult{Success: false, ConflictFiles: conflicts, Output: message}, nil
			}
			return MergeResult{Output: message}, err
		}
	}
	head, _ := s.HeadCommit(ctx, path)
	return MergeResult{Success: true, CommitSHA: head, Output: strings.TrimSpace(combined.String())}, nil
}

func (s *Service) CountCommitsBetween(ctx context.Context, path, fromRef, toRef string) (int, error) {
	out, err := s.run(ctx, path, "git", "rev-list", "--count", strings.TrimSpace(fromRef)+".."+strings.TrimSpace(toRef))
	if err != nil {
		return 0, err
	}
	count, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Service) StashList(ctx context.Context, path string) ([]map[string]any, error) {
	out, err := s.run(ctx, path, "git", "stash", "list", "--pretty=format:%gd%x1f%gs%x1f%ai")
	if err != nil {
		return nil, err
	}
	items := []map[string]any{}
	for _, line := range splitLines(out) {
		parts := strings.Split(line, "\x1f")
		if len(parts) >= 2 {
			idx := 0
			ref := parts[0]
			idxText := strings.TrimSuffix(strings.TrimPrefix(ref, "stash@{"), "}")
			if parsed, parseErr := strconv.Atoi(idxText); parseErr == nil {
				idx = parsed
			}
			date := ""
			if len(parts) >= 3 {
				date = parts[2]
			}
			items = append(items, map[string]any{"index": idx, "message": parts[1], "date": date, "ref": ref})
		}
	}
	return items, nil
}

func (s *Service) Stash(ctx context.Context, path, message string) error {
	args := []string{"stash", "push", "--include-untracked"}
	if message != "" {
		args = append(args, "-m", message)
	}
	_, err := s.run(ctx, path, "git", args...)
	return err
}

type StashResult struct {
	HasConflicts bool   `json:"hasConflicts"`
	Output       string `json:"output,omitempty"`
}

func (s *Service) StashPop(ctx context.Context, path string) (StashResult, error) {
	return s.stashApplyLike(ctx, path, "pop")
}

func (s *Service) StashApply(ctx context.Context, path string, index int) (StashResult, error) {
	return s.stashApplyLike(ctx, path, "apply", fmt.Sprintf("stash@{%d}", index))
}

func (s *Service) stashApplyLike(ctx context.Context, path string, args ...string) (StashResult, error) {
	out, err := s.run(ctx, path, "git", append([]string{"stash"}, args...)...)
	if err == nil {
		return StashResult{Output: out}, nil
	}
	combined := strings.TrimSpace(out + "\n" + err.Error())
	conflicts, conflictErr := s.conflictFileNames(ctx, path)
	if conflictErr == nil && (len(conflicts) > 0 || strings.Contains(combined, "CONFLICT")) {
		return StashResult{HasConflicts: true, Output: combined}, nil
	}
	return StashResult{Output: combined}, err
}

func (s *Service) StashDrop(ctx context.Context, path string, index int) error {
	_, err := s.run(ctx, path, "git", "stash", "drop", fmt.Sprintf("stash@{%d}", index))
	return err
}

func (s *Service) Reset(ctx context.Context, path, target, mode string) error {
	if mode != "hard" {
		mode = "soft"
	}
	_, err := s.run(ctx, path, "git", "reset", "--"+mode, target)
	return err
}

func (s *Service) IsMergeCommit(ctx context.Context, path, sha string) (bool, error) {
	out, err := s.run(ctx, path, "git", "rev-list", "--parents", "-n", "1", sha)
	if err != nil {
		return false, err
	}
	return len(strings.Fields(out)) > 2, nil
}

func (s *Service) RevertCommit(ctx context.Context, path, sha string) (string, error) {
	if _, err := s.run(ctx, path, "git", "revert", "--no-edit", sha); err != nil {
		_, _ = s.run(ctx, path, "git", "revert", "--abort")
		return "", err
	}
	return s.HeadCommit(ctx, path)
}

func (s *Service) RevertMergeCommit(ctx context.Context, path, sha string) (string, error) {
	if _, err := s.run(ctx, path, "git", "revert", "-m", "1", "--no-edit", sha); err != nil {
		_, _ = s.run(ctx, path, "git", "revert", "--abort")
		return "", err
	}
	return s.HeadCommit(ctx, path)
}

func (s *Service) FullDiff(ctx context.Context, path string) (string, error) {
	out, err := s.run(ctx, path, "git", "diff", "HEAD", "-D", "--no-color")
	if err != nil {
		return "", err
	}
	parts := []string{}
	total := 0
	addPart := func(text string) bool {
		if text == "" {
			return true
		}
		if total+len(text) > 100000 {
			remaining := 100000 - total
			if remaining > 0 {
				parts = append(parts, text[:remaining])
			}
			parts = append(parts, "\n\n[diff truncated — exceeded size limit]")
			return false
		}
		parts = append(parts, text)
		total += len(text)
		return true
	}
	if !addPart(out) {
		return strings.Join(parts, "\n"), nil
	}
	untrackedOut, err := s.run(ctx, path, "git", "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return strings.Join(parts, "\n"), nil
	}
	untracked := splitLines(untrackedOut)
	limit := len(untracked)
	if limit > 50 {
		limit = 50
	}
	for _, file := range untracked[:limit] {
		diff, _ := s.run(ctx, path, "git", "diff", "--no-index", "--no-color", os.DevNull, file)
		if diff != "" && !addPart(diff) {
			break
		}
	}
	if len(untracked) > limit {
		addPart(fmt.Sprintf("\n\n[... %d more untracked files omitted]", len(untracked)-limit))
	}
	return strings.Join(parts, "\n"), nil
}

func (s *Service) DiffBetweenRefs(ctx context.Context, repoPath, baseRef, headRef string) (string, error) {
	out, err := s.run(ctx, repoPath, "git", "diff", "--no-color", baseRef+".."+headRef)
	if err != nil {
		return "", err
	}
	if len(out) > 100000 {
		return out[:100000] + "\n\n[diff truncated — exceeded size limit]", nil
	}
	return out, nil
}

func (s *Service) run(parent context.Context, cwd, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, s.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	stdout := &limitWriter{limit: maxOutputBytes}
	stderr := &limitWriter{limit: maxOutputBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if ctx.Err() != nil {
			return stdout.String(), ctx.Err()
		}
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), errors.New(msg)
	}
	return stdout.String(), nil
}

type limitWriter struct {
	buf   bytes.Buffer
	limit int
}

func (w *limitWriter) Write(p []byte) (int, error) {
	remaining := w.limit - w.buf.Len()
	if remaining > 0 {
		if len(p) > remaining {
			_, _ = w.buf.Write(p[:remaining])
		} else {
			_, _ = w.buf.Write(p)
		}
	}
	return len(p), nil
}

func (w *limitWriter) String() string {
	return w.buf.String()
}

func parseGitStatInt(value string) int {
	if value == "-" {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return n
}

func commitStatusFromCode(code string) string {
	if code == "" {
		return "modified"
	}
	switch code[0] {
	case 'A':
		return "added"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	case 'C':
		return "copied"
	case 'T':
		return "typechange"
	default:
		return "modified"
	}
}

func splitLines(s string) []string {
	raw := strings.Split(strings.TrimSpace(s), "\n")
	if len(raw) == 1 && raw[0] == "" {
		return nil
	}
	out := raw[:0]
	for _, line := range raw {
		line = strings.TrimRight(line, "\r")
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func scanGitProgressLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for i, b := range data {
		if b == '\n' || b == '\r' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func isCloneAuthError(message string) bool {
	lower := strings.ToLower(message)
	needles := []string{"authentication failed", "could not read username", "terminal prompts disabled", "could not read password", "invalid credentials", "logon failed", "permission denied"}
	for _, needle := range needles {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func maskCloneSecrets(line, rawURL string, credentials *CloneCredentials) string {
	masked := line
	if safe := RemoteURLWithoutCredentials(rawURL); safe != rawURL {
		masked = strings.ReplaceAll(masked, rawURL, safe)
	}
	if credentials != nil {
		if credentials.Password != "" {
			masked = strings.ReplaceAll(masked, credentials.Password, "***")
		}
		if credentials.Username != "" {
			masked = strings.ReplaceAll(masked, credentials.Username+":***@", "***@")
		}
	}
	return masked
}

func RemoteURLWithoutCredentials(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed == nil || parsed.User == nil {
		return strings.TrimSpace(rawURL)
	}
	parsed.User = nil
	return parsed.String()
}

func cloneCredentialsFromURL(rawURL string) *CloneCredentials {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed == nil || parsed.User == nil {
		return nil
	}
	username := parsed.User.Username()
	password, _ := parsed.User.Password()
	if username == "" && password == "" {
		return nil
	}
	return &CloneCredentials{Username: username, Password: password}
}
