//go:build windows

package dashboardsetup

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func assertTokenPermissions(t *testing.T, path string) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatalf("GetTokenUser() error = %v", err)
	}
	userSID := user.User.Sid

	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo() error = %v", err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatalf("SecurityDescriptor.Control() error = %v", err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("setup token DACL is not protected from inheritance")
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		t.Fatalf("SecurityDescriptor.Owner() error = %v", err)
	}
	if !owner.Equals(userSID) {
		t.Fatalf("setup token owner = %s, want %s", owner, userSID)
	}
	dacl, defaulted, err := descriptor.DACL()
	if err != nil {
		t.Fatalf("SecurityDescriptor.DACL() error = %v", err)
	}
	if defaulted || dacl == nil || dacl.AceCount != 1 {
		t.Fatalf("setup token DACL must contain exactly one explicit ACE: defaulted=%v dacl=%v", defaulted, dacl)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatalf("GetAce() error = %v", err)
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags&windows.INHERITED_ACE != 0 {
		t.Fatalf("unexpected setup token ACE header: %+v", ace.Header)
	}
	aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !aceSID.Equals(userSID) {
		t.Fatalf("setup token ACE trustee = %s, want %s", aceSID, userSID)
	}
	if ace.Mask != windows.ACCESS_MASK(windows.GENERIC_ALL) {
		t.Fatalf("setup token ACE mask = %#x, want full control for the owner", ace.Mask)
	}
}
