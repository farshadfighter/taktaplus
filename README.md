# TaktaPlus

مدیریت متمرکز FortiGate و FortiWeb به صورت On-Premise: بروزرسانی سیگنیچر بدون دسترسی به FortiGuard، بکاپ و ریستور، مانیتورینگ، احراز هویت دو مرحله‌ای پیامکی، و گزارش‌گیری.

- معماری: [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)
- سرور لایسنس: [taktaplus-Lic](https://github.com/farshadfighter/taktaplus-Lic)

## وضعیت

فاز ۱ (در شاخه‌ی `claude/phase-1`):

- **هسته:** کاربران پنل با نقش، ورود با رمز + TOTP اجباری، قفل پس از ۵ خطا، لاگ ممیزی، تأیید مجدد رمز و کد برای ریستور و حذف دستگاه.
- **لایسنس:** فعال‌سازی آنلاین و آفلاین، Heartbeat، تشخیص عقب کشیدن ساعت؛ فقط سریال‌های فعال یا در مهلت پایش و بکاپ می‌شوند.
- **ماژول A:** ثبت FortiGate و FortiWeb با REST API و/یا SSH (ترتیب قابل تنظیم)، Pin گواهی TLS و کلید SSH در اولین اتصال، کشف مدل/فرم‌ور/HA/VDOM، پایش هر ۶۰ ثانیه (CPU، حافظه، دیسک، Session، اینترفیس‌ها)، نسخه‌ی سیگنیچرها و قراردادهای FortiGuard هر ۶ ساعت، قوانین هشدار با کانال‌های ایمیل، Webhook و Syslog.
- **ماژول C:** بکاپ زمان‌بندی‌شده (Cron)، فوری و قبل از ریستور؛ فایل رمزنگاری‌شده؛ حذف نسخه‌ی تکراری؛ نگهداری بر اساس تعداد/روز و قفل؛ نمایش با ماسک رمزها؛ Diff بلاکی؛ پیش‌نمایش و ریستور با بکاپ ایمنی و انتظار برای ریبوت.

> درایورها بر اساس مستندات FortiOS/FortiWeb و با سرورهای شبیه‌سازی‌شده تست شده‌اند و باید روی دستگاه واقعی تأیید شوند؛ مسیرهای API فورتی‌وب (`/api/v2.0/...`) قطعیت کمتری دارند و بکاپ فورتی‌وب فعلاً از طریق SSH است.

## تنظیمات

| متغیر | توضیح |
|---|---|
| `TAKTAPLUS_DATABASE_URL` | آدرس PostgreSQL |
| `TAKTAPLUS_MASTER_KEY` | ۳۲ بایت تصادفی به base64 (`openssl rand -base64 32`)؛ رمزنگاری Secretها و فایل‌های بکاپ. **گم شدن آن یعنی غیرقابل‌خواندن شدن بکاپ‌ها.** |
| `TAKTAPLUS_DATA_DIR` | شناسه‌ی نصب و بکاپ‌ها (`backups/`)؛ پیش‌فرض `/var/lib/taktaplus` |
| `TAKTAPLUS_LICENSE_SERVER` | آدرس سرور لایسنس برای فعال‌سازی آنلاین (اختیاری) |
| `TAKTAPLUS_COOKIE_SECURE` | `false` فقط برای توسعه روی HTTP |
| `TAKTAPLUS_LISTEN` | آدرس گوش دادن، پیش‌فرض `:8443` (و `:8080` وقتی TLS خاموش است) |
| `TAKTAPLUS_TLS` | `on` (پیش‌فرض) یا `off` فقط برای توسعه |
| `TAKTAPLUS_TLS_CERT`, `TAKTAPLUS_TLS_KEY` | گواهی و کلید PEM سازمان؛ اگر خالی باشد گواهی خودامضا در `DATA_DIR/tls` ساخته می‌شود |

ساخت اولین مدیر: `TAKTAPLUS_ADMIN_PASSWORD=... taktaplus create-admin -username admin`

### دسترسی روی دستگاه

- **FortiGate:** یک REST API Admin با پروفایل فقط‌خواندنی (و برای ریستور، دسترسی نوشتن به System) و Trusted Host آدرس TaktaPlus؛ یا کاربر SSH.
- **FortiWeb:** کاربر ادمین برای REST API و کاربر SSH برای بکاپ.

## ساختار

| مسیر | توضیح |
|---|---|
| `cmd/taktaplus` | یک باینری با زیرفرمان‌های `api`، `migrate`، `worker`، `fileserver`، `radius`، `smtpgw`، `collector` |
| `internal/license` | شناسه‌ی نصب، اثرانگشت سخت‌افزار و بررسی لایسنس (با `taktaplus-lic/pkg/licformat`) |
| `internal/license/keys` | کلیدهای **عمومی** مرجع لایسنس که داخل باینری قرار می‌گیرند |
| `internal/api`, `internal/store`, `migrations` | HTTP، PostgreSQL و Migrationها |
| `web/` | رابط وب (React + Ant Design، راست‌به‌چپ) |
| `deploy/` | Docker Compose |

## توسعه

پیش‌نیاز: Go 1.26+، Node.js 22+، Docker.

```sh
cp .env.example .env
make db-up      # PostgreSQL، Redis و ClickHouse روی localhost
make web        # ساخت رابط وب
make run        # http://localhost:8080 (TLS خاموش در .env)
make test lint
```

برای رابط وب با Hot Reload: `cd web && npm run dev`.

اجرای کامل با Docker:

```sh
docker compose -f deploy/docker-compose.yml up -d --build
```
