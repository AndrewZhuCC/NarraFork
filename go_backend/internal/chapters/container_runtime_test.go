package chapters

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"narrafork-go/internal/config"
	gitx "narrafork-go/internal/git"

	_ "modernc.org/sqlite"
)

func TestLimitedCombinedWriterTruncatesContainerOutput(t *testing.T) {
	writer := newLimitedCombinedWriter(16)
	if _, err := writer.Write(bytes.Repeat([]byte("x"), 32)); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := writer.String()
	if len(out) > 64 || !strings.Contains(out, "[truncated]") {
		t.Fatalf("limited output = %q, want truncation marker", out)
	}
}

func TestCopyFileWithModeStreamsLargeFiles(t *testing.T) {
	src := filepath.Join(t.TempDir(), "large.bin")
	dst := filepath.Join(t.TempDir(), "nested", "large.bin")
	if err := os.WriteFile(src, bytes.Repeat([]byte("x"), 3*1024*1024), 0o750); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := copyFileWithMode(src, dst, 0o750); err != nil {
		t.Fatalf("copy file: %v", err)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat copied file: %v", err)
	}
	if info.Size() != 3*1024*1024 || info.Mode().Perm() != 0o750 {
		t.Fatalf("copied file size/mode = %d/%#o", info.Size(), info.Mode().Perm())
	}
}

func TestComposeFileReadsAreBounded(t *testing.T) {
	worktree := t.TempDir()
	path := filepath.Join(worktree, "compose.yml")
	content := append([]byte("services:\n  web:\n    image: test\n"), bytes.Repeat([]byte("x"), composeFileReadLimit+1024)...)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write compose: %v", err)
	}

	raw, ok := readCompose(worktree)
	if !ok {
		t.Fatalf("expected compose file to be read")
	}
	if !strings.Contains(raw, "[truncated]") || len(raw) > composeFileReadLimit+64 {
		t.Fatalf("compose content was not bounded: len=%d", len(raw))
	}
	services := parseComposeServicesFromFile(path)
	if len(services) == 0 || services[0]["name"] != "web" {
		t.Fatalf("expected bounded parser to keep early service, got %#v", services)
	}
}

func TestRunStartupScriptBoundsFailureOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("startup scripts use POSIX sh")
	}
	script := "i=0; while [ $i -lt 70000 ]; do printf x; i=$((i+1)); done; exit 1"
	warnings := runStartupScript(context.Background(), t.TempDir(), "chapter-1", script)
	if len(warnings) != 1 || !strings.HasPrefix(warnings[0], "Startup script failed:") {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
	if len(warnings[0]) > 360 {
		t.Fatalf("startup warning should stay bounded, got len=%d", len(warnings[0]))
	}
}

func TestAllocateContainerPortsReleasesExistingChapterPorts(t *testing.T) {
	database := openContainerRuntimeDB(t)
	service := &Service{DB: database}
	ctx := context.Background()
	if _, err := database.Exec(`INSERT INTO port_allocations (port, chapter_id, service_name, allocated_at) VALUES (10000, 'other', 'api', 'now')`); err != nil {
		t.Fatalf("seed port allocation: %v", err)
	}
	first, err := service.allocateContainerPorts(ctx, "chapter-1", []containerPortConfig{{ContainerPort: 3000, ServiceName: "web"}, {ContainerPort: 5432, ServiceName: "db"}})
	if err != nil {
		t.Fatalf("allocate first: %v", err)
	}
	if intValue(first[0]["hostPort"]) != 10001 || intValue(first[1]["hostPort"]) != 10002 {
		t.Fatalf("first mappings = %#v, want 10001/10002", first)
	}
	second, err := service.allocateContainerPorts(ctx, "chapter-1", []containerPortConfig{{ContainerPort: 8080, ServiceName: "web"}})
	if err != nil {
		t.Fatalf("allocate second: %v", err)
	}
	if intValue(second[0]["hostPort"]) != 10001 {
		t.Fatalf("second mapping = %#v, want released chapter port reused at 10001", second)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM port_allocations WHERE chapter_id = 'chapter-1'`).Scan(&count); err != nil {
		t.Fatalf("count allocations: %v", err)
	}
	if count != 1 {
		t.Fatalf("chapter allocations = %d, want 1", count)
	}
}

func TestContainerStartProxyModeRecordsProxyMetadataWithFakePodman(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	database := openContainerRuntimeDB(t)
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "compose.yml"), []byte("services:\n  web:\n    image: test\n    ports:\n      - \"8080:8080\"\n"), 0o644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	settings := loadTempSettings(t)
	if _, err := settings.Patch(map[string]any{"containers": map[string]any{"proxy": map[string]any{"enabled": true, "port": 7780}}}); err != nil {
		t.Fatalf("patch settings: %v", err)
	}
	insertContainerRuntimeProjectAndChapter(t, database, worktree, `{"ports":[{"containerPort":8080,"serviceName":"web"}]}`, "dev.example.test")
	prependFakePodman(t, fakePodmanScript())

	handler := NewHandler(&Service{DB: database, Settings: settings})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/c1/containers/start", nil)
	router := newChapterRouter(handler)
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["ok"] != true {
		t.Fatalf("start response = %#v, want ok", body)
	}
	eventually(t, func() bool {
		var count int
		return database.QueryRow(`SELECT COUNT(*) FROM container_instances WHERE chapter_id = 'c1'`).Scan(&count) == nil && count > 0
	}, "proxy container instance persisted")
	var serviceName, proxyLabel, containerIP string
	var hostPort sql.NullInt64
	var containerPort int
	if err := database.QueryRow(`SELECT service_name, host_port, container_port, proxy_label, container_ip FROM container_instances WHERE chapter_id = 'c1'`).Scan(&serviceName, &hostPort, &containerPort, &proxyLabel, &containerIP); err != nil {
		t.Fatalf("query container instance: %v", err)
	}
	if serviceName != "web" || hostPort.Valid || containerPort != 8080 || proxyLabel != "c1-web-8080" || containerIP != "10.88.0.2" {
		t.Fatalf("proxy row = service=%s host=%v container=%d label=%s ip=%s", serviceName, hostPort, containerPort, proxyLabel, containerIP)
	}
	var allocations int
	if err := database.QueryRow(`SELECT COUNT(*) FROM port_allocations WHERE chapter_id = 'c1'`).Scan(&allocations); err != nil {
		t.Fatalf("count allocations: %v", err)
	}
	if allocations != 0 {
		t.Fatalf("proxy mode allocations = %d, want 0", allocations)
	}
}

func TestPersistContainerInstancesReturnsPortCleanupError(t *testing.T) {
	database := openContainerRuntimeDB(t)
	if _, err := database.Exec(`INSERT INTO port_allocations (port, chapter_id, service_name, allocated_at) VALUES (10000, 'c1', 'web', 'now')`); err != nil {
		t.Fatalf("seed port allocation: %v", err)
	}
	if _, err := database.Exec(`CREATE TRIGGER fail_port_cleanup BEFORE DELETE ON port_allocations WHEN OLD.chapter_id = 'c1' BEGIN SELECT RAISE(ABORT, 'port cleanup blocked'); END;`); err != nil {
		t.Fatalf("create cleanup trigger: %v", err)
	}
	handler := NewHandler(&Service{DB: database})

	err := handler.persistContainerInstances(context.Background(), "c1", []map[string]any{{"id": "container-1", "serviceName": "web", "status": "running", "containerPort": 8080}})
	if err == nil || !strings.Contains(err.Error(), "port cleanup blocked") {
		t.Fatalf("expected port cleanup error, got %v", err)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM container_instances WHERE chapter_id = 'c1'`).Scan(&count); err != nil {
		t.Fatalf("count container instances: %v", err)
	}
	if count != 0 {
		t.Fatalf("port cleanup failure should not persist container instances, count=%d", count)
	}
}

