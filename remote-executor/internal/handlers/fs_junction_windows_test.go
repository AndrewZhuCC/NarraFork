//go:build windows

package handlers

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func init() {
	windowsJunctionCommand = func(ctx context.Context, link, target string) *exec.Cmd {
		command := exec.CommandContext(ctx, "cmd.exe")
		// Bypass MSVCRT argv escaping for cmd.exe's different quote grammar.
		// Expand process-local values once inside quotes; /v:off also preserves
		// literal '!' and /d disables AutoRun. No path is parsed as shell code.
		command.SysProcAttr = &syscall.SysProcAttr{
			CmdLine: `cmd.exe /d /v:off /s /c "mklink /J "%NARRAFORK_TEST_JUNCTION_LINK%" "%NARRAFORK_TEST_JUNCTION_TARGET%""`,
		}
		for _, entry := range os.Environ() {
			name, _, _ := strings.Cut(entry, "=")
			if !strings.EqualFold(name, "NARRAFORK_TEST_JUNCTION_LINK") && !strings.EqualFold(name, "NARRAFORK_TEST_JUNCTION_TARGET") {
				command.Env = append(command.Env, entry)
			}
		}
		command.Env = append(command.Env, "NARRAFORK_TEST_JUNCTION_LINK="+link, "NARRAFORK_TEST_JUNCTION_TARGET="+target)
		return command
	}
}

func TestWindowsJunctionFixtureQuotedPaths(t *testing.T) {
	for _, name := range []string{"amp&no-space", "space & percent% bang!"} {
		t.Run(name, func(t *testing.T) {
			base := filepath.Join(t.TempDir(), name)
			root, outside := filepath.Join(base, "root"), filepath.Join(base, "outside")
			for _, dir := range []string{root, outside} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			secret := filepath.Join(outside, "secret.txt")
			if err := os.WriteFile(secret, []byte("untouched"), 0600); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(root, "junction")
			requireWindowsJunction(t, outside, link)
			guard := NewPathGuard([]string{root})
			if _, err := guard.CheckExisting(filepath.Join(link, "secret.txt")); err == nil {
				t.Fatal("escaping junction existing path accepted")
			}
			if resolved, err := guard.CheckCreate(filepath.Join(link, "created.txt")); err == nil {
				// Both paths are inside this test's own base. Prove the accepted
				// name physically escapes the allow-root, not merely a string error.
				writeErr := os.WriteFile(resolved, []byte("owned-probe"), 0600)
				observed, readErr := os.ReadFile(filepath.Join(outside, "created.txt"))
				evaluated, evalErr := filepath.EvalSymlinks(link)
				info, statErr := os.Lstat(link)
				var mode os.FileMode
				if statErr == nil {
					mode = info.Mode()
				}
				t.Fatalf("escaping junction create accepted: resolved=%q linkMode=%v lstat=%v eval=%q evalErr=%v write=%v outside=%q read=%v", resolved, mode, statErr, evaluated, evalErr, writeErr, observed, readErr)
			}
			got, err := os.ReadFile(secret)
			if err != nil || string(got) != "untouched" {
				t.Fatalf("outside fixture changed: %q %v", got, err)
			}
		})
	}
}

func TestWindowsGuardPathNamespaces(t *testing.T) {
	for _, tc := range []struct {
		path string
		want string
	}{
		{`\\?\C:\allowed\file.txt`, `C:\allowed\file.txt`},
		{`\\?\c:\测试\file.txt`, `c:\测试\file.txt`},
		{`\\?\UNC\server\share\file.txt`, `\\server\share\file.txt`},
		{`\\?\unc\server\share\file.txt`, `\\server\share\file.txt`},
	} {
		got, err := normalizeWindowsGuardPath(tc.path)
		if err != nil || got != tc.want {
			t.Fatalf("normalize %q: got=%q want=%q err=%v", tc.path, got, tc.want, err)
		}
	}
	for _, path := range []string{
		`relative\file.txt`, `\\?\C:relative`, `\\.\PhysicalDrive0`,
		`\\?\GLOBALROOT\Device\HarddiskVolume1\file`, `\\?\Volume{unknown}\file`, "",
		`\\?\C:\allowed\secret.txt.`, `\\?\C:\allowed\secret.txt `,
		`\\?\C:\allowed.\file.txt`, `\\?\C:\allowed \file.txt`,
		`\\?\C:\allowed\NUL.txt`, `\\?\C:\allowed\CON`, `\\?\C:\allowed\COM1`,
		`\\?\C:\allowed\LPT1`, `\\?\C:\allowed\COM¹`, `\\?\C:\allowed\secret.txt.:stream`,
		`\\?\UNC\server\share\secret.txt.`, `\\?\UNC\server\share\NUL.txt`,
		`\\?\UNC\server\share.\file.txt`, `\\?\UNC\server \share\file.txt`,
	} {
		if got, err := normalizeWindowsGuardPath(path); err == nil || got != "" {
			t.Fatalf("uncertain path namespace accepted: %q -> %q (%v)", path, got, err)
		}
	}
}

func TestWindowsGuardLossyDOSNamespace(t *testing.T) {
	for _, suffix := range []string{".", " "} {
		t.Run(suffix, func(t *testing.T) {
			root := t.TempDir()
			denied := filepath.Join(root, "secret.txt")
			if err := os.WriteFile(denied, []byte("owned-denied-bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			exact := `\\?\` + denied + suffix
			name, err := windows.UTF16PtrFromString(exact)
			if err != nil {
				t.Fatal(err)
			}
			handle, err := windows.CreateFile(name, windows.GENERIC_WRITE,
				windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
				nil, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
			if err != nil {
				t.Fatal(err)
			}
			file := os.NewFile(uintptr(handle), exact)
			_, writeErr := file.Write([]byte("owned-distinct-extended-file"))
			closeErr := file.Close()
			t.Cleanup(func() {
				if err := windows.DeleteFile(name); err != nil {
					t.Errorf("remove owned extended fixture: %v", err)
				}
			})
			if writeErr != nil || closeErr != nil {
				t.Fatalf("prepare owned extended file: write=%v close=%v", writeErr, closeErr)
			}
			guard := NewPathGuardWithRules([]PathRule{
				{Action: RuleAllow, Path: root}, {Action: RuleDeny, Path: denied},
			})
			if resolved, err := guard.CheckExisting(exact); err == nil {
				result, readErr := New(guard, 1024).FsRead(map[string]any{"path": exact})
				t.Fatalf("lossy DOS namespace accepted: exact=%q resolved=%q read=%v result=%v", exact, resolved, readErr, result)
			}
			if got, err := os.ReadFile(denied); err != nil || string(got) != "owned-denied-bytes" {
				t.Fatalf("denied owned fixture changed: %q %v", got, err)
			}
		})
	}
}
