package auth

// Role is a panel user's role.
type Role string

const (
	RoleSuperAdmin Role = "superadmin"
	RoleAdmin      Role = "admin"
	RoleApprover   Role = "approver"
	RoleOperator   Role = "operator"
	RoleViewer     Role = "viewer"
)

// Perm is a single permission checked by handlers.
type Perm string

const (
	PermDevicesRead   Perm = "devices.read"
	PermDevicesWrite  Perm = "devices.write"
	PermBackupsRun    Perm = "backups.run"
	PermBackupsAdmin  Perm = "backups.admin" // policies, deletion
	PermRestore       Perm = "backups.restore"
	PermAlertsManage  Perm = "alerts.manage"
	PermPackages      Perm = "packages.manage" // sources, uploads, repository
	PermChangeCreate  Perm = "changes.create"  // deployments, scripts, firmware upgrades
	PermChangeApprove Perm = "changes.approve"
	PermLicenseRead   Perm = "license.read"
	PermLicenseManage Perm = "license.manage"
	PermUsersManage   Perm = "users.manage"
	PermAuditRead     Perm = "audit.read"
	PermSettings      Perm = "settings.manage"
)

var rolePerms = map[Role][]Perm{
	RoleSuperAdmin: {
		PermDevicesRead, PermDevicesWrite, PermBackupsRun, PermBackupsAdmin, PermRestore, PermAlertsManage,
		PermChangeApprove, PermLicenseRead, PermLicenseManage, PermUsersManage, PermAuditRead, PermSettings, PermPackages, PermChangeCreate,
	},
	RoleAdmin: {
		PermDevicesRead, PermDevicesWrite, PermBackupsRun, PermBackupsAdmin, PermRestore, PermAlertsManage,
		PermChangeApprove, PermLicenseRead, PermAuditRead, PermPackages, PermChangeCreate,
	},
	RoleApprover: {PermDevicesRead, PermChangeApprove, PermLicenseRead, PermAuditRead},
	RoleOperator: {PermDevicesRead, PermBackupsRun, PermLicenseRead, PermChangeCreate},
	RoleViewer:   {PermDevicesRead, PermLicenseRead},
}

// Valid reports whether r is a known role.
func (r Role) Valid() bool { _, ok := rolePerms[r]; return ok }

// Can reports whether the role grants p.
func (r Role) Can(p Perm) bool {
	for _, have := range rolePerms[r] {
		if have == p {
			return true
		}
	}
	return false
}

// Perms lists the role's permissions, for the UI.
func (r Role) Perms() []Perm { return append([]Perm(nil), rolePerms[r]...) }
