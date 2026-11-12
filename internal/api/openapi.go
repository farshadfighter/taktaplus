package api

import (
	"net/http"
	"sync"

	"github.com/farshadfighter/taktaplus-lic/pkg/apidoc"

	"github.com/farshadfighter/taktaplus/internal/buildinfo"
)

// Tag descriptions shown in the API documentation.
var apiTags = map[string]string{
	"alert-rules": "قوانین هشدار", "backup-policies": "سیاست‌های بکاپ", "channels": "کانال‌های اعلان",
	"package-sources": "منابع پکیج", "restores": "ریستور",
	"auth": "ورود به پنل", "me": "کاربر فعلی", "audit": "لاگ ممیزی", "tokens": "توکن‌های API", "users": "کاربران پنل",
	"license": "لایسنس", "devices": "تجهیزات", "groups": "گروه‌های دستگاه", "variables": "متغیرهای قالب", "alerts": "هشدارها",
	"backups": "بکاپ و ریستور", "packages": "پکیج‌ها و منابع", "sources": "منابع پکیج", "deployments": "استقرار",
	"changes": "درخواست‌های تغییر", "maintenance": "پنجره‌ها و تأیید", "scripts": "اسکریپت‌های CLI", "firmware": "فرم‌ور",
	"otp": "احراز هویت دو مرحله‌ای", "sms": "پیامک", "logs": "لاگ و تحلیل", "reports": "گزارش‌ها", "compliance": "قالب و انطباق",
	"policy": "Policy Packageها", "system": "بکاپ و ارتقای سیستم", "version": "نسخه", "openapi": "همین مستند",
}

var (
	openAPIOnce sync.Once
	openAPIDoc  map[string]any
)

// openAPI serves the OpenAPI document of this version.
func (s *srv) openAPI(w http.ResponseWriter, _ *http.Request) {
	openAPIOnce.Do(func() {
		openAPIDoc = apidoc.Build(apidoc.Info{
			Title:   "TaktaPlus API",
			Version: buildinfo.Version,
			Description: "REST API of TaktaPlus. Authenticate with an API token (Authorization: Bearer tpk_...) " +
				"created under Users; a token has a role, may be read-only (GET only) and may be limited to source addresses. " +
				"Actions marked panel_only need the password and authenticator code again and are refused for tokens.",
			Base: "/api/v1",
			Tags: apiTags,
		}, apiRoutes)
	})
	writeJSON(w, http.StatusOK, openAPIDoc)
}