func TestContainerActionBroadcastsContainerStatusChanges(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	database := openContainerRuntimeDB(t)
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "compose.yml"), []byte("services:\n  web:\n    image: test\n    ports:\n      - \"8080:8080\"\n"), 0o644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	insertContainerRuntimeProjectAndChapter(t, database, worktree, `{"ports":[{"containerPort":8080,"serviceName":"web"}]}`, "")
	if _, err := database.Exec(`INSERT INTO narrators (id, chapter_id, variant, created_at) VALUES ('nar-1', 'c1', 'primary', 'now')`); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	prependFakePodman(t, fakePodmanScript())
	recorder := &chapterBroadcastRecorder{}
	handler := NewHandler(&Service{DB: database}, recorder)

	recorderHTTP := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/c1/containers/start", nil)
	newChapterRouter(handler).ServeHTTP(recorderHTTP, req)
	if recorderHTTP.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorderHTTP.Code, recorderHTTP.Body.String())
	}

	eventually(t, func() bool {
		return recorder.hasNarratorEvent("nar-1", "container_status_changed", "starting", true)
	}, "starting container_status_changed event")
	eventually(t, func() bool {
		return recorder.hasNarratorEvent("nar-1", "container_status_changed", "running", true)
	}, "running container_status_changed event")
	eventually(t, func() bool {
		return recorder.hasGlobalEvent("container:starting") && recorder.hasGlobalEvent("container:started")
	}, "container starting/started events")
	eventually(t, func() bool {
		return recorder.hasContainerLogPhase("build") && recorder.hasContainerLogPhase("start")
	}, "streamed build/start container logs")
}

func TestContainerStartReturnsBeforeComposeUpCompletes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake uses POSIX sh")
	}
	database := openContainerRuntimeDB(t)
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "compose.yml"), []byte("services:\n  web:\n    image: test\n    ports:\n      - \"8080:8080\"\n"), 0o644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	insertContainerRuntimeProjectAndChapter(t, database, worktree, `{"ports":[{"containerPort":8080,"serviceName":"web"}]}`, "")
	if _, err := database.Exec(`INSERT INTO narrators (id, chapter_id, variant, created_at) VALUES ('nar-1', 'c1', 'primary', 'now')`); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	releasePath := prependBlockingPodman(t)
	recorder := &chapterBroadcastRecorder{}
	handler := NewHandler(&Service{DB: database}, recorder)

	started := time.Now()
	recorderHTTP := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/c1/containers/start", nil)
	newChapterRouter(handler).ServeHTTP(recorderHTTP, req)
	if recorderHTTP.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorderHTTP.Code, recorderHTTP.Body.String())
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("container start did not return promptly, elapsed=%s", elapsed)
	}
	var body map[string]any
	if err := json.Unmarshal(recorderHTTP.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["ok"] != true || body["status"] != "starting" {
		t.Fatalf("background start response = %#v, want ok starting", body)
	}
	eventually(t, func() bool {
		return recorder.hasGlobalEvent("container:starting") && recorder.hasContainerLogPhase("build")
	}, "background start emits starting/build log before compose finishes")
	if recorder.hasGlobalEvent("container:started") {
		t.Fatalf("container should not be marked started before fake compose release: %#v", recorder.snapshotGlobalEvents())
	}
	if err := os.WriteFile(releasePath, []byte("go"), 0o644); err != nil {
		t.Fatalf("release fake podman: %v", err)
	}
	eventually(t, func() bool {
		return recorder.hasGlobalEvent("container:started") && recorder.hasContainerLogPhase("start") && recorder.hasNarratorEvent("nar-1", "container_status_changed", "running", true)
	}, "background start completes after release")
}

func TestChapterContainerLockSerializesSameChapterOnly(t *testing.T) {
	chapterID := "lock-" + strings.ReplaceAll(t.Name(), "/", "-")
	enteredFirst := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondEntered := make(chan struct{})
	errCh := make(chan error, 2)

	go func() {
		errCh <- withChapterContainerLock(chapterID, func() error {
			close(enteredFirst)
			<-releaseFirst
			return nil
		})
	}()
	<-enteredFirst
	go func() {
		errCh <- withChapterContainerLock(chapterID, func() error {
			close(secondEntered)
			return nil
		})
	}()
	select {
	case <-secondEntered:
		t.Fatalf("same chapter lock allowed concurrent entry")
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseFirst)
	select {
	case <-secondEntered:
	case <-time.After(time.Second):
		t.Fatalf("second waiter did not acquire lock after release")
	}
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("lock error: %v", err)
		}
	}

	ready := make(chan string, 2)
	release := make(chan struct{})
	for _, id := range []string{chapterID + "-a", chapterID + "-b"} {
		id := id
		go func() {
			_ = withChapterContainerLock(id, func() error {
				ready <- id
				<-release
				return nil
			})
		}()
	}
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatalf("first different-chapter lock did not enter")
	}
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatalf("different chapter locks should run concurrently")
	}
	close(release)
}

func TestDormantPausesContainersAndBroadcasts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake podman script uses POSIX sh")
	}
	database := openContainerRuntimeDB(t)
	repo, worktree := newContainerLifecycleWorktree(t, true)
	insertContainerLifecycleChapter(t, database, repo, worktree, "active", `{"ports":[{"containerPort":8080,"serviceName":"web"}]}`)
	if _, err := database.Exec(`INSERT INTO container_instances (id, chapter_id, service_name, status, host_port, container_port, created_at, updated_at) VALUES ('container-1', 'c1', 'web', 'running', 18080, 8080, 'now', 'now')`); err != nil {
		t.Fatalf("seed container instance: %v", err)
	}
	logPath := prependRecordingPodman(t)
	recorder := &chapterBroadcastRecorder{}
	service := &Service{DB: database, Git: gitx.NewService(), BroadcastMergeProgress: recorder.BroadcastMergeProgress}

	if err := service.Dormant(context.Background(), "c1"); err != nil {
		t.Fatalf("Dormant: %v", err)
	}
	var status string
	var worktreePath sql.NullString
	if err := database.QueryRow(`SELECT status, worktree_path FROM chapters WHERE id = 'c1'`).Scan(&status, &worktreePath); err != nil {
		t.Fatalf("query chapter: %v", err)
	}
	if status != "dormant" || worktreePath.Valid {
		t.Fatalf("chapter status/worktree = %s/%v, want dormant/null", status, worktreePath)
	}
	var containerStatus string
	if err := database.QueryRow(`SELECT status FROM container_instances WHERE id = 'container-1'`).Scan(&containerStatus); err != nil {
		t.Fatalf("query container status: %v", err)
	}
	if containerStatus != "paused" {
		t.Fatalf("container status = %s, want paused", containerStatus)
	}
	commands := readTextFile(t, logPath)
	if !strings.Contains(commands, " pause") {
		t.Fatalf("podman commands did not include pause:\n%s", commands)
	}
	if !recorder.hasGlobalEvent("container:paused") || !recorder.hasGlobalEvent("chapter:dormant") {
		t.Fatalf("missing dormant lifecycle events: %#v", recorder.globalEvents)
	}
}

