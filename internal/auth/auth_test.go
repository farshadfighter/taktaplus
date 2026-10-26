package auth

import (
	"testing"
	"time"
)

func TestPassword(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := CheckPassword(h, "correct horse battery"); err != nil || !ok {
		t.Fatalf("expected match: %v", err)
	}
	if ok, _ := CheckPassword(h, "wrong horse battery"); ok {
		t.Fatal("wrong password matched")
	}
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("short password accepted")
	}
	if _, err := CheckPassword("$bcrypt$x", "x"); err == nil {
		t.Fatal("foreign hash accepted")
	}
}

func TestTOTPVectors(t *testing.T) {
	// RFC 6238 appendix B, SHA-1 secret, truncated to 6 digits.
	secret := []byte("12345678901234567890")
	cases := map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"}
	for ts, want := range cases {
		if got := TOTPCode(secret, time.Unix(ts, 0)); got != want {
			t.Errorf("t=%d: got %s, want %s", ts, got, want)
		}
	}
}

func TestVerifyTOTPWindow(t *testing.T) {
	secret := NewTOTPSecret()
	now := time.Unix(1_800_000_000, 0)
	if !VerifyTOTP(secret, TOTPCode(secret, now.Add(-30*time.Second)), now) {
		t.Error("previous step must be accepted")
	}
	if VerifyTOTP(secret, TOTPCode(secret, now.Add(-90*time.Second)), now) {
		t.Error("code three steps old must be rejected")
	}
	if VerifyTOTP(secret, "12345", now) {
		t.Error("short code must be rejected")
	}
}

func TestRoles(t *testing.T) {
	if !RoleSuperAdmin.Can(PermUsersManage) || RoleAdmin.Can(PermUsersManage) {
		t.Error("only superadmins manage users")
	}
	if !RoleOperator.Can(PermBackupsRun) || RoleOperator.Can(PermRestore) {
		t.Error("operators back up but do not restore")
	}
	if RoleViewer.Can(PermDevicesWrite) || !RoleViewer.Can(PermDevicesRead) {
		t.Error("viewer permissions wrong")
	}
	if Role("root").Valid() {
		t.Error("unknown role accepted")
	}
}
