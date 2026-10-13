//go:build windows

package handlers

import (
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func canonicalGuardPath(path string) (string, error) {
	resolved := filepath.Clean(path)
	if !filepath.IsAbs(resolved) {
		return "", fmt.Errorf("canonical Windows path is not absolute: %q", resolved)
	}
	name, err := windows.UTF16PtrFromString(resolved)
	if err != nil {
		return "", err
	}
	// Go can report a mount-point junction as ModeIrregular and leave its
	// final path component unresolved. Open without OPEN_REPARSE_POINT so
	// the handle binds its actual referent, including a junction directory.
	// Zero desired access queries its name without requesting file contents.
	handle, err := windows.CreateFile(name, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", fmt.Errorf("open canonical path %q: %w", resolved, err)
	}
	defer windows.CloseHandle(handle)

	// A fixed UTF-16 budget covers the Windows extended-path limit. Do not
	// trust an oversized API length or retry with an unbounded allocation.
	var buffer [32768]uint16
	length, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
	if err != nil {
		return "", fmt.Errorf("resolve canonical path %q: %w", resolved, err)
	}
	if length == 0 || length >= uint32(len(buffer)) {
		return "", fmt.Errorf("canonical path %q exceeds the supported path budget", resolved)
	}
	return normalizeWindowsGuardPath(windows.UTF16ToString(buffer[:length]))
}

func normalizeWindowsGuardPath(path string) (string, error) {
	const uncPrefix = `\\?\UNC\`
	const dosPrefix = `\\?\`
	if len(path) > len(uncPrefix) && strings.EqualFold(path[:len(uncPrefix)], uncPrefix) {
		path = `\\` + path[len(uncPrefix):]
	} else if strings.HasPrefix(path, dosPrefix) && len(path) >= 7 &&
		((path[4] >= 'A' && path[4] <= 'Z') || (path[4] >= 'a' && path[4] <= 'z')) &&
		path[5] == ':' && path[6] == '\\' {
		path = path[len(dosPrefix):]
	} else {
		return "", fmt.Errorf("unsupported canonical Windows path namespace %q", path)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("canonical Windows path is not absolute: %q", path)
	}
	volume := filepath.VolumeName(path)
	if strings.HasPrefix(volume, `\\`) {
		for _, component := range strings.Split(strings.TrimPrefix(volume, `\\`), `\`) {
			if strings.HasSuffix(component, ".") || strings.HasSuffix(component, " ") {
				return "", fmt.Errorf("canonical Windows volume cannot be represented without its extended prefix: %q", path)
			}
		}
	}
	for _, component := range strings.Split(path[len(volume):], `\`) {
		if component == "" {
			continue
		}
		// Downgrade only names whose ordinary Win32 interpretation is identical.
		// Otherwise trailing dots/spaces or DOS devices could redirect later I/O.
		base, _, _ := strings.Cut(component, ":")
		if !filepath.IsLocal(base) || windowsGuardReservedName(base) || strings.HasSuffix(component, ".") || strings.HasSuffix(component, " ") || strings.HasSuffix(base, ".") || strings.HasSuffix(base, " ") {
			return "", fmt.Errorf("canonical Windows name cannot be represented without its extended prefix: %q", path)
		}
	}
	return filepath.Clean(path), nil
}

func windowsGuardReservedName(name string) bool {
	base, _, _ := strings.Cut(name, ".")
	base = strings.ToUpper(strings.TrimRight(base, " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT") {
		switch base[3:] {
		case "1", "2", "3", "4", "5", "6", "7", "8", "9", "¹", "²", "³":
			return true
		}
	}
	return false
}