func TestWakeUnpausesContainersAndBroadcasts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake podman script uses POSIX sh")
	}
	database := openContainerRuntimeDB(t)
	repo, worktree := newContainerLifecycleWorktree(t, false)
	insertContainerLifecycleChapter(t, database, repo, "", "dormant", `{"ports":[{"containerPort":8080,"serviceName":"web"}]}`)
	if _, err := database.Exec(`INSERT INTO container_instances (id, chapter_id, service_name, status, container_port, created_at, updated_at) VALUES ('container-1', 'c1', 'web', 'paused', 8080, 'now', 'now')`); err != nil {
		t.Fatalf("seed paused container: %v", err)
	}
	logPath := prependRecordingPodman(t)
	recorder := &chapterBroadcastRecorder{}
	service := &Service{DB: database, Git: gitx.NewService(), BroadcastMergeProgress: recorder.BroadcastMergeProgress}

	if err := service.Wake(context.Background(), "c1"); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("expected wake to recreate worktree %s: %v", worktree, err)
	}
	commands := readTextFile(t, logPath)
	if !strings.Contains(commands, " unpause") || strings.Contains(commands, " up -d") {
		t.Fatalf("unpause success should not fresh-start containers:\n%s", commands)
	}
	var containerStatus string
	if err := database.QueryRow(`SELECT status FROM container_instances WHERE id = 'container-1'`).Scan(&containerStatus); err != nil {
		t.Fatalf("query unpaused container: %v", err)
	}
	if containerStatus != "running" {
		t.Fatalf("container status after wake = %s, want running", containerStatus)
	}
	if !recorder.hasGlobalEvent("container:resumed") || !recorder.hasGlobalEvent("chapter:woken") {
		t.Fatalf("missing wake lifecycle events: %#v", recorder.globalEvents)
	}
}

func TestDormantWakeRealGitWorktreeRemoveRecreatePreservesBranchFiles(t *testing.T) {
	database := openContainerRuntimeDB(t)
	createChapterCommitTableForCreateLifecycle(t, database)
	repo := newForkParityRepo(t)
	branch := "chapter/realistic-dormant-wake"
	worktree := filepath.Join(repo, ".worktrees", "realistic-dormant-wake")
	runGit(t, repo, "branch", branch)
	runGit(t, repo, "worktree", "add", worktree, branch)
	if err := os.MkdirAll(filepath.Join(worktree, "pkg", "feature"), 0o755); err != nil {
		t.Fatalf("mkdir realistic worktree content: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "pkg", "feature", "story.txt"), []byte("chapter content\n"), 0o644); err != nil {
		t.Fatalf("write realistic worktree content: %v", err)
	}
	runGit(t, worktree, "add", ".")
	runGit(t, worktree, "commit", "-m", "add realistic dormant wake content")
	branchHead := strings.TrimSpace(runGitChapterTest(t, worktree, "rev-parse", "HEAD"))
	commitCount := gitCommitCountForTest(t, worktree)
	if _, err := database.Exec(`INSERT INTO projects (id, name, git_path, default_branch, created_at, updated_at) VALUES ('p1', 'Project', ?, 'master', 'now', 'now')`, repo); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, head_commit_sha, commit_count, is_root, pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at) VALUES ('c1', 'p1', 'Realistic Dormant Wake', 'active', 'branch', ?, ?, 'master', ?, ?, 0, 0, 0, 0, 0, 'now', 'now')`, branch, worktree, branchHead, commitCount); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
	service := &Service{DB: database, Git: gitx.NewService()}

	if err := service.Dormant(context.Background(), "c1"); err != nil {
		t.Fatalf("Dormant: %v", err)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("dormant should remove worktree %s, stat err=%v", worktree, err)
	}
	if head := strings.TrimSpace(runGitChapterTest(t, repo, "rev-parse", branch)); head != branchHead {
		t.Fatalf("branch head after dormant = %s, want %s", head, branchHead)
	}
	var dormantStatus string
	var dormantWorktree sql.NullString
	if err := database.QueryRow(`SELECT status, worktree_path FROM chapters WHERE id = 'c1'`).Scan(&dormantStatus, &dormantWorktree); err != nil {
		t.Fatalf("query dormant chapter: %v", err)
	}
	if dormantStatus != "dormant" || dormantWorktree.Valid {
		t.Fatalf("chapter after dormant = %s/%#v, want dormant/null", dormantStatus, dormantWorktree)
	}

	if err := service.Wake(context.Background(), "c1"); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktree, "pkg", "feature", "story.txt")); err != nil {
		t.Fatalf("wake should recreate tracked nested file: %v", err)
	}
	if content, err := os.ReadFile(filepath.Join(worktree, "pkg", "feature", "story.txt")); err != nil || string(content) != "chapter content\n" {
		t.Fatalf("recreated nested file content = %q err=%v", string(content), err)
	}
	if head := strings.TrimSpace(runGitChapterTest(t, worktree, "rev-parse", "HEAD")); head != branchHead {
		t.Fatalf("worktree head after wake = %s, want %s", head, branchHead)
	}
	if status := strings.TrimSpace(runGitChapterTest(t, worktree, "status", "--porcelain")); status != "" {
		t.Fatalf("wake recreated worktree should be clean, status=%q", status)
	}
	var activeStatus string
	var activeWorktree string
	var syncedHead sql.NullString
	var syncedCount int
	if err := database.QueryRow(`SELECT status, worktree_path, head_commit_sha, commit_count FROM chapters WHERE id = 'c1'`).Scan(&activeStatus, &activeWorktree, &syncedHead, &syncedCount); err != nil {
		t.Fatalf("query woken chapter: %v", err)
	}
	if activeStatus != "active" || activeWorktree != worktree || !syncedHead.Valid || syncedHead.String != branchHead || syncedCount != commitCount {
		t.Fatalf("chapter after wake = status:%s worktree:%s head:%#v count:%d", activeStatus, activeWorktree, syncedHead, syncedCount)
	}
	var commitRows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM chapter_commits WHERE chapter_id = 'c1'`).Scan(&commitRows); err != nil {
		t.Fatalf("count wake-synced commits: %v", err)
	}
	if commitRows != commitCount {
		t.Fatalf("wake-synced commits = %d, want %d", commitRows, commitCount)
	}
}

func TestWakeSyncsCommitsAfterWokenBroadcast(t *testing.T) {
	database := openContainerRuntimeDB(t)
	createChapterCommitTableForCreateLifecycle(t, database)
	repo, worktree := newContainerLifecycleWorktree(t, false)
	insertContainerLifecycleChapter(t, database, repo, "", "dormant", `{}`)
	recorder := &chapterBroadcastRecorder{}
	service := &Service{
		DB:                     database,
		Git:                    gitx.NewService(),
		BroadcastMergeProgress: recorder.BroadcastMergeProgress,
	}

	if err := service.Wake(context.Background(), "c1"); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if !recorder.hasGlobalEvent("chapter:woken") {
		t.Fatalf("missing woken lifecycle event before/with commit sync: %#v", recorder.globalEvents)
	}
	expectedHead := strings.TrimSpace(runGitChapterTest(t, repo, "rev-parse", "chapter/test"))
	var message, source string
	if err := database.QueryRow(`SELECT message, source FROM chapter_commits WHERE chapter_id = 'c1' AND sha = ?`, expectedHead).Scan(&message, &source); err != nil {
		t.Fatalf("query wake-synced head commit: %v", err)
	}
	if message != "add compose" || source != "manual" {
		t.Fatalf("wake-synced commit fields = message:%q source:%q", message, source)
	}
	var head sql.NullString
	var count int
	if err := database.QueryRow(`SELECT head_commit_sha, commit_count FROM chapters WHERE id = 'c1'`).Scan(&head, &count); err != nil {
		t.Fatalf("query wake-synced chapter metadata: %v", err)
	}
	expectedCount := gitCommitCountForTest(t, worktree)
	if !head.Valid || head.String != expectedHead || count != expectedCount {
		t.Fatalf("wake-synced chapter metadata = head:%#v count:%d, want %s/%d", head, count, expectedHead, expectedCount)
	}
}

