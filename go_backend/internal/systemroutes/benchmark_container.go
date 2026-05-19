package systemroutes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const benchmarkContainerOutputLimit = 1024 * 1024

type benchmarkCommandResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

type benchmarkPodmanRunner interface {
	Run(ctx context.Context, args []string, stdin string, timeout time.Duration) (benchmarkCommandResult, error)
}

type podmanCommandRunner struct{}

var benchmarkContainerRunner benchmarkPodmanRunner = podmanCommandRunner{}

func (podmanCommandRunner) Run(ctx context.Context, args []string, stdin string, timeout time.Duration) (benchmarkCommandResult, error) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "podman", args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedBuffer{Buffer: &stdout, Limit: benchmarkContainerOutputLimit}
	cmd.Stderr = &limitedBuffer{Buffer: &stderr, Limit: benchmarkContainerOutputLimit}
	err := cmd.Run()
	result := benchmarkCommandResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return result, nil
		}
		return result, err
	}
	return result, nil
}

type limitedBuffer struct {
	*bytes.Buffer
	Limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Buffer.Len() >= b.Limit {
		return len(p), nil
	}
	remaining := b.Limit - b.Buffer.Len()
	if len(p) > remaining {
		_, _ = b.Buffer.Write(p[:remaining])
		_, _ = b.Buffer.WriteString("\n[truncated]")
		return len(p), nil
	}
	_, err := b.Buffer.Write(p)
	return len(p), err
}

type benchmarkContainerHandle struct {
	ID     string
	Output string
}

func (h *Handler) prepareBenchmarkContainer(ctx context.Context, run benchmarkRunRecord, task benchmarkTask, workDir string) (benchmarkContainerHandle, error) {
	if runtime.GOOS != "linux" {
		return benchmarkContainerHandle{}, errors.New("Benchmark container execution requires Linux/rootless Podman in go_backend")
	}
	if benchmarkContainerRunner == nil {
		return benchmarkContainerHandle{}, errors.New("Benchmark container execution is unavailable: podman runner is not configured")
	}
	image := firstNonEmpty(stringMap(task.Metadata, "containerImage", ""), stringMap(task.Metadata, "dockerImage", ""), stringMap(run.Config, "containerImage", ""), "python:3.11-slim")
	if !safeContainerImage(image) {
		return benchmarkContainerHandle{}, fmt.Errorf("unsafe benchmark container image %q", image)
	}
	if result, err := benchmarkContainerRunner.Run(ctx, []string{"image", "exists", image}, "", 5*time.Second); err != nil || result.ExitCode != 0 {
		pull, pullErr := benchmarkContainerRunner.Run(ctx, []string{"pull", image}, "", benchmarkDurationConfig(run, task, "imagePullTimeoutMs", 5*time.Minute))
		if pullErr != nil || pull.ExitCode != 0 {
			return benchmarkContainerHandle{}, fmt.Errorf("failed to pull benchmark image %s: %s%s", image, pull.Stderr, errorSuffix(pullErr))
		}
	}
	name := "nf-bench-" + safeBenchmarkPathPart(run.ID) + "-" + safeBenchmarkPathPart(task.ID)
	args := []string{"run", "-d", "--name", name, "--memory", benchmarkMemoryLimit(run, task), "--cpus", benchmarkCPUQuota(run, task), "--pids-limit", benchmarkPidsLimit(run, task), "--network", benchmarkNetwork(run, task), "-v", workDir + ":/testbed:Z", "-w", "/testbed", image, "tail", "-f", "/dev/null"}
	runResult, err := benchmarkContainerRunner.Run(ctx, args, "", 2*time.Minute)
	if err != nil || runResult.ExitCode != 0 {
		return benchmarkContainerHandle{}, fmt.Errorf("failed to create benchmark container: %s%s", runResult.Stderr, errorSuffix(err))
	}
	containerID := strings.TrimSpace(runResult.Stdout)
	if len(containerID) > 64 {
		containerID = containerID[:64]
	}
	output := runResult.Stdout
	if setup := h.benchmarkContainerSetupScript(run, task); setup != "" {
		setupResult, setupErr := benchmarkContainerRunner.Run(ctx, []string{"exec", "-i", containerID, "sh", "-lc", "cat > /tmp/nf-bench-setup.sh && sh /tmp/nf-bench-setup.sh"}, setup, benchmarkDurationConfig(run, task, "setupTimeoutMs", 5*time.Minute))
		output = joinOutputs(output, setupResult.Stdout, setupResult.Stderr)
		if setupErr != nil || setupResult.ExitCode != 0 {
			_ = cleanupBenchmarkContainer(context.Background(), containerID)
			return benchmarkContainerHandle{}, fmt.Errorf("SWE-bench setup failed (exit %d): %s%s", setupResult.ExitCode, truncateForBenchmark(joinOutputs(setupResult.Stdout, setupResult.Stderr), maxBenchmarkErrorChars), errorSuffix(setupErr))
		}
	}
	return benchmarkContainerHandle{ID: containerID, Output: output}, nil
}

