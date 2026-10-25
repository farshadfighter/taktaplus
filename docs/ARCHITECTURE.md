# معماری TaktaPlus

> وضعیت: **پیش‌نویس برای تأیید** — تا تأیید نهایی، کدی نوشته نمی‌شود.
> سند معماری سرور لایسنس: [`taktaplus-lic/docs/ARCHITECTURE.md`](https://github.com/farshadfighter/taktaplus-lic/blob/main/docs/ARCHITECTURE.md)

## ۱. هدف محصول

TaktaPlus یک نرم‌افزار **On-Premise تحت وب** برای مدیریت متمرکز تجهیزات **FortiGate** و **FortiWeb** در شبکه‌هایی است که به FortiGuard دسترسی ندارند. قابلیت‌ها:

| ماژول | شرح | لایسنس |
|---|---|---|
| A — تجهیزات و مانیتورینگ | ثبت دستگاه، کشف اطلاعات، پایش سلامت، هشدار | سریال دستگاه |
| B — بروزرسانی سیگنیچر | دریافت پکیج از منبع مرکزی/مشتری و نصب روی دستگاه | سریال دستگاه |
| C — بکاپ و ریستور | بکاپ زمان‌بندی‌شده، فوری، خودکار هنگام تغییر، Diff، ریستور | سریال دستگاه |
| D — احراز هویت دو مرحله‌ای پیامکی | OTP پیامکی (sms.ir) برای کاربران FortiGate | تعداد کاربر |
| E — گزارش‌ها و تحلیل لاگ | جمع‌آوری لاگ و داشبورد/گزارش مشابه FortiAnalyzer | سریال دستگاه |

## ۲. تصمیم‌های تأییدشده

| موضوع | تصمیم |
|---|---|
| استقرار | On-Prem، رابط وب، بسته‌ی نصب آفلاین مبتنی بر Docker Compose |
| لایسنس دستگاه | به ازای هر سریال؛ ماژول‌های A، B، C و E را برای آن سریال فعال می‌کند |
| لایسنس 2FA | به ازای تعداد **کاربر تعریف‌شده** |
| مدل زمانی | اشتراکی؛ هر سریال و هر بسته‌ی کاربر انقضای جداگانه دارد |
| HA | هر عضو کلاستر یک لایسنس جدا |
| پس از انقضا | دوره‌ی مهلت (پیش‌فرض ۱۴ روز) با هشدار؛ سپس عملیات متوقف و داده‌های قبلی فقط‌خواندنی |
| فعال‌سازی | آنلاین و آفلاین |
| توزیع سیگنیچر | فقط از طریق TaktaPlus (دستگاه از FTP/TFTP داخلی TaktaPlus می‌گیرد یا با API آپلود می‌شود) |
| منبع پکیج | پیش‌فرض: مخزن مرکزی شرکت با Manifest امضاشده؛ قابل جایگزینی با منبع FTP/TFTP مشتری |
| روش 2FA | هر دو: سرور RADIUS، و دروازه‌ی ایمیل‌به‌پیامک برای کاربران Local فورتی‌گیت |
| سرویس پیامک | sms.ir |
| کانال هشدار | ایمیل، پیامک، پیام‌رسان داخلی (بله/ایتا)، Syslog/Webhook |
| ارتباط با دستگاه | REST API و SSH (هر دو) |
| زبان | Backend: Go — Frontend: React + TypeScript |
| ریپازیتوری | Public می‌ماند؛ هیچ کلید خصوصی یا Secret وارد ریپازیتوری نمی‌شود |

## ۳. فناوری‌ها

| لایه | انتخاب |
|---|---|
| Backend | Go 1.26+، روتر `chi`، دسترسی به DB با `pgx` + `sqlc`، مهاجرت با `goose` |
| صف و زمان‌بندی | Redis + کتابخانه‌ی `asynq` (Job، Retry، Cron) |
| دیتابیس اصلی | PostgreSQL 16 |
| لاگ و گزارش | ClickHouse |
| SSH | `golang.org/x/crypto/ssh` |
| RADIUS | `layeh.com/radius` |
| TFTP / FTP | `pin/tftp` و `fclairamb/ftpserverlib` (داخل خود باینری) |
| SMTP دروازه | `emersion/go-smtp` |
| Frontend | React 19 + TypeScript + Vite + Ant Design (RTL) + ECharts + TanStack Query، تقویم شمسی |
| PDF گزارش | Chromium headless (رندر صفحه‌ی چاپی گزارش) |
| استقرار | Docker Compose؛ ایمیج‌ها به صورت فایل `tar` در بسته‌ی نصب آفلاین |

## ۴. نمای کلی معماری

```
                  ┌──────────────────── TaktaPlus (On-Prem) ────────────────────┐
  Admin ──HTTPS──▶│  web (React, served by api)                                 │
                  │  api ── REST + Auth + RBAC + License Guard                  │
                  │   │                                                         │
                  │   ├── worker  (asynq: backup, signature deploy, polling,    │
                  │   │            reports, notifications, sync)                │
                  │   ├── fileserver (TFTP :69/udp, FTP :21 + passive range)    │
                  │   ├── radius  (:1812/:1813 udp)  ──┐                        │
                  │   ├── smtpgw  (:25/:587)  ─────────┼──▶ sms.ir (HTTPS)       │
                  │   └── collector (syslog :514 udp/tcp, :6514 tls) ─▶ ClickHouse│
                  │                                                             │
                  │  PostgreSQL · Redis · ClickHouse · /data (encrypted files)  │
                  └──────▲──────────────▲───────────────▲──────────────▲────────┘
            REST / SSH   │  TFTP / FTP  │  RADIUS / SMTP│        Syslog│
                  ┌──────┴──────────────┴───────────────┴──────────────┴────────┐
                  │                FortiGate / FortiWeb devices                 │
                  └─────────────────────────────────────────────────────────────┘

  Package Source ──sync──▶ fileserver repo     License Server ◀──activate/heartbeat── api
  (central / customer FTP·TFTP·SFTP·HTTP / upload)       (online or offline file)
```

همه‌ی سرویس‌های Go از **یک باینری** با زیرفرمان ساخته می‌شوند (`taktaplus api|worker|radius|smtpgw|collector|fileserver`) تا نسخه و کد لایسنس یکسان باشد؛ در Compose هر کدام کانتینر جدا دارند.

### پورت‌ها

| پورت | سرویس | جهت |
|---|---|---|
| 443/tcp | پنل وب و API | Admin → TaktaPlus |
| 69/udp | TFTP | دستگاه → TaktaPlus |
| 21/tcp + 30000-30100/tcp | FTP (passive) | دستگاه → TaktaPlus |
| 1812, 1813/udp | RADIUS | دستگاه → TaktaPlus |
| 25 یا 587/tcp | دروازه‌ی SMTP | دستگاه → TaktaPlus |
| 514/udp,tcp — 6514/tcp | Syslog | دستگاه → TaktaPlus |
| 443/tcp, 22/tcp | REST / SSH | TaktaPlus → دستگاه |
| 443/tcp | sms.ir، سرور لایسنس، مخزن پکیج | TaktaPlus → اینترنت (مستقیم یا از طریق Proxy؛ اختیاری) |

> اگر سرور TaktaPlus هیچ راهی به اینترنت نداشته باشد، ارسال پیامک (ماژول D) ممکن نیست؛ برای این حالت تنظیم HTTP Proxy خروجی فقط به `api.sms.ir` پیش‌بینی می‌شود.

## ۵. ساختار ریپازیتوری

```
taktaplus/
├── cmd/taktaplus/            # main؛ زیرفرمان‌ها: api, worker, radius, smtpgw, collector, fileserver, migrate
├── internal/
│   ├── api/                  # هندلرهای HTTP، middleware، OpenAPI
│   ├── auth/                 # ورود، Session/JWT، 2FA ادمین، RBAC
│   ├── audit/                # لاگ ممیزی
│   ├── config/               # تنظیمات برنامه
│   ├── crypto/               # رمزنگاری Secretها (AES-256-GCM، کلید اصلی)
│   ├── device/
│   │   ├── driver.go         # اینترفیس مشترک درایور
│   │   ├── fortigate/        # پیاده‌سازی API + SSH
│   │   └── fortiweb/
│   ├── inventory/            # ماژول A
│   ├── monitor/              # ماژول A — پایش و هشدار
│   ├── backup/               # ماژول C
│   ├── signature/            # ماژول B — کاتالوگ و استقرار
│   ├── repository/           # منابع پکیج و همگام‌سازی
│   ├── fileserver/           # TFTP / FTP
│   ├── otp/
│   │   ├── radius/           # ماژول D — RADIUS
│   │   ├── smtpgw/           # ماژول D — ایمیل به پیامک
│   │   └── users/
│   ├── sms/smsir/            # کلاینت sms.ir
│   ├── notify/               # ایمیل، پیامک، بله/ایتا، Webhook، Syslog
│   ├── logs/                 # ماژول E — collector و parser
│   ├── reports/              # ماژول E — داشبورد و گزارش
│   ├── license/              # License Guard (استفاده از taktaplus-lic/pkg/licformat)
│   ├── jobs/                 # تعریف Jobها و Scheduler
│   └── store/                # کوئری‌های sqlc، مخزن فایل
├── migrations/               # PostgreSQL و ClickHouse
├── web/                      # React
├── deploy/
│   ├── docker-compose.yml
│   ├── installer/            # اسکریپت نصب/ارتقای آفلاین
│   └── fortinet-snippets/    # نمونه کانفیگ لازم روی دستگاه‌ها
└── docs/
```

## ۶. هسته‌ی مشترک

- **کاربران پنل و RBAC:** نقش‌های پیش‌فرض `SuperAdmin`، `Admin`، `Operator`، `Viewer`، و محدودسازی دسترسی به گروه‌های دستگاه.
- **ورود ادمین:** رمز (Argon2id) + 2FA اجباری‌پذیر (TOTP یا پیامک). اتصال LDAP/AD اختیاری.
- **لاگ ممیزی:** هر عملیات (کاربر، زمان، IP، شیء، قبل/بعد) غیرقابل حذف از پنل.
- **Secretها:** اطلاعات ورود دستگاه، کلید sms.ir و رمز FTP با AES-256-GCM و یک کلید اصلی رمزنگاری می‌شوند. کلید اصلی هنگام نصب ساخته و خارج از دیتابیس (فایل با دسترسی محدود) نگه داشته می‌شود.
- **عملیات حساس** (ریستور، استقرار سیگنیچر روی همه، حذف دستگاه) نیازمند تأیید مجدد رمز/OTP ادمین.
- **زبان:** فارسی (RTL) و انگلیسی؛ نمایش تاریخ شمسی و میلادی.

## ۷. لایه‌ی درایور دستگاه

همه‌ی ماژول‌ها فقط با این اینترفیس کار می‌کنند و از جزئیات API/SSH بی‌خبرند:

```go
type Driver interface {
    Probe(ctx) (DeviceInfo, error)            // مدل، سریال، فرم‌ور، HA، VDOM
    Health(ctx) (HealthSnapshot, error)       // CPU, RAM, Session, Interface
    SignatureStatus(ctx) ([]SignatureVersion, error)
    BackupConfig(ctx, scope) (io.Reader, error)
    RestoreConfig(ctx, cfg io.Reader) error
    InstallPackage(ctx, pkg Package, via Transport) error  // API upload یا pull از TFTP/FTP
    ApplyConfig(ctx, snippet ConfigSnippet) error          // تنظیم syslog, radius, sms-server
    ListLocalUsers(ctx) ([]LocalUser, error)
}
```

- هر قابلیت ابتدا با **REST API** و در صورت عدم دسترسی/پشتیبانی با **SSH** اجرا می‌شود؛ ترتیب برای هر دستگاه قابل تنظیم است.
- مسیرهای API بین نسخه‌های FortiOS / FortiWeb OS متفاوت‌اند؛ درایور یک **جدول قابلیت بر اساس نسخه‌ی فرم‌ور** دارد. نمونه برای FortiGate:

| عمل | REST API | SSH (CLI) |
|---|---|---|
| اطلاعات | `GET /api/v2/monitor/system/status` | `get system status` |
| منابع | `GET /api/v2/monitor/system/resource/usage` | `get system performance status` |
| نسخه‌ی سیگنیچرها | `GET /api/v2/monitor/license/status` | `diagnose autoupdate versions` |
| بکاپ | `/api/v2/monitor/system/config/backup` | `execute backup config tftp/ftp ...` |
| ریستور | `POST /api/v2/monitor/system/config/restore` | `execute restore config tftp/ftp ...` |
| نصب سیگنیچر | `POST /api/v2/monitor/system/fortiguard/manual-update` | `execute restore ips|av ... tftp/ftp ...` |

> جزئیات دقیق هر Endpoint در زمان پیاده‌سازی برای هر نسخه‌ی پشتیبانی‌شده تست و در جدول قابلیت ثبت می‌شود. حداقل نسخه‌های هدف: FortiOS 7.0+ و FortiWeb 7.0+.

## ۸. ماژول A — تجهیزات و مانیتورینگ

- ثبت دستگاه: نوع، آدرس، پورت، API Token، کاربر/رمز یا کلید SSH، گروه، برچسب.
- **Probe** هنگام ثبت: سریال باید در لایسنس باشد؛ در غیر این صورت دستگاه با وضعیت «بدون لایسنس» ثبت می‌شود و هیچ عملیاتی اجرا نمی‌شود.
- اعضای HA جدا کشف و نمایش داده می‌شوند (هر سریال لایسنس خودش).
- **پایش دوره‌ای** (پیش‌فرض هر ۶۰ ثانیه): دسترس‌پذیری، CPU، RAM، Disk، Session، وضعیت اینترفیس‌ها، وضعیت HA، انقضای قراردادهای FortiGuard، عقب‌ماندگی نسخه‌ی سیگنیچر از مخزن.
- **قوانین هشدار:** شرط + آستانه + مدت + کانال‌ها (ایمیل، پیامک، بله/ایتا، Webhook، Syslog) + جلوگیری از تکرار.
- داده‌ی زمانی سلامت در ClickHouse (جدول `device_metrics`) نگه داشته می‌شود.

## ۹. ماژول B — بروزرسانی سیگنیچر

### ۹.۱ منابع پکیج

| نوع منبع | توضیح |
|---|---|
| مخزن مرکزی شرکت (پیش‌فرض) | HTTPS، با احراز هویت مبتنی بر لایسنس، دارای Manifest امضاشده |
| FTP / SFTP / TFTP مشتری | منبع داخلی سازمان؛ بدون Manifest |
| HTTP(S) دلخواه | اختیاری |
| آپلود دستی / بسته‌ی آفلاین | فایل `zip` شامل Manifest امضاشده + پکیج‌ها (مثلاً با فلش) |

همگام‌سازی زمان‌بندی‌شده (یا دستی) پکیج‌ها را در مخزن داخلی TaktaPlus (`/data/repo`) کپی می‌کند. پکیج‌ها با `sha256` آدرس‌دهی و بررسی می‌شوند.

### ۹.۲ Manifest

```json
{
  "format": 1,
  "published_at": "2026-10-25T08:00:00Z",
  "packages": [
    {
      "file": "fgt-ips-db-28.950.pkg",
      "sha256": "…",
      "size": 1234567,
      "platform": "fortigate",
      "type": "ips_db",
      "version": "28.950",
      "models": ["FGT60F", "FGT100F", "*"],
      "firmware": { "min": "7.0.0", "max": "7.6.99" },
      "released_at": "2026-10-24T00:00:00Z"
    }
  ]
}
```

- `manifest.json` با کلید Ed25519 **انتشار پکیج** (جدا از کلید لایسنس) امضا می‌شود: `manifest.sig`.
- انواع پیشنهادی: FortiGate — `ips_db`, `ips_engine`, `av_db`, `av_engine`, `appctrl_db`, `botnet_db`, `isdb`, `ot_db`؛ FortiWeb — `waf_signature`, `av_db`, `ip_reputation`, `geo_db`. فهرست نهایی قابل گسترش است.
- برای منبع مشتری (بدون Manifest)، نوع و نسخه از الگوی نام فایل حدس زده می‌شود و ادمین می‌تواند در پنل اصلاح/برچسب‌گذاری کند.

### ۹.۳ جریان استقرار

```
انتخاب دستگاه‌ها/گروه + پکیج(ها)  →  بررسی لایسنس سریال  →  بررسی سازگاری (مدل/فرم‌ور)
  →  بکاپ خودکار کانفیگ  →  نصب
        ├─ روش API:    آپلود فایل با manual-update
        └─ روش Pull:   فرمان CLI روی دستگاه تا فایل را از TFTP/FTP داخلی TaktaPlus بگیرد
  →  بررسی نسخه‌ی جدید با SignatureStatus  →  ثبت نتیجه + اعلان
```

- اجرای مرحله‌ای: «دسته‌ی آزمایشی» و سپس بقیه، با حداکثر هم‌زمانی قابل تنظیم و توقف خودکار در صورت خطای بیش از حد.
- زمان‌بندی: یک‌باره یا خودکار (مثلاً «هر شب ساعت ۲ آخرین نسخه‌ی سازگار را نصب کن»).
- سرور FTP داخلی برای هر Job یک **حساب موقت فقط‌خواندنی** با رمز یک‌بارمصرف می‌سازد. TFTP فقط به IP دستگاه‌های ثبت‌شده پاسخ می‌دهد.
- محدودیت: دستگاه باید خودش قرارداد FortiGuard معتبر برای آن سرویس داشته باشد؛ TaktaPlus این را دور نمی‌زند و فقط وضعیت را گزارش می‌دهد.

## ۱۰. ماژول C — بکاپ و ریستور

- **انواع بکاپ:** زمان‌بندی‌شده (Cron برای هر دستگاه/گروه)، فوری (دکمه)، خودکار هنگام تغییر کانفیگ (تشخیص از لاگ رویداد تغییر کانفیگ در Syslog، با Debounce مثلاً ۵ دقیقه)، و قبل از هر عملیات خطرناک.
- **ذخیره:** فایل رمزنگاری‌شده در `/data/backups/{device}/{timestamp}.conf.enc` + متادیتا در PostgreSQL (hash، اندازه، نسخه‌ی فرم‌ور، کاربر/علت).
- اگر محتوای کانفیگ با بکاپ قبلی یکی باشد (بدون در نظر گرفتن خطوط متغیر مثل `#conf_file_ver`)، نسخه‌ی تکراری ثبت نمی‌شود.
- **نگهداری:** بر اساس تعداد و/یا مدت، با امکان «قفل» یک نسخه.
- **Diff:** مقایسه‌ی دو نسخه به صورت ساختاریافته بر اساس بلاک‌های `config/edit`.
- **ارسال نسخه‌ی دوم:** SFTP / FTP / SMB بیرونی (اختیاری).
- **ریستور:** انتخاب نسخه → نمایش Diff با کانفیگ فعلی → تأیید مجدد ادمین → بکاپ از وضعیت فعلی → ریستور (API یا SSH) → انتظار برای ریبوت → Probe مجدد.
- **رمزهای داخل کانفیگ:** فایل کامل نگه داشته می‌شود؛ نمایش در پنل با ماسک کردن مقادیر `ENC` / `set password`.

## ۱۱. ماژول D — احراز هویت دو مرحله‌ای پیامکی

### ۱۱.۱ حالت RADIUS

```
User ──VPN/Admin login──▶ FortiGate ──Access-Request(user, password)──▶ TaktaPlus RADIUS
                                                      بررسی رمز (محلی یا LDAP/AD)
                                                      ارسال OTP با sms.ir
        FortiGate ◀──Access-Challenge(State, "کد پیامک‌شده را وارد کنید")──
User ──OTP──▶ FortiGate ──Access-Request(user, OTP, State)──▶ TaktaPlus
        FortiGate ◀──Access-Accept (+ Fortinet-Group-Name)──
```

- کاربران در TaktaPlus تعریف می‌شوند، یا از کاربران Local فورتی‌گیت (با `ListLocalUsers`) یا LDAP/AD **وارد** می‌شوند. منبع رمز برای هر کاربر: محلی (Argon2id) یا LDAP/AD.
- روش احراز هویت RADIUS باید **PAP** باشد (Challenge-Response با MS-CHAPv2 سازگار نیست). ارتباط با Shared Secret قوی و محدود به IP دستگاه‌های ثبت‌شده.
- تنظیمات لازم روی فورتی‌گیت (شیء `user radius` و گروه کاربری) از پنل قابل اعمال خودکار است.
- سیاست OTP: طول کد، اعتبار (پیش‌فرض ۱۲۰ ثانیه)، حداکثر تلاش، محدودیت ارسال در دقیقه، قفل موقت.
- امکان «دستگاه/IP مورد اعتماد» و کد پشتیبان یک‌بارمصرف (اختیاری).

### ۱۱.۲ حالت دروازه‌ی ایمیل‌به‌پیامک (کاربران Local فورتی‌گیت)

FortiGate برای کاربران Local با `two-factor sms` و `sms-server custom` پیامک را به صورت ایمیل به آدرس `<phone>@<mail-server>` می‌فرستد.

```
FortiGate ──SMTP: to 0912xxxxxxx@sms.taktaplus──▶ TaktaPlus smtpgw ──▶ sms.ir ──▶ کاربر
                 (سایر ایمیل‌های فورتی‌گیت)  ──relay──▶ SMTP اصلی سازمان
```

- روی FortiGate: `config system email-server` به TaktaPlus، و `config system sms-server` با `mail-server` برابر دامنه‌ی دروازه. TaktaPlus این تنظیمات را با `ApplyConfig` اعمال می‌کند.
- ایمیل‌های غیرپیامکی (هشدارهای خود فورتی‌گیت) به SMTP واقعی سازمان Relay می‌شوند تا چیزی از دست نرود.
- پذیرش ایمیل فقط از IP دستگاه‌های ثبت‌شده و فقط برای شماره‌هایی که متعلق به کاربران شمارش‌شده هستند.

### ۱۱.۳ شمارش لایسنس کاربر

- RADIUS: تعداد کاربران فعال تعریف‌شده در TaktaPlus.
- دروازه: تعداد کاربران یکتای دارای `two-factor sms` روی همه‌ی فورتی‌گیت‌ها (همگام‌سازی دوره‌ای با `ListLocalUsers`).
- مجموع هر دو حالت ≤ ظرفیت لایسنس. با رسیدن به سقف، کاربر جدید فعال نمی‌شود و هشدار ارسال می‌شود.

### ۱۱.۴ sms.ir

- ارسال با API «ارسال سریع/Verify» (قالب و پارامتر) با `x-api-key`؛ تنظیم قالب در پنل.
- ثبت وضعیت تحویل، شمارش مصرف و هشدار کاهش اعتبار پنل پیامک.
- طراحی با اینترفیس `SMSProvider` تا بعداً سرویس‌دهنده‌ی دیگری اضافه شود.

## ۱۲. ماژول E — گزارش‌ها و تحلیل لاگ

- **دریافت:** FortiGate/FortiWeb با Syslog (ترجیحاً TCP یا TLS، فرمت `default`) به collector می‌فرستند. پیکربندی Syslog روی دستگاه از پنل اعمال می‌شود.
- لاگ‌های سریال‌های بدون لایسنس (فیلد `devid`) دور ریخته و شمارش می‌شوند.
- **Parser:** تبدیل `key=value` فورتی به رکورد ساختاریافته؛ درج دسته‌ای (Batch) در ClickHouse.
- **جداول ClickHouse:** `log_traffic`، `log_utm` (IPS، AV، WebFilter، AppCtrl، DNS)، `log_event` (سیستم، VPN، کاربر، HA، تغییر کانفیگ)، `log_fortiweb_attack`، `log_fortiweb_traffic`، `device_metrics`. پارتیشن روزانه، TTL قابل تنظیم برای هر جدول، و Materialized View برای تجمیع ساعتی/روزانه.
- **داشبوردها:** خلاصه‌ی امنیتی، Top Source / Destination / Application / Country، پهنای باند، تهدیدها، WebFilter، کاربران و نشست‌های VPN، رویدادهای سیستمی، حملات WAF.
- **Log View:** جستجو و فیلتر با بازه‌ی زمانی، Drill-down از داشبورد، خروجی CSV.
- **گزارش‌ها:** قالب‌های آماده + قالب سفارشی (انتخاب ویجت‌ها)، زمان‌بندی روزانه/هفتگی/ماهانه، خروجی PDF و Excel، ارسال ایمیلی.
- **ظرفیت:** حجم دیسک و نرخ دریافت (EPS) در داشبورد سیستم نمایش داده می‌شود؛ نیازمندی سخت‌افزار در سند نصب بر اساس EPS اعلام می‌شود.

## ۱۳. License Guard (سمت برنامه)

- فرمت فایل لایسنس و بررسی امضا از پکیج مشترک `github.com/farshadfighter/taktaplus-lic/pkg/licformat`.
- **شناسه‌ی نصب:** هنگام نصب یک جفت‌کلید Ed25519 برای این نصب ساخته می‌شود؛ شناسه‌ی نصب = هش کلید عمومی. اثرانگشت سخت‌افزار (machine-id میزبان، UUID سخت‌افزار از DMI، MAC) جداگانه در درخواست فرستاده می‌شود و تغییر یکی از سه جزء تحمل می‌شود. در Docker، `/etc/machine-id` و `/sys/class/dmi/id` میزبان به صورت فقط‌خواندنی به کانتینر داده می‌شوند.
- **فعال‌سازی آنلاین:** ارسال درخواست امضاشده با کلید نصب به سرور لایسنس، دریافت فایل لایسنس.
- **فعال‌سازی آفلاین:** دانلود فایل درخواست (`.tpreq`) از پنل ← آپلود در پورتال ← دریافت فایل لایسنس (`.tplic`) ← آپلود در پنل.
- **Heartbeat** (در صورت اتصال، روزانه): دریافت لایسنس به‌روز، اعمال ابطال، گزارش تعداد مصرف.
- **بررسی‌ها:** امضا، تطابق شناسه‌ی نصب، تاریخ‌ها، و تشخیص عقب کشیدن ساعت سیستم (ذخیره‌ی آخرین زمان دیده‌شده).
- **اعمال:** هر Job و هر درخواست API مرتبط با دستگاه، وضعیت لایسنس آن سریال را بررسی می‌کند: `active` / `grace` / `expired` / `unlicensed`. در `expired` فقط مشاهده و دانلود داده‌های قبلی مجاز است.
- هشدار انقضا ۳۰، ۱۴ و ۷ روز قبل.

## ۱۴. مدل داده (PostgreSQL — جداول اصلی)

| جدول | محتوا |
|---|---|
| `users`, `roles`, `user_roles`, `sessions` | کاربران پنل و دسترسی |
| `audit_log` | ممیزی |
| `settings`, `secrets` | تنظیمات و Secretهای رمزنگاری‌شده |
| `license_state` | فایل لایسنس فعلی، وضعیت، آخرین Heartbeat |
| `device_groups`, `devices`, `device_credentials` | تجهیزات |
| `device_status` | آخرین وضعیت سلامت و نسخه‌ی سیگنیچرها |
| `alert_rules`, `alerts`, `notification_channels` | هشدار و اعلان |
| `backup_policies`, `backups` | بکاپ |
| `restore_jobs` | ریستور |
| `package_sources`, `packages`, `package_sync_runs` | منابع و کاتالوگ پکیج |
| `deployments`, `deployment_targets` | استقرار سیگنیچر |
| `otp_users`, `otp_policies`, `otp_events`, `radius_clients` | ماژول D |
| `sms_providers`, `sms_messages` | پیامک |
| `report_templates`, `report_schedules`, `report_runs` | گزارش‌ها |
| `jobs` | تاریخچه‌ی Jobها (کنار asynq) |

## ۱۵. API

- REST با پیشوند `/api/v1`، مستند با OpenAPI 3 (تولید خودکار کلاینت TypeScript برای Frontend).
- گروه‌ها: `auth`, `users`, `audit`, `license`, `devices`, `monitor`, `alerts`, `backups`, `packages`, `sources`, `deployments`, `otp`, `sms`, `logs`, `reports`, `settings`, `system`.
- رویدادهای زنده (پیشرفت Job، هشدار) با Server-Sent Events.
- API Token برای یکپارچه‌سازی با سیستم‌های دیگر سازمان.

## ۱۶. امنیت

- HTTPS اجباری (گواهی خودامضا هنگام نصب، قابل جایگزینی).
- Secretها رمزنگاری‌شده؛ هیچ Secretی در لاگ چاپ نمی‌شود.
- اعتبارسنجی گواهی TLS دستگاه‌ها با امکان Pin کردن گواهی خودامضای هر دستگاه.
- TFTP/FTP/RADIUS/SMTP/Syslog فقط به IP دستگاه‌های ثبت‌شده پاسخ می‌دهند.
- Rate limit روی ورود پنل و OTP.
- **ریپازیتوری Public:** هیچ کلید خصوصی، توکن یا Secret در کد نیست؛ کلیدهای عمومی امضا (لایسنس و Manifest) با شناسه‌ی کلید (`kid`) در کد قرار می‌گیرند تا چرخش کلید ممکن باشد.

## ۱۷. نصب و ارتقا

- بسته‌ی آفلاین: `taktaplus-<version>.tar.gz` شامل ایمیج‌های Docker، `docker-compose.yml`، اسکریپت نصب، و امضای بسته.
- پیش‌نیاز: Linux x86_64 با Docker Engine + Compose plugin. (در آینده: OVA آماده.)
- ارتقا: بارگذاری بسته‌ی جدید → بررسی امضا → بکاپ خودکار دیتابیس → اجرای Migration → راه‌اندازی مجدد.
- بکاپ از خود TaktaPlus (دیتابیس + فایل‌ها) زمان‌بندی‌شده.

## ۱۸. فازبندی

| فاز | محتوا | خروجی |
|---|---|---|
| ۰ | اسکلت هر دو ریپازیتوری، CI، `licformat`، Compose توسعه | قابل اجرا بدون قابلیت |
| ۱ | هسته (ورود، RBAC، ممیزی)، سرور لایسنس پایه (ادمین، صدور، فعال‌سازی آنلاین/آفلاین)، License Guard، ماژول A، ماژول C | نسخه‌ی قابل استفاده‌ی اول |
| ۲ | ماژول B + انتشار پکیج و Manifest در سرور لایسنس | بروزرسانی سیگنیچر |
| ۳ | ماژول D (RADIUS، دروازه‌ی SMTP، sms.ir) | 2FA |
| ۴ | ماژول E | گزارش‌ها |
| ۵ | پورتال مشتری و نماینده در سرور لایسنس، هشدار بله/ایتا، تکمیل گزارش‌ها | تکمیل محصول |

## ۱۹. موارد باز

- فهرست دقیق مدل‌ها و نسخه‌های فرم‌وری که در نسخه‌ی اول پشتیبانی می‌شوند (برای تست).
- فهرست نهایی انواع پکیج سیگنیچر FortiWeb.
- قالب پیامک‌ها در sms.ir.
- مدت پیش‌فرض نگهداری لاگ و بکاپ.