func TestWakeCommitSyncFailureIsNonFatalAfterWokenBroadcast(t *testing.T) {
	database := openContainerRuntimeDB(t)
	createChapterCommitTableForCreateLifecycle(t, database)
	if _, err := database.Exec(`CREATE TRIGGER fail_wake_commit_sync BEFORE INSERT ON chapter_commits BEGIN SELECT RAISE(ABORT, 'wake commit sync blocked'); END;`); err != nil {
		t.Fatalf("create sync failure trigger: %v", err)
	}
	repo, worktree := newContainerLifecycleWorktree(t, false)
	insertContainerLifecycleChapter(t, database, repo, "", "dormant", `{}`)
	recorder := &chapterBroadcastRecorder{}
	service := &Service{
		DB:                     database,
		Git:                    gitx.NewService(),
		BroadcastMergeProgress: recorder.BroadcastMergeProgress,
	}

	if err := service.Wake(context.Background(), "c1"); err != nil {
		t.Fatalf("Wake should survive commit sync failure: %v", err)
	}
	if !recorder.hasGlobalEvent("chapter:woken") {
		t.Fatalf("missing woken lifecycle event after sync failure: %#v", recorder.globalEvents)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("wake should still recreate worktree after sync failure: %v", err)
	}
	var status string
	var worktreePath sql.NullString
	if err := database.QueryRow(`SELECT status, worktree_path FROM chapters WHERE id = 'c1'`).Scan(&status, &worktreePath); err != nil {
		t.Fatalf("query chapter after sync failure: %v", err)
	}
	if status != "active" || !worktreePath.Valid || worktreePath.String != worktree {
		t.Fatalf("chapter after sync failure = %s/%#v, want active/%s", status, worktreePath, worktree)
	}
	var rows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM chapter_commits WHERE chapter_id = 'c1'`).Scan(&rows); err != nil {
		t.Fatalf("count synced commits after failure: %v", err)
	}
	if rows != 0 {
		t.Fatalf("commit rows after sync failure = %d, want 0", rows)
	}
}

func TestDormantRoutePausesContainersAndReturnsUpdatedChapter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake podman script uses POSIX sh")
	}
	database := openContainerRuntimeDB(t)
	repo, worktree := newContainerLifecycleWorktree(t, true)
	insertContainerLifecycleChapter(t, database, repo, worktree, "active", `{"ports":[{"containerPort":8080,"serviceName":"web"}]}`)
	if _, err := database.Exec(`INSERT INTO container_instances (id, chapter_id, service_name, status, host_port, container_port, created_at, updated_at) VALUES ('container-1', 'c1', 'web', 'running', 18080, 8080, 'now', 'now')`); err != nil {
		t.Fatalf("seed container instance: %v", err)
	}
	logPath := prependRecordingPodman(t)
	recorder := &chapterBroadcastRecorder{}
	service := &Service{DB: database, Git: gitx.NewService(), BroadcastMergeProgress: recorder.BroadcastMergeProgress}
	handler := NewHandler(service, recorder)

	recorderHTTP := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/c1/dormant", nil)
	newChapterRouter(handler).ServeHTTP(recorderHTTP, req)
	if recorderHTTP.Code != http.StatusOK {
		t.Fatalf("dormant route status = %d body=%s", recorderHTTP.Code, recorderHTTP.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorderHTTP.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode dormant response: %v", err)
	}
	if body["status"] != "dormant" || body["worktreePath"] != nil {
		t.Fatalf("dormant route body = %#v, want dormant/null worktree", body)
	}
	var containerStatus string
	if err := database.QueryRow(`SELECT status FROM container_instances WHERE id = 'container-1'`).Scan(&containerStatus); err != nil {
		t.Fatalf("query route container status: %v", err)
	}
	if containerStatus != "paused" {
		t.Fatalf("route container status = %s, want paused", containerStatus)
	}
	commands := readTextFile(t, logPath)
	if !strings.Contains(commands, " pause") {
		t.Fatalf("dormant route podman commands did not include pause:\n%s", commands)
	}
	if !recorder.hasGlobalEvent("container:paused") || !recorder.hasGlobalEvent("chapter:dormant") {
		t.Fatalf("dormant route missing lifecycle events: %#v", recorder.globalEvents)
	}
}

func TestWakeRouteUnpausesContainersAndReturnsUpdatedChapter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake podman script uses POSIX sh")
	}
	database := openContainerRuntimeDB(t)
	repo, worktree := newContainerLifecycleWorktree(t, false)
	insertContainerLifecycleChapter(t, database, repo, "", "dormant", `{"ports":[{"containerPort":8080,"serviceName":"web"}]}`)
	if _, err := database.Exec(`INSERT INTO container_instances (id, chapter_id, service_name, status, container_port, created_at, updated_at) VALUES ('container-1', 'c1', 'web', 'paused', 8080, 'now', 'now')`); err != nil {
		t.Fatalf("seed paused container: %v", err)
	}
	logPath := prependRecordingPodman(t)
	recorder := &chapterBroadcastRecorder{}
	service := &Service{DB: database, Git: gitx.NewService(), BroadcastMergeProgress: recorder.BroadcastMergeProgress}
	handler := NewHandler(service, recorder)

	recorderHTTP := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/c1/wake", nil)
	newChapterRouter(handler).ServeHTTP(recorderHTTP, req)
	if recorderHTTP.Code != http.StatusOK {
		t.Fatalf("wake route status = %d body=%s", recorderHTTP.Code, recorderHTTP.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorderHTTP.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode wake response: %v", err)
	}
	if body["status"] != "active" || body["worktreePath"] != worktree {
		t.Fatalf("wake route body = %#v, want active/%s", body, worktree)
	}
	commands := readTextFile(t, logPath)
	if !strings.Contains(commands, " unpause") || strings.Contains(commands, " up -d") {
		t.Fatalf("wake route unpause success should not fresh-start containers:\n%s", commands)
	}
	var containerStatus string
	if err := database.QueryRow(`SELECT status FROM container_instances WHERE id = 'container-1'`).Scan(&containerStatus); err != nil {
		t.Fatalf("query route unpaused container: %v", err)
	}
	if containerStatus != "running" {
		t.Fatalf("route container status after wake = %s, want running", containerStatus)
	}
	if !recorder.hasGlobalEvent("container:resumed") || !recorder.hasGlobalEvent("chapter:woken") {
		t.Fatalf("wake route missing lifecycle events: %#v", recorder.globalEvents)
	}
}

func TestWakeUnpauseFailureFallsBackToFreshStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake podman script uses POSIX sh")
	}
	database := openContainerRuntimeDB(t)
	repo, worktree := newContainerLifecycleWorktree(t, false)
	insertContainerLifecycleChapter(t, database, repo, "", "dormant", `{"ports":[{"containerPort":8080,"serviceName":"web"}]}`)
	if _, err := database.Exec(`INSERT INTO container_instances (id, chapter_id, service_name, status, container_port, created_at, updated_at) VALUES ('container-1', 'c1', 'web', 'paused', 8080, 'now', 'now')`); err != nil {
		t.Fatalf("seed paused container: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO port_allocations (port, chapter_id, service_name, allocated_at) VALUES (18080, 'c1', 'web', 'now')`); err != nil {
		t.Fatalf("seed port allocation: %v", err)
	}
	t.Setenv("NARRAFORK_FAIL_UNPAUSE", "1")
	logPath := prependRecordingPodman(t)
	recorder := &chapterBroadcastRecorder{}
	service := &Service{DB: database, Git: gitx.NewService(), BroadcastMergeProgress: recorder.BroadcastMergeProgress}

	if err := service.Wake(context.Background(), "c1"); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("expected wake to recreate worktree %s: %v", worktree, err)
	}
	commands := readTextFile(t, logPath)
	for _, want := range []string{" unpause", " down --remove-orphans", " up -d"} {
		if !strings.Contains(commands, want) {
			t.Fatalf("podman commands missing %q:\n%s", want, commands)
		}
	}
	var containerStatus string
	if err := database.QueryRow(`SELECT status FROM container_instances WHERE chapter_id = 'c1'`).Scan(&containerStatus); err != nil {
		t.Fatalf("query restarted container: %v", err)
	}
	if containerStatus != "running" {
		t.Fatalf("container status after fallback = %s, want running", containerStatus)
	}
	var allocations int
	if err := database.QueryRow(`SELECT COUNT(*) FROM port_allocations WHERE chapter_id = 'c1'`).Scan(&allocations); err != nil {
		t.Fatalf("count allocations: %v", err)
	}
	if allocations == 0 {
		t.Fatalf("fresh start fallback should restore port allocation")
	}
	if !recorder.hasGlobalEvent("container:started") || !recorder.hasGlobalEvent("chapter:woken") {
		t.Fatalf("missing wake fallback lifecycle events: %#v", recorder.globalEvents)
	}
}