func (h *Handler) benchmarkContainerSetupScript(run benchmarkRunRecord, task benchmarkTask) string {
	repo := strings.TrimSpace(stringMap(task.Metadata, "repo", ""))
	baseCommit := strings.TrimSpace(stringMap(task.Metadata, "baseCommit", ""))
	installCmd := firstNonEmpty(stringMap(task.Metadata, "installCmd", ""), "python -m pip install -e .")
	if repo == "" || baseCommit == "" {
		return ""
	}
	if !safeGithubRepo(repo) || !safeGitRef(baseCommit) {
		return "exit 64"
	}
	lines := []string{"set -eu", "command -v git >/dev/null 2>&1 || (apt-get update -qq && apt-get install -y -qq git >/dev/null 2>&1 || true)", "git clone https://github.com/" + repo + ".git /tmp/nf-bench-repo", "cd /tmp/nf-bench-repo", "git reset --hard " + baseCommit, "cp -a /tmp/nf-bench-repo/. /testbed/", "cd /testbed", installCmd}
	return strings.Join(lines, "\n")
}

func (h *Handler) evaluateBenchmarkTaskWithContainer(ctx context.Context, containerID string, task benchmarkTask, base benchmarkEvalResult, config map[string]any) benchmarkEvalResult {
	cmd := firstNonEmpty(stringMap(task.Metadata, "evalCommand", ""), stringMap(task.Metadata, "testCommand", ""), stringMap(config, "evalCommand", ""))
	if strings.TrimSpace(cmd) == "" {
		base.Metadata["containerId"] = containerID
		base.Metadata["evaluator"] = "go-container-fallback"
		return base
	}
	result, err := benchmarkContainerRunner.Run(ctx, []string{"exec", "-i", containerID, "sh", "-lc", "cat > /tmp/nf-bench-eval.sh && sh /tmp/nf-bench-eval.sh"}, "set -eu\ncd /testbed\n"+cmd+"\n", time.Duration(numericMapOr(task.Metadata, "evalTimeoutMs", numericMapOr(config, "evalTimeoutMs", 10*60*1000)))*time.Millisecond)
	output := truncateForBenchmark(joinOutputs(result.Stdout, result.Stderr), maxBenchmarkOutputChars)
	passed := err == nil && result.ExitCode == 0
	return benchmarkEvalResult{Passed: passed, Score: boolScore(passed), MaxScore: 1, Details: fmt.Sprintf("Container evaluator exit=%d\n%s", result.ExitCode, output), Metadata: map[string]any{"evaluator": "go-container", "containerId": containerID, "exitCode": result.ExitCode}}
}

func cleanupBenchmarkContainer(ctx context.Context, containerID string) error {
	if strings.TrimSpace(containerID) == "" || benchmarkContainerRunner == nil {
		return nil
	}
	_, err := benchmarkContainerRunner.Run(ctx, []string{"rm", "-f", containerID}, "", 30*time.Second)
	return err
}

func benchmarkDurationConfig(run benchmarkRunRecord, task benchmarkTask, key string, fallback time.Duration) time.Duration {
	ms := numericMapOr(task.Metadata, key, numericMapOr(run.Config, key, 0))
	if ms <= 0 {
		return fallback
	}
	if ms > 30*60*1000 {
		ms = 30 * 60 * 1000
	}
	return time.Duration(ms) * time.Millisecond
}

func benchmarkMemoryLimit(run benchmarkRunRecord, task benchmarkTask) string {
	value := firstNonEmpty(stringMap(task.Metadata, "memoryLimit", ""), stringMap(run.Config, "memoryLimit", ""), "4g")
	if matched, _ := regexp.MatchString(`^[0-9]+[kKmMgG]?$`, value); matched {
		return value
	}
	return "4g"
}
func benchmarkCPUQuota(run benchmarkRunRecord, task benchmarkTask) string {
	value := firstNonEmpty(stringMap(task.Metadata, "cpus", ""), stringMap(run.Config, "cpus", ""), "2")
	if matched, _ := regexp.MatchString(`^[0-9]+(\.[0-9]+)?$`, value); matched {
		return value
	}
	return "2"
}
func benchmarkPidsLimit(run benchmarkRunRecord, task benchmarkTask) string {
	value := firstNonEmpty(stringMap(task.Metadata, "pidsLimit", ""), stringMap(run.Config, "pidsLimit", ""), "512")
	if matched, _ := regexp.MatchString(`^[0-9]+$`, value); matched {
		return value
	}
	return "512"
}
func benchmarkNetwork(run benchmarkRunRecord, task benchmarkTask) string {
	value := strings.ToLower(firstNonEmpty(stringMap(task.Metadata, "network", ""), stringMap(run.Config, "network", "bridge")))
	if value == "none" || value == "bridge" || value == "slirp4netns" || value == "pasta" {
		return value
	}
	return "bridge"
}

func safeContainerImage(value string) bool {
	matched, _ := regexp.MatchString(`^[A-Za-z0-9][A-Za-z0-9._/:@-]{0,255}$`, value)
	return matched && !strings.Contains(value, "..")
}
func safeGithubRepo(value string) bool {
	matched, _ := regexp.MatchString(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`, value)
	return matched
}
func safeGitRef(value string) bool {
	matched, _ := regexp.MatchString(`^[A-Za-z0-9._/-]{1,128}$`, value)
	return matched && !strings.Contains(value, "..")
}
func joinOutputs(values ...string) string {
	parts := []string{}
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			parts = append(parts, strings.TrimSpace(value))
		}
	}
	return strings.Join(parts, "\n")
}
func errorSuffix(err error) string {
	if err == nil {
		return ""
	}
	return ": " + err.Error()
}
