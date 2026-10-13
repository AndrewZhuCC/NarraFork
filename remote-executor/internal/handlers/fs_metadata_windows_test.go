//go:build windows

package handlers

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsDACLStatesAndACEOrder(t *testing.T) {
	type fingerprint struct {
		control windows.SECURITY_DESCRIPTOR_CONTROL
		acl     []byte
	}
	states := make([]fingerprint, 0, 3)
	for _, sddl := range []string{"O:WD", "D:NO_ACCESS_CONTROL", "D:"} {
		sd, err := windows.SecurityDescriptorFromString(sddl)
		if err != nil {
			t.Fatal(err)
		}
		acl, err := descriptorACL(sd, false)
		if err != nil {
			t.Fatal(err)
		}
		control, _, err := sd.Control()
		if err != nil {
			t.Fatal(err)
		}
		states = append(states, fingerprint{control & daclControlMask, acl})
	}
	for i := range states {
		for j := i + 1; j < len(states); j++ {
			if states[i].control == states[j].control && bytes.Equal(states[i].acl, states[j].acl) {
				t.Fatalf("DACL states %d/%d collapsed", i, j)
			}
		}
	}
	a, err := windows.SecurityDescriptorFromString("D:(A;;FR;;;WD)(A;;FW;;;AU)")
	if err != nil {
		t.Fatal(err)
	}
	b, err := windows.SecurityDescriptorFromString("D:(A;;FW;;;AU)(A;;FR;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	aa, err := descriptorACL(a, false)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := descriptorACL(b, false)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(aa, bb) {
		t.Fatal("ACE ordering silently canonicalized")
	}
}

func TestWindowsDACLAvailabilityCompatibility(t *testing.T) {
	fingerprint := func(sddl string) ([]byte, windows.SECURITY_DESCRIPTOR_CONTROL) {
		t.Helper()
		sd, err := windows.SecurityDescriptorFromString(sddl)
		if err != nil {
			t.Fatal(err)
		}
		acl, err := descriptorACL(sd, false)
		if err != nil {
			t.Fatal(err)
		}
		control, _, err := sd.Control()
		if err != nil {
			t.Fatal(err)
		}
		return acl, control
	}
	original := "D:(A;ID;FA;;;SY)(A;ID;FA;;;BA)(A;ID;FA;;;WD)"
	for _, tc := range []struct {
		name  string
		left  string
		right string
		want  bool
	}{
		{"native duplicates and inheritance", original, "D:AI(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;WD)(A;ID;FA;;;SY)(A;ID;FA;;;BA)(A;ID;FA;;;WD)", true},
		{"native backup order and owner ACE", original, "D:(A;;FA;;;BA)(A;ID;FA;;;SY)(A;ID;FA;;;WD)", true},
		{"changed rights", original, "D:(A;ID;FA;;;SY)(A;ID;FR;;;BA)(A;ID;FA;;;WD)", false},
		{"added trustee", original, original + "(A;;FR;;;AU)", false},
		{"removed trustee", original, "D:(A;ID;FA;;;SY)(A;ID;FA;;;BA)", false},
		{"inherit-only changes access", "D:(A;OIIO;FR;;;WD)", "D:(A;OI;FR;;;WD)", false},
		{"protected state", original, "D:P(A;ID;FA;;;SY)(A;ID;FA;;;BA)(A;ID;FA;;;WD)", false},
		{"deny order remains strict", "D:(D;;FW;;;WD)(A;;FA;;;WD)", "D:(A;;FA;;;WD)(D;;FW;;;WD)", false},
		{"null is not empty", "D:NO_ACCESS_CONTROL", "D:", false},
		{"absent is not null", "O:WD", "D:NO_ACCESS_CONTROL", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			left, lc := fingerprint(tc.left)
			right, rc := fingerprint(tc.right)
			if got := windowsDACLCompatible(left, right, lc, rc); got != tc.want {
				t.Fatalf("compatibility=%v want=%v controls=%#x/%#x", got, tc.want, lc, rc)
			}
		})
	}
	acl, control := fingerprint(original)
	if windowsDACLCompatible(acl, acl, control, control|windows.SE_DACL_AUTO_INHERIT_REQ) {
		t.Fatal("an inheritance request was treated as completed native bookkeeping")
	}
	for _, offset := range []int{0, 1, 2, 4, 6, 8, 9, 10, 16, 17} {
		malformed := bytes.Clone(acl)
		malformed[offset] ^= 0x80
		if windowsDACLCompatible(acl, malformed, control, control|windows.SE_DACL_AUTO_INHERITED) {
			t.Fatalf("unsupported or malformed ACL accepted at offset %d", offset)
		}
	}
}

func TestWindowsConditionalRejectsSpecialTargets(t *testing.T) {
	for _, kind := range []string{"hardlink", "ADS"} {
		t.Run(kind, func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "target.txt")
			if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			if kind == "hardlink" {
				if err := os.Link(target, target+".link"); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(target+":hidden", []byte("stream"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			f, err := os.Open(target)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err := captureConditionalMetadata(f); err == nil {
				t.Fatalf("special target %s accepted", kind)
			}
			got, err := os.ReadFile(target)
			if err != nil || string(got) != "original" {
				t.Fatalf("original changed: %q %v", got, err)
			}
		})
	}
}

func TestWindowsConditionalPreparationDoesNotInstallDACL(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target.txt")
	if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	testSetWindowsDACL(t, target, "D:P(A;;FA;;;"+testWindowsUserSID(t)+")(A;;FR;;;WD)", true)
	source, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	metadata, err := captureConditionalMetadata(source)
	if err != nil {
		t.Fatal(err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".nf-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer tmp.Close()
	defer os.Remove(tmp.Name())
	before, err := captureWindowsMetadata(tmp, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := metadata.apply(tmp); err != nil {
		t.Fatal(err)
	}
	after, err := captureWindowsMetadata(tmp, false)
	if err != nil {
		t.Fatal(err)
	}
	if before.control&daclControlMask != after.control&daclControlMask || !bytes.Equal(before.dacl, after.dacl) {
		t.Fatal("preparation altered temporary DACL")
	}
	if err := metadata.matches(source); err != nil {
		t.Fatal(err)
	}
}