func TestCleanupRemovesOrphanContainerRecordsWhenComposeDownFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake podman script uses POSIX sh")
	}
	database := openContainerRuntimeDB(t)
	repo, worktree := newContainerLifecycleWorktree(t, true)
	insertContainerLifecycleChapter(t, database, repo, worktree, "active", `{}`)
	if _, err := database.Exec(`INSERT INTO container_instances (id, chapter_id, service_name, status, host_port, container_port, created_at, updated_at) VALUES ('container-1', 'c1', 'web', 'running', 18080, 8080, 'now', 'now')`); err != nil {
		t.Fatalf("seed container instance: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO port_allocations (port, chapter_id, service_name, allocated_at) VALUES (18080, 'c1', 'web', 'now')`); err != nil {
		t.Fatalf("seed port allocation: %v", err)
	}
	t.Setenv("NARRAFORK_FAIL_DOWN", "1")
	logPath := prependRecordingPodman(t)
	recorder := &chapterBroadcastRecorder{}
	service := &Service{DB: database, Git: gitx.NewService(), BroadcastMergeProgress: recorder.BroadcastMergeProgress}

	chapter, err := service.Get(context.Background(), "c1")
	if err != nil {
		t.Fatalf("get chapter: %v", err)
	}
	if err := service.cleanupChapterResources(context.Background(), chapter, true); err != nil {
		t.Fatalf("cleanupChapterResources: %v", err)
	}
	commands := readTextFile(t, logPath)
	if !strings.Contains(commands, " down --remove-orphans -v") {
		t.Fatalf("podman commands missing down -v:\n%s", commands)
	}
	assertContainerRuntimeCount(t, database, `SELECT COUNT(*) FROM container_instances WHERE chapter_id = 'c1'`, 0)
	assertContainerRuntimeCount(t, database, `SELECT COUNT(*) FROM port_allocations WHERE chapter_id = 'c1'`, 0)
	if !recorder.hasGlobalEvent("container:stopped") || !recorder.hasGlobalEvent("chapter:abandoned") {
		t.Fatalf("missing cleanup lifecycle events: %#v", recorder.globalEvents)
	}
}

func TestContainerReadyNarratorIntegrationLeavesBrowserDisabledByDefault(t *testing.T) {
	database := openContainerRuntimeDB(t)
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "compose.yml"), []byte("services:\n  web:\n    image: test\n"), 0o644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	insertContainerRuntimeProjectAndChapter(t, database, worktree, `{}`, "")
	if _, err := database.Exec(`INSERT INTO narrators (id, chapter_id, variant, enabled_tools, created_at, updated_at) VALUES ('nar-1', 'c1', 'primary', '[]', 'now', 'now')`); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO container_instances (id, chapter_id, service_name, status, host_port, container_port, created_at, updated_at) VALUES ('container-1', 'c1', 'web', 'running', 18080, 8080, 'now', 'now')`); err != nil {
		t.Fatalf("insert container instance: %v", err)
	}
	service := &Service{DB: database}
	if err := service.handleContainerReadyNarratorIntegration(context.Background(), "c1"); err != nil {
		t.Fatalf("handleContainerReadyNarratorIntegration: %v", err)
	}
	var enabledTools string
	if err := database.QueryRow(`SELECT enabled_tools FROM narrators WHERE id = 'nar-1'`).Scan(&enabledTools); err != nil {
		t.Fatalf("load enabled tools: %v", err)
	}
	var decodedTools []string
	if err := json.Unmarshal([]byte(enabledTools), &decodedTools); err != nil {
		t.Fatalf("decode enabled tools: %v", err)
	}
	if len(decodedTools) != 0 {
		t.Fatalf("enabled tools should stay empty by default, got %#v", decodedTools)
	}
	var contentJSON string
	if err := database.QueryRow(`SELECT content_json FROM narrator_messages WHERE narrator_id = 'nar-1' ORDER BY created_at DESC LIMIT 1`).Scan(&contentJSON); err != nil {
		t.Fatalf("load ready message: %v", err)
	}
	if !strings.Contains(contentJSON, "browserAutoEnabled\":false") {
		t.Fatalf("ready message should record browserAutoEnabled=false by default: %s", contentJSON)
	}
}

func TestContainerReadyNarratorIntegrationEnablesBrowserAndInjectsMessage(t *testing.T) {
	database := openContainerRuntimeDB(t)
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "compose.yml"), []byte("services:\n  web:\n    image: test\n"), 0o644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	insertContainerRuntimeProjectAndChapter(t, database, worktree, `{}`, "")
	if _, err := database.Exec(`INSERT INTO narrators (id, chapter_id, variant, enabled_tools, created_at, updated_at) VALUES ('nar-1', 'c1', 'primary', '[]', 'now', 'now')`); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO container_instances (id, chapter_id, service_name, status, host_port, container_port, created_at, updated_at) VALUES ('container-1', 'c1', 'web', 'running', 18080, 8080, 'now', 'now')`); err != nil {
		t.Fatalf("insert container instance: %v", err)
	}
	service := &Service{DB: database, BrowserToolAutoEnableEnabled: true}
	if err := service.handleContainerReadyNarratorIntegration(context.Background(), "c1"); err != nil {
		t.Fatalf("handleContainerReadyNarratorIntegration: %v", err)
	}
	var enabledTools string
	if err := database.QueryRow(`SELECT enabled_tools FROM narrators WHERE id = 'nar-1'`).Scan(&enabledTools); err != nil {
		t.Fatalf("load enabled tools: %v", err)
	}
	var decodedTools []string
	if err := json.Unmarshal([]byte(enabledTools), &decodedTools); err != nil {
		t.Fatalf("decode enabled tools: %v", err)
	}
	if len(decodedTools) != 1 || decodedTools[0] != "Browser" {
		t.Fatalf("enabled tools = %#v, want Browser", decodedTools)
	}
	var role, contentText, contentJSON string
	if err := database.QueryRow(`SELECT role, content_text, content_json FROM narrator_messages WHERE narrator_id = 'nar-1' ORDER BY created_at DESC LIMIT 1`).Scan(&role, &contentText, &contentJSON); err != nil {
		t.Fatalf("load ready message: %v", err)
	}
	if role != "system" || !strings.Contains(contentText, "Container services are now running") || !strings.Contains(contentText, "http://localhost:18080") {
		t.Fatalf("ready message role/text mismatch role=%s text=%q", role, contentText)
	}
	if !strings.Contains(contentJSON, "container_ready") || !strings.Contains(contentJSON, "browserAutoEnabled") || !strings.Contains(contentJSON, "http://localhost:18080") {
		t.Fatalf("ready message content json missing structured payload: %s", contentJSON)
	}
	var refCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM narrator_message_refs WHERE narrator_id = 'nar-1'`).Scan(&refCount); err != nil {
		t.Fatalf("count ready refs: %v", err)
	}
	if refCount != 1 {
		t.Fatalf("ready ref count = %d, want 1", refCount)
	}
}

func TestPersistContainerReadyMessageAppendsSystemMessageAfterExistingRefs(t *testing.T) {
	database := openContainerRuntimeDB(t)
	if _, err := database.Exec(`INSERT INTO narrators (id, chapter_id, variant, enabled_tools, created_at, updated_at) VALUES ('nar-1', 'c1', 'primary', '[]', 'now', 'now')`); err != nil {
		t.Fatalf("insert narrator: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO narrator_messages (id, narrator_id, role, content_json, content_text, created_at) VALUES ('msg-existing', 'nar-1', 'user', '[{"type":"text","text":"existing"}]', 'existing', 'now')`); err != nil {
		t.Fatalf("insert existing message: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO narrator_message_refs (id, narrator_id, message_id, seq, is_compact) VALUES ('ref-existing', 'nar-1', 'msg-existing', 41, 0)`); err != nil {
		t.Fatalf("insert existing ref: %v", err)
	}
	service := &Service{DB: database}

	err := service.persistContainerReadyMessage(context.Background(), "nar-1", []containerReadyServiceInfo{{ServiceName: "web", URL: "http://localhost:18080", ContainerPort: 8080}}, false)
	if err != nil {
		t.Fatalf("persistContainerReadyMessage: %v", err)
	}
	var role string
	var seq int
	if err := database.QueryRow(`SELECT m.role, r.seq FROM narrator_message_refs r JOIN narrator_messages m ON m.id = r.message_id WHERE r.narrator_id = 'nar-1' AND m.content_json LIKE '%container_ready%' LIMIT 1`).Scan(&role, &seq); err != nil {
		t.Fatalf("load ready message ref: %v", err)
	}
	if role != "system" || seq != 42 {
		t.Fatalf("ready message role/seq = %s/%d, want system/42", role, seq)
	}
	var messageCount, messageVersion int
	if err := database.QueryRow(`SELECT message_count, message_version FROM narrators WHERE id = 'nar-1'`).Scan(&messageCount, &messageVersion); err != nil {
		t.Fatalf("load narrator counters: %v", err)
	}
	if messageCount != 2 || messageVersion != 1 {
		t.Fatalf("narrator counters = count %d version %d, want 2/1", messageCount, messageVersion)
	}
}

