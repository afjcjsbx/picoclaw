//go:build windows

package dashboardsetup

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/windows"
)

func writeTokenFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create token directory: %w", err)
	}

	file, err := os.CreateTemp(dir, ".dashboard-setup-token-*")
	if err != nil {
		return fmt.Errorf("create token temp file: %w", err)
	}
	tempPath := file.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = file.Close()
			_ = os.Remove(tempPath)
		}
	}()

	// Windows mode bits do not define a DACL. Restrict the empty temp file
	// before writing the secret so inherited permissions never expose it.
	if err := setOwnerOnlyACL(tempPath); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write token temp file: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync token temp file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close token temp file: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("rename token temp file: %w", err)
	}
	cleanup = false
	return nil
}

func setOwnerOnlyACL(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("get current user for setup token ACL: %w", err)
	}
	sid := user.User.Sid
	var pinner runtime.Pinner
	pinner.Pin(sid)
	defer pinner.Unpin()

	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.ACCESS_MASK(windows.GENERIC_ALL),
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}}, nil)
	if err != nil {
		return fmt.Errorf("build setup token ACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		sid,
		nil,
		acl,
		nil,
	); err != nil {
		return fmt.Errorf("apply owner-only setup token ACL: %w", err)
	}
	return nil
}
