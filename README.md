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

فاز ۲:

- **منابع پکیج:** مخزن مرکزی (با امضای کلید نصب و بررسی امضای Manifest)، آینه‌ی HTTP، و FTP/SFTP/TFTP مشتری؛ همگام‌سازی زمان‌بندی‌شده، بسته‌ی آفلاین و بارگذاری فایل.
- **سرور TFTP/FTP داخلی:** دستگاه‌ها فقط فایل‌های مجاز را، فقط از IP خودشان و با حساب FTP یک‌بارمصرف دریافت می‌کنند.
- **گردش کار تغییرات:** پیش‌نویس، تأیید (۰ تا ۲ تأییدکننده برای هر گروه؛ درخواست‌دهنده تأییدکننده نیست)، پنجره‌ی نگهداری یا اجرای فوری با دلیل، بکاپ قبل از هر دستگاه، دسته‌ی آزمایشی و توقف پس از خطا.
- **ماژول B:** استقرار پکیج مشخص یا جدیدترین نسخه‌ی سازگار با REST API یا دریافت دستگاه از TFTP/FTP داخلی، و بررسی نسخه پس از نصب.
- **F1:** اسکریپت CLI با قالب و متغیرهای سراسری/گروه/دستگاه؛ اسکریپت فقط‌خواندنی بدون تأیید اجرا می‌شود.
- **F2:** ارتقای فرم‌ور با محاسبه‌ی مسیر مرحله‌ای، بررسی سلامت پیش از ارتقا و انتظار برای بالا آمدن دستگاه بعد از هر مرحله.

فاز ۳:

- **ماژول D:** احراز هویت دو مرحله‌ای پیامکی با sms.ir (ارسال سریع با قالب)؛ کاربران محلی یا LDAP/AD؛ سرور RADIUS با Challenge پیامکی و Message-Authenticator اجباری؛ دروازه‌ی ایمیل‌به‌پیامک برای کاربران Local فورتی‌گیت؛ شمارش کاربران در لایسنس.

فاز ۴:

- **ماژول E:** دریافت Syslog (UDP، TCP با قاب‌بندی RFC 6587، TLS) از FortiGate و FortiWeb؛ رد لاگ سریال‌های ثبت‌نشده، بدون لایسنس یا از آدرس نامعتبر (با فهرست برای اقدام مدیر)؛ ذخیره در ClickHouse با نگهداری ۹۰ روز لاگ خام و یک سال خلاصه‌های ساعتی (قابل تغییر)؛ کشور از خود لاگ یا پایگاه MMDB آفلاین (از مخزن پکیج با نوع `geoip_mmdb` یا بارگذاری دستی).
- **تحلیل امنیتی:** داشبوردهای امنیت، ترافیک، WebFilter، VPN، حملات WAF و رویدادها با Drill-down به نمایشگر لاگ.
- **نمایشگر لاگ:** جستجو با فیلتر روی هر فیلد لاگ، جزئیات کامل رکورد و خروجی CSV.
- **گزارش‌ها:** قالب‌های آماده و سفارشی، اجرای دستی یا زمان‌بندی روزانه/هفتگی/ماهانه‌ی شمسی، خروجی PDF (Chromium headless) و Excel و ارسال با کانال ایمیل هشدارها.
- **پیکربندی دستگاه:** تولید CLI ارسال لاگ برای FortiGate و FortiWeb و ذخیره به عنوان اسکریپت برای اعمال از طریق گردش کار تغییرات.

> درایورها بر اساس مستندات FortiOS/FortiWeb و با سرورهای شبیه‌سازی‌شده تست شده‌اند و باید روی دستگاه واقعی تأیید شوند؛ مسیرهای API فورتی‌وب (`/api/v2.0/...`) قطعیت کمتری دارند و بکاپ فورتی‌وب فعلاً از طریق SSH است. نصب سیگنیچر روی فورتی‌وب مثل بارگذاری در GUI با REST API انجام می‌شود؛ مسیر آن (`TAKTAPLUS_FORTIWEB_UPLOAD_PATH`، پیش‌فرض `/api/v2.0/system/maintenance.manualupdate`) باید با درخواستی که GUI هنگام بارگذاری می‌فرستد (در DevTools مرورگر) تطبیق داده شود. دستورهای `execute restore ips|av|image` فورتی‌گیت و پاسخ `y` به پرسش تأیید هم باید روی دستگاه واقعی تأیید شوند.

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
| `TAKTAPLUS_FILESERVER_IP` | آدرسی که دستگاه‌ها با آن به TaktaPlus می‌رسند (برای دریافت پکیج و فرم‌ور با TFTP/FTP) |
| `TAKTAPLUS_TFTP_LISTEN`, `TAKTAPLUS_FTP_LISTEN` | پیش‌فرض `:6969` و `:2121` (در Compose روی 69/udp و 21 منتشر می‌شوند)؛ `off` برای غیرفعال کردن |
| `TAKTAPLUS_FTP_PASSIVE_PORTS`, `TAKTAPLUS_FTP_PUBLIC_PORT` | بازه‌ی پورت Passive (پیش‌فرض `30000-30009`) و پورت FTP از دید دستگاه |
| `TAKTAPLUS_RADIUS_LISTEN`, `TAKTAPLUS_SMTP_LISTEN` | سرور RADIUS (پیش‌فرض `:1812` UDP) و دروازه‌ی ایمیل‌به‌پیامک (پیش‌فرض `:2525`، در Compose روی 25)؛ `off` برای غیرفعال کردن |
| `TAKTAPLUS_CLICKHOUSE_URL` | ذخیره‌ساز لاگ، مثل `http://user:pass@clickhouse:8123/taktaplus`؛ خالی یعنی ماژول E غیرفعال |
| `TAKTAPLUS_SYSLOG_UDP`, `TAKTAPLUS_SYSLOG_TCP`, `TAKTAPLUS_SYSLOG_TLS` | دریافت Syslog، پیش‌فرض `:5514` (UDP و TCP، در Compose روی 514) و `:6514` (TLS با همان گواهی پنل)؛ `off` برای غیرفعال کردن |
| `TAKTAPLUS_TIMEZONE` | منطقه‌ی زمانی زمان‌بندی و تاریخ گزارش‌ها، پیش‌فرض `Asia/Tehran` |
| `TAKTAPLUS_CHROMIUM` | مرورگر چاپ PDF؛ خالی یعنی جستجوی `chromium-browser`/`chromium` در PATH (در ایمیج Docker نصب است) |

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