func TestContainerRemoveRouteHonorsDeleteVolumes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake podman script uses POSIX sh")
	}
	database := openContainerRuntimeDB(t)
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "compose.yml"), []byte("services:\n  web:\n    image: test\n"), 0o644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	insertContainerRuntimeProjectAndChapter(t, database, worktree, `{}`, "")
	if _, err := database.Exec(`INSERT INTO container_instances (id, chapter_id, service_name, status, host_port, container_port, created_at, updated_at) VALUES ('container-1', 'c1', 'web', 'running', 18080, 8080, 'now', 'now')`); err != nil {
		t.Fatalf("seed container instance: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO port_allocations (port, chapter_id, service_name, allocated_at) VALUES (18080, 'c1', 'web', 'now')`); err != nil {
		t.Fatalf("seed port allocation: %v", err)
	}
	logPath := prependRecordingPodman(t)
	handler := NewHandler(&Service{DB: database})

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/c1/containers/remove", strings.NewReader(`{"deleteVolumes":true}`))
	req.Header.Set("Content-Type", "application/json")
	newChapterRouter(handler).ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("remove status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	commands := readTextFile(t, logPath)
	if !strings.Contains(commands, " down --remove-orphans -v") {
		t.Fatalf("remove route did not pass -v for deleteVolumes=true:\n%s", commands)
	}
	assertContainerRuntimeCount(t, database, `SELECT COUNT(*) FROM container_instances WHERE chapter_id = 'c1'`, 0)
	assertContainerRuntimeCount(t, database, `SELECT COUNT(*) FROM port_allocations WHERE chapter_id = 'c1'`, 0)
}

func TestContainerLogsCapTailTruncateAndIgnoreUnsafeService(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake podman script uses POSIX sh")
	}
	database := openContainerRuntimeDB(t)
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "compose.yml"), []byte("services:\n  web:\n    image: test\n"), 0o644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	insertContainerRuntimeProjectAndChapter(t, database, worktree, `{}`, "")
	logPath := prependRecordingPodman(t)
	handler := NewHandler(&Service{DB: database})

	recorder := httptest.NewRecorder()
	newChapterRouter(handler).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/c1/containers/logs?tail=999999&service=web", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("logs status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode logs body: %v", err)
	}
	if body["truncated"] != true || !strings.Contains(stringValue(body["logs"]), "[truncated]") {
		t.Fatalf("logs body should be truncated, got %#v", body)
	}
	commands := readTextFile(t, logPath)
	if !strings.Contains(commands, " logs --tail 1000 web") {
		t.Fatalf("logs command did not cap tail/include service:\n%s", commands)
	}

	recorder = httptest.NewRecorder()
	newChapterRouter(handler).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/c1/containers/logs?service=../bad", nil))
	commands = readTextFile(t, logPath)
	last := lastNonEmptyLine(commands)
	if strings.Contains(last, "../bad") {
		t.Fatalf("unsafe service should not be passed to compose, last command=%q", last)
	}
}

type chapterBroadcastEvent struct {
	NarratorID string
	Payload    map[string]any
}

type chapterBroadcastRecorder struct {
	mu             sync.Mutex
	narratorEvents []chapterBroadcastEvent
	globalEvents   []map[string]any
}

func (r *chapterBroadcastRecorder) BroadcastNarrator(narratorID string, payload any) {
	if decoded, ok := payload.(map[string]any); ok {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.narratorEvents = append(r.narratorEvents, chapterBroadcastEvent{NarratorID: narratorID, Payload: decoded})
	}
}

func (r *chapterBroadcastRecorder) BroadcastMergeProgress(payload any) {
	if decoded, ok := payload.(map[string]any); ok {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.globalEvents = append(r.globalEvents, decoded)
	}
}

func (r *chapterBroadcastRecorder) hasNarratorEvent(narratorID, eventType, status string, ok bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, event := range r.narratorEvents {
		if event.NarratorID == narratorID && event.Payload["type"] == eventType && event.Payload["status"] == status && event.Payload["ok"] == ok {
			return true
		}
	}
	return false
}

func (r *chapterBroadcastRecorder) hasGlobalEvent(eventType string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, event := range r.globalEvents {
		if event["type"] == eventType {
			return true
		}
	}
	return false
}

func (r *chapterBroadcastRecorder) hasContainerLogPhase(phase string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, event := range r.globalEvents {
		if event["type"] == "container:log" && event["phase"] == phase {
			return true
		}
	}
	return false
}

func (r *chapterBroadcastRecorder) snapshotGlobalEvents() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := make([]map[string]any, len(r.globalEvents))
	copy(items, r.globalEvents)
	return items
}

func eventually(t *testing.T, cond func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met: %s", message)
}

func TestPodmanStatusMissingOnLinuxRemainsInstallable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("go_backend container management is Linux-only")
	}
	t.Setenv("PATH", t.TempDir())
	handler := NewHandler(&Service{})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/chapters/podman/status", nil)
	if err := handler.podmanStatus(recorder, req); err != nil {
		t.Fatalf("podmanStatus: %v", err)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["installed"] != false || body["supported"] != true || body["fallback"] != true || body["platform"] != "linux" {
		t.Fatalf("missing podman should be installable on Linux, got %#v", body)
	}
	reason, _ := body["reason"].(string)
	if reason != "Podman is not installed or not available on PATH." || body["error"] != reason || body["message"] != reason {
		t.Fatalf("missing podman status should expose reason/error/message, got %#v", body)
	}
}

