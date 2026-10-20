//go:build windows

package handlers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func checkWindowsFileMutationAlias(t *testing.T, root, path, alias string, before any) {
	t.Helper()
	for _, method := range []string{"write", "remove", "conditional", "exclusive", "transfer"} {
		t.Run(method, func(t *testing.T) {
			h := New(NewPathGuard([]string{root}), 4000000)
			release := acquireTestFileMutation(t, fileMutations, path)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, err := runFileMutationMethod(ctx, h, method, alias, before)
				result <- err
			}()
			waitFileMutationQueue(t, fileMutations, path, 1)
			cancel()
			if err := fileMutationResult(t, result); !errors.Is(err, context.Canceled) {
				t.Fatalf("alias escaped canonical queue: %v", err)
			}
			waitFileMutationQueue(t, fileMutations, path, 0)
			release()
			if before == nil {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("cancelled alias mutation created destination")
				}
			} else if data, err := os.ReadFile(path); err != nil || string(data) != before.(string) {
				t.Fatalf("cancelled alias mutation altered destination: %q, %v", data, err)
			}
		})
	}
}

func TestFileMutationWindowsCanonicalAliases(t *testing.T) {
	root := canonicalFixturePath(t, t.TempDir())
	path := filepath.Join(root, "Canonical long filename.txt")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	extended := `\\?\` + path
	if strings.HasPrefix(path, `\\`) {
		extended = `\\?\UNC\` + strings.TrimPrefix(path, `\\`)
	}
	for name, alias := range map[string]string{
		"case": strings.ToUpper(path), "slashes": filepath.ToSlash(path), "extended": extended,
	} {
		t.Run(name, func(t *testing.T) {
			checkWindowsFileMutationAlias(t, root, path, alias, "before")
		})
	}
	t.Run("short leaf name", func(t *testing.T) {
		name, err := windows.UTF16PtrFromString(path)
		if err != nil {
			t.Fatal(err)
		}
		var buffer [32768]uint16
		length, err := windows.GetShortPathName(name, &buffer[0], uint32(len(buffer)))
		if err != nil || length == 0 || length >= uint32(len(buffer)) {
			t.Fatalf("resolve short fixture path: %d, %v", length, err)
		}
		alias := windows.UTF16ToString(buffer[:length])
		if samePath(filepath.Base(alias), filepath.Base(path)) {
			t.Skip("fixture volume has 8.3 leaf aliases disabled")
		}
		checkWindowsFileMutationAlias(t, root, path, alias, "before")
	})
	t.Run("missing leaf case", func(t *testing.T) {
		path := filepath.Join(root, "MissingFile.txt")
		checkWindowsFileMutationAlias(t, root, path, strings.ToUpper(path), nil)
	})
}
