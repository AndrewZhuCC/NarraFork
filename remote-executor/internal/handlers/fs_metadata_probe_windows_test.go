//go:build windows

package handlers

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func logWindowsMetadataProbe(t *testing.T, label, path string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Logf("%s open: %v", label, err)
		return
	}
	defer f.Close()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s links=%d attrs=%#x", label, info.NumberOfLinks, info.FileAttributes)
	m, err := captureWindowsMetadata(f, true)
	if err != nil {
		t.Logf("%s capture: %v", label, err)
		return
	}
	t.Logf("%s owner=%s group=%s control=%#x dacl=%x label=%x auditReadable=%v auditControl=%#x audit=%x", label, m.owner.String(), m.group.String(), m.control, m.dacl, m.labelACL, m.audit != nil, m.auditControl, m.auditACL)
}

func TestWindowsNativeMetadataProbe(t *testing.T) {
	for _, kind := range []string{"ordinary", "protected", "complex-inherited"} {
		t.Run(kind, func(t *testing.T) {
			parent := t.TempDir()
			if kind == "complex-inherited" {
				testSetWindowsDACL(t, parent, "D:P(A;OICI;FA;;;"+testWindowsUserSID(t)+")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FR;;;BU)(A;OICI;GR;;;AU)(A;OICIIO;GA;;;CO)", true)
			}
			target := filepath.Join(parent, "target.txt")
			if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			if kind == "protected" {
				testSetWindowsDACL(t, target, "D:P(A;;FA;;;"+testWindowsUserSID(t)+")(A;;FR;;;WD)", true)
			}
			target, tmp, _, metadata := windowsPreparedReplacement(t, target)
			m := metadata.(*windowsConditionalMetadata)
			logWindowsMetadataProbe(t, "before", target)
			logWindowsMetadataProbe(t, "prepared", tmp)
			dir := filepath.Join(filepath.Dir(target), "raw-private-backup")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			testSetWindowsDACL(t, dir, "D:P(A;OICI;FA;;;"+testWindowsUserSID(t)+")", true)
			backup := filepath.Join(dir, "original")
			if err := nativeConditionalReplaceFile(target, tmp, backup); err != nil {
				t.Fatal(err)
			}
			for label, path := range map[string]string{"committed": target, "backup": backup} {
				logWindowsMetadataProbe(t, label, path)
				f, err := openConditionalWindowsFile(path, windows.GENERIC_READ|windows.READ_CONTROL|windows.WRITE_DAC)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("%s strict-before=%v", label, m.matches(f))
				err = windows.SetKernelObjectSecurity(windows.Handle(f.Fd()), windows.DACL_SECURITY_INFORMATION, m.sd)
				t.Logf("%s SetKernelObjectSecurity=%v strict-after=%v", label, err, m.matches(f))
				f.Close()
				logWindowsMetadataProbe(t, label+"-restored", path)
			}
		})
	}
}

func TestWindowsRecoveryDirectorySecurityProbe(t *testing.T) {
	sid := testWindowsUserSID(t)
	sd, err := windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;OICI;FA;;;" + sid + ")(A;OICI;FA;;;SY)")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "private")
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	if err := windows.CreateDirectory(p, &sa); err != nil {
		t.Fatal(err)
	}
	f, err := openConditionalWindowsFile(dir, windows.READ_CONTROL)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	actual, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := actual.Owner()
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := actual.Control()
	if err != nil {
		t.Fatal(err)
	}
	wantACL, err := descriptorACL(sd, false)
	if err != nil {
		t.Fatal(err)
	}
	gotACL, err := descriptorACL(actual, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("requestedOwner=%s actualOwner=%s control=%#x wantACL=%x gotACL=%x", sid, owner.String(), control, wantACL, gotACL)
}

func TestWindowsMandatoryLabelSetupProbe(t *testing.T) {
	var backing [256 / 8]uint64
	buffer := unsafe.Slice((*byte)(unsafe.Pointer(&backing[0])), 256)
	var returned uint32
	if err := windows.GetTokenInformation(windows.GetCurrentProcessToken(), windows.TokenIntegrityLevel, &buffer[0], uint32(len(buffer)), &returned); err != nil {
		t.Fatal(err)
	}
	callerLabel := (*windows.Tokenmandatorylabel)(unsafe.Pointer(&buffer[0])).Label.Sid.String()
	for _, label := range []string{"LW", callerLabel} {
		for _, access := range []uint32{windows.WRITE_OWNER, windows.WRITE_OWNER | windows.READ_CONTROL} {
			target := filepath.Join(t.TempDir(), "label.txt")
			if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			sd, err := windows.SecurityDescriptorFromString("S:(ML;;NW;;;" + label + ")")
			if err != nil {
				t.Fatal(err)
			}
			acl, _, err := sd.SACL()
			if err != nil {
				t.Fatal(err)
			}
			f, err := openConditionalWindowsFile(target, access)
			if err != nil {
				t.Fatal(err)
			}
			err = windows.SetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.LABEL_SECURITY_INFORMATION, nil, nil, nil, acl)
			f.Close()
			t.Logf("callerLabel=%s requestedLabel=%s access=%#x setup=%v (%T %#v)", callerLabel, label, access, err, err, err)
			logWindowsMetadataProbe(t, "label", target)
		}
	}
}

func TestWindowsTokenPrivilegesAlignmentProbe(t *testing.T) {
	var backing [4096 / 4]uint32
	buffer := unsafe.Slice((*byte)(unsafe.Pointer(&backing[0])), 4096)
	var returned uint32
	err := windows.GetTokenInformation(windows.GetCurrentProcessToken(), windows.TokenPrivileges, &buffer[0], uint32(len(buffer)), &returned)
	t.Logf("TokenPrivileges aligned addressMod8=%d returned=%d error=%v", uintptr(unsafe.Pointer(&buffer[0]))%8, returned, err)
	if err != nil {
		t.Fatal(err)
	}
	var legacyBacking [4096/8 + 1]uint64
	legacy := unsafe.Slice((*byte)(unsafe.Pointer(&legacyBacking[0])), 4104)[2 : 2+4096 : 2+4096]
	err = windows.GetTokenInformation(windows.GetCurrentProcessToken(), windows.TokenPrivileges, &legacy[0], uint32(len(legacy)), &returned)
	t.Logf("TokenPrivileges legacy addressMod8=%d error=%v (%T %#v)", uintptr(unsafe.Pointer(&legacy[0]))%8, err, err, err)
}