func TestContainerSetupPodmanMissingExposesReasonedFallback(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	handler := NewHandler(&Service{})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/chapters/container-setup", nil)
	if err := handler.containerSetup(recorder, req); err != nil {
		t.Fatalf("containerSetup: %v", err)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	reason, _ := body["reason"].(string)
	if body["supported"] != false || body["fallback"] != true || reason == "" || body["error"] != reason || body["message"] != reason {
		t.Fatalf("container setup fallback should expose reason/error/message, got %#v", body)
	}
}

func TestPodmanInstallDoesNotReportSuccessWhenCommandDoesNotInstall(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("fake package manager test is Linux-only")
	}
	binDir := t.TempDir()
	for _, name := range []string{"apt-get", "sh"} {
		path := filepath.Join(binDir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}
	t.Setenv("PATH", binDir)
	handler := NewHandler(&Service{})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/chapters/podman/install", nil)
	if err := handler.podmanInstall(recorder, req); err != nil {
		t.Fatalf("podmanInstall: %v", err)
	}
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["ok"] != false || body["code"] != "PODMAN_INSTALL_NOT_VERIFIED" || body["installed"] != false {
		t.Fatalf("install should not report fake success after no-op command: %#v", body)
	}
}

func TestContainerListReturnsArrayWhenPodmanMissing(t *testing.T) {
	database := openContainerRuntimeDB(t)
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "compose.yml"), []byte("services:\n  web:\n    image: test\n"), 0o644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	insertContainerRuntimeProjectAndChapter(t, database, worktree, `{}`, "")
	if _, err := database.Exec(`INSERT INTO container_instances (id, chapter_id, service_name, status, container_port, created_at, updated_at) VALUES ('container-1', 'c1', 'web', 'running', 8080, 'now', 'now')`); err != nil {
		t.Fatalf("insert persisted container: %v", err)
	}
	t.Setenv("PATH", t.TempDir())

	handler := NewHandler(&Service{DB: database})
	recorder := httptest.NewRecorder()
	newChapterRouter(handler).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/c1/containers", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("X-Narrafork-Fallback-Code") != "PODMAN_NOT_FOUND" {
		t.Fatalf("fallback header = %q", recorder.Header().Get("X-Narrafork-Fallback-Code"))
	}
	var body []map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode array body: %v; body=%s", err, recorder.Body.String())
	}
	if len(body) != 1 || body[0]["serviceName"] != "web" || body[0]["status"] != "running" {
		t.Fatalf("container list body = %#v", body)
	}
}

func TestContainerMutationFallbacksDoNotMutateOrBroadcastWhenPodmanMissing(t *testing.T) {
	database := openContainerRuntimeDB(t)
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "compose.yml"), []byte("services:\n  web:\n    image: test\n"), 0o644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	insertContainerRuntimeProjectAndChapter(t, database, worktree, `{"ports":[{"containerPort":8080,"serviceName":"web"}]}`, "")
	if _, err := database.Exec(`INSERT INTO container_instances (id, chapter_id, service_name, status, host_port, container_port, created_at, updated_at) VALUES ('container-1', 'c1', 'web', 'running', 18080, 8080, 'now', 'now')`); err != nil {
		t.Fatalf("insert persisted container: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO port_allocations (port, chapter_id, service_name, allocated_at) VALUES (18080, 'c1', 'web', 'now')`); err != nil {
		t.Fatalf("insert port allocation: %v", err)
	}
	t.Setenv("PATH", t.TempDir())
	recorder := &chapterBroadcastRecorder{}
	router := newChapterRouter(NewHandler(&Service{DB: database}, recorder))

	cases := []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/c1/containers/start"},
		{method: http.MethodPost, path: "/c1/containers/stop"},
		{method: http.MethodPost, path: "/c1/containers/remove"},
		{method: http.MethodGet, path: "/c1/containers/logs"},
	}
	for _, tc := range cases {
		recorderHTTP := httptest.NewRecorder()
		router.ServeHTTP(recorderHTTP, httptest.NewRequest(tc.method, tc.path, nil))
		if recorderHTTP.Code != http.StatusOK {
			t.Fatalf("%s %s status = %d, body = %s", tc.method, tc.path, recorderHTTP.Code, recorderHTTP.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(recorderHTTP.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %s %s body: %v", tc.method, tc.path, err)
		}
		if body["fallback"] != true || body["code"] != "PODMAN_NOT_FOUND" || body["supported"] != false || body["reason"] == "" {
			t.Fatalf("%s %s fallback body = %#v", tc.method, tc.path, body)
		}
	}

	var status string
	if err := database.QueryRow(`SELECT status FROM container_instances WHERE id = 'container-1'`).Scan(&status); err != nil {
		t.Fatalf("query persisted container status: %v", err)
	}
	if status != "running" {
		t.Fatalf("missing-Podman fallbacks should not mutate container status, got %s", status)
	}
	assertContainerRuntimeCount(t, database, `SELECT COUNT(*) FROM container_instances WHERE chapter_id = 'c1'`, 1)
	assertContainerRuntimeCount(t, database, `SELECT COUNT(*) FROM port_allocations WHERE chapter_id = 'c1'`, 1)
	if events := recorder.snapshotGlobalEvents(); len(events) != 0 {
		t.Fatalf("missing-Podman fallbacks should not broadcast lifecycle success/error events: %#v", events)
	}
}

func TestContainerActionReturnsExplicitFallbackWhenPodmanMissing(t *testing.T) {
	database := openContainerRuntimeDB(t)
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "compose.yml"), []byte("services:\n  web:\n    image: test\n"), 0o644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	insertContainerRuntimeProjectAndChapter(t, database, worktree, `{}`, "")
	t.Setenv("PATH", t.TempDir())

	handler := NewHandler(&Service{DB: database})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/c1/containers/start", nil)
	newChapterRouter(handler).ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["fallback"] != true || body["code"] != "PODMAN_NOT_FOUND" || body["supported"] != false || body["reason"] == "" {
		t.Fatalf("fallback body = %#v", body)
	}
}

func newChapterRouter(handler *Handler) http.Handler {
	router := chi.NewRouter()
	handler.RegisterRoutes(router)
	return router
}

func openContainerRuntimeDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })
	stmts := []string{
		`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT, git_path TEXT, default_branch TEXT, proxy_domain TEXT, startup_script TEXT, copy_files TEXT, chapter_settings TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE chapters (id TEXT PRIMARY KEY, project_id TEXT, title TEXT, description TEXT, status TEXT, role TEXT, branch TEXT, worktree_path TEXT, base_branch TEXT, parent_chapter_id TEXT, fork_point TEXT, merged_into_chapter_id TEXT, merge_commit_sha TEXT, merge_strategy TEXT, pre_merge_target_sha TEXT, container_config TEXT, exploration_group_id TEXT, is_root INTEGER, head_commit_sha TEXT, start_commit_sha TEXT, commit_count INTEGER, color TEXT, group_label TEXT, pinned INTEGER, anchor_commit_sha TEXT, axis_offset REAL, cross_offset REAL, panel_expanded INTEGER, panel_width REAL, panel_height REAL, review_source_chapter_id TEXT, review_status TEXT, last_accessed_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE container_instances (id TEXT PRIMARY KEY, chapter_id TEXT, container_id TEXT, service_name TEXT, status TEXT, host_port INTEGER, container_port INTEGER, proxy_label TEXT, container_ip TEXT, volume_name TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE narrators (id TEXT PRIMARY KEY, chapter_id TEXT, variant TEXT, enabled_tools TEXT, message_count INTEGER DEFAULT 0, message_version INTEGER DEFAULT 0, last_message_at TEXT, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE narrator_messages (id TEXT PRIMARY KEY, narrator_id TEXT, role TEXT, content_json TEXT, content_text TEXT, created_at TEXT)`,
		`CREATE TABLE narrator_message_refs (id TEXT PRIMARY KEY, narrator_id TEXT, message_id TEXT, seq INTEGER, is_compact INTEGER DEFAULT 0)`,
		`CREATE TABLE port_allocations (port INTEGER PRIMARY KEY, chapter_id TEXT, service_name TEXT, allocated_at TEXT)`,
		`CREATE TABLE terminals (id TEXT PRIMARY KEY, chapter_id TEXT, status TEXT)`,
		`CREATE TABLE terminal_tabs (id TEXT PRIMARY KEY, chapter_id TEXT)`,
		`CREATE TABLE terminal_view_state (id TEXT PRIMARY KEY, chapter_id TEXT)`,
	}
	for _, stmt := range stmts {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatalf("create schema: %v", err)
		}
	}
	return database
}

