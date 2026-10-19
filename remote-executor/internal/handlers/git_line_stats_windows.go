//go:build windows

package handlers

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func lineStatsFileIdentity(f *os.File) (string, error) {
	h := windows.Handle(f.Fd())
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return "", err
	}
	var basic struct {
		CreationTime, LastAccessTime, LastWriteTime, ChangeTime int64
		Attributes                                              uint32
	}
	if err := windows.GetFileInformationByHandleEx(h, windows.FileBasicInfo, (*byte)(unsafe.Pointer(&basic)), uint32(unsafe.Sizeof(basic))); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d:%d:%d:%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow, info.FileSizeHigh, info.FileSizeLow, basic.LastWriteTime, basic.ChangeTime), nil
}