func insertContainerRuntimeProjectAndChapter(t *testing.T, database *sql.DB, worktree, containerConfig, proxyDomain string) {
	t.Helper()
	if _, err := database.Exec(`INSERT INTO projects (id, name, git_path, default_branch, proxy_domain, created_at, updated_at) VALUES ('p1', 'Project', ?, 'main', NULLIF(?, ''), 'now', 'now')`, worktree, proxyDomain); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, container_config, is_root, commit_count, pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at) VALUES ('c1', 'p1', 'Chapter', 'active', 'branch', 'chapter/test', ?, 'main', ?, 0, 0, 0, 0, 0, 0, 'now', 'now')`, worktree, containerConfig); err != nil {
		t.Fatalf("insert chapter: %v", err)
	}
}

func loadTempSettings(t *testing.T) *config.Store {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	return settings
}

func prependFakePodman(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "podman")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake podman: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func fakePodmanScript() string {
	return `#!/bin/sh
if [ "$1" = "--version" ]; then echo "podman version 5.0.0"; exit 0; fi
if [ "$1" = "info" ]; then echo "pasta"; exit 0; fi
if [ "$1" = "compose" ] && [ "$2" = "version" ]; then echo "podman-compose version 1.0.0"; exit 0; fi
if [ "$1" = "compose" ]; then
  case " $* " in
    *" ps --format json "*) printf '[{"ID":"cid1","Service":"web","State":"running","Publishers":[{"PublishedPort":18080,"TargetPort":8080}]}]'; exit 0 ;;
    *" up -d "*) printf 'STEP 1/2: FROM alpine\nContainer web Creating\nContainer web Started\n'; exit 0 ;;
    *" stop "*|*" pause "*|*" unpause "*|*" down "*) echo "ok"; exit 0 ;;
    *" logs "*) echo "log line"; exit 0 ;;
  esac
fi
if [ "$1" = "inspect" ]; then
  case "$3" in
    *ExposedPorts*) printf '{"8080/tcp":{}}'; exit 0 ;;
    *) printf '10.88.0.2 '; exit 0 ;;
  esac
fi
exit 0
`
}

func prependBlockingPodman(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	releasePath := filepath.Join(dir, "release")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then echo "podman version 5.0.0"; exit 0; fi
if [ "$1" = "info" ]; then echo "pasta"; exit 0; fi
if [ "$1" = "compose" ] && [ "$2" = "version" ]; then echo "podman-compose version 1.0.0"; exit 0; fi
if [ "$1" = "compose" ]; then
  case " $* " in
    *" ps --format json "*) printf '[{"ID":"cid1","Service":"web","State":"running","Publishers":[{"PublishedPort":18080,"TargetPort":8080}]}]'; exit 0 ;;
    *" up -d "*) printf 'STEP 1/2: FROM alpine\n'; while [ ! -f "` + releasePath + `" ]; do sleep 0.02; done; printf 'Container web Started\n'; exit 0 ;;
  esac
fi
exit 0
`
	path := filepath.Join(dir, "podman")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write blocking podman: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return releasePath
}

func prependRecordingPodman(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "podman.log")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "` + logPath + `"
if [ "$1" = "--version" ]; then echo "podman version 5.0.0"; exit 0; fi
if [ "$1" = "info" ]; then echo "pasta"; exit 0; fi
if [ "$1" = "compose" ] && [ "$2" = "version" ]; then echo "podman-compose version 1.0.0"; exit 0; fi
if [ "$1" = "compose" ]; then
  case " $* " in
    *" logs "*) i=0; while [ $i -lt 70000 ]; do printf x; i=$((i+1)); done; exit 0 ;;
    *" ps --format json "*) printf '[{"ID":"cid1","Service":"web","State":"running","Publishers":[{"PublishedPort":18080,"TargetPort":8080}]}]'; exit 0 ;;
    *" unpause "*) if [ "$NARRAFORK_FAIL_UNPAUSE" = "1" ]; then echo "unpause failed" >&2; exit 1; fi; echo "ok"; exit 0 ;;
    *" down "*) if [ "$NARRAFORK_FAIL_DOWN" = "1" ]; then echo "down failed" >&2; exit 1; fi; echo "ok"; exit 0 ;;
    *" up -d "*) echo "started"; exit 0 ;;
    *" pause "*|*" stop "*) echo "ok"; exit 0 ;;
  esac
fi
if [ "$1" = "inspect" ]; then
  case "$3" in
    *ExposedPorts*) printf '{"8080/tcp":{}}'; exit 0 ;;
    *) printf '10.88.0.2 '; exit 0 ;;
  esac
fi
exit 0
`
	path := filepath.Join(dir, "podman")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write recording podman: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func newContainerLifecycleWorktree(t *testing.T, keepWorktree bool) (string, string) {
	t.Helper()
	repo := newForkParityRepo(t)
	branch := "chapter/test"
	worktree := filepath.Join(repo, ".worktrees", "test")
	runGit(t, repo, "branch", branch)
	runGit(t, repo, "worktree", "add", worktree, branch)
	if err := os.WriteFile(filepath.Join(worktree, "compose.yml"), []byte("services:\n  web:\n    image: test\n"), 0o644); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	runGit(t, worktree, "add", "compose.yml")
	runGit(t, worktree, "commit", "-m", "add compose")
	if !keepWorktree {
		runGit(t, repo, "worktree", "remove", "--force", worktree)
	}
	return repo, worktree
}

func insertContainerLifecycleChapter(t *testing.T, database *sql.DB, repo, worktree, status, containerConfig string) {
	t.Helper()
	if _, err := database.Exec(`INSERT INTO projects (id, name, git_path, default_branch, created_at, updated_at) VALUES ('p1', 'Project', ?, 'master', 'now', 'now')`, repo); err != nil {
		t.Fatalf("insert lifecycle project: %v", err)
	}
	var worktreeValue any
	if strings.TrimSpace(worktree) != "" {
		worktreeValue = worktree
	}
	if _, err := database.Exec(`INSERT INTO chapters (id, project_id, title, status, role, branch, worktree_path, base_branch, container_config, is_root, commit_count, pinned, axis_offset, cross_offset, panel_expanded, created_at, updated_at) VALUES ('c1', 'p1', 'Chapter', ?, 'branch', 'chapter/test', ?, 'master', ?, 0, 0, 0, 0, 0, 0, 'now', 'now')`, status, worktreeValue, containerConfig); err != nil {
		t.Fatalf("insert lifecycle chapter: %v", err)
	}
}

func readTextFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func assertContainerRuntimeCount(t *testing.T, database *sql.DB, query string, want int) {
	t.Helper()
	var got int
	if err := database.QueryRow(query).Scan(&got); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if got != want {
		t.Fatalf("count for %q = %d, want %d", query, got, want)
	}
}

func lastNonEmptyLine(value string) string {
	lines := strings.Split(value, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			return line
		}
	}
	return ""
}
