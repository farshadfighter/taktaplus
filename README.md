# taktaplus

نرم‌افزار جامع مدیریت متمرکز تجهیزات FortiGate و FortiWeb: افزودن و مانیتورینگ
دستگاه‌ها، بکاپ/ریستور، آپدیت آفلاین سیگنیچر/فرم‌ور، مانیتورینگ SNMP، و
احراز هویت دو مرحله‌ای پیامکی (RADIUS).

## فازبندی توسعه

- **فاز ۰** — زیرساخت پایه: احراز هویت/RBAC، Audit Log، سیستم
  لایسنسینگ (فعال‌سازی + heartbeat آنلاین + enforcement)، مدیریت کلید
  رمزنگاری، اسکلت آپدیت خودِ نرم‌افزار، بکاپ خودکار دیتابیس داخلی، ابزار
  بسته تشخیصی.
- **فاز ۱** — مدیریت دستگاه‌ها: افزودن/حذف/لیست FortiGate و
  FortiWeb، رمزنگاری credential ها، تست اتصال زنده، و enforcement جدا برای
  سقف تعداد هر نوع دستگاه طبق لایسنس.
- **فاز ۲** — بکاپ/ریستور: بکاپ دستی و زمان‌بندی‌شده (شبانه) با
  نگهداری (retention) قابل‌تنظیم، محتوای رمزنگاری‌شده در دیتابیس، دانلود
  بکاپ خام، و اعتبارسنجی پیش از ریستور (تطبیق سریال/فرم‌ور با هشدار قابل‌عبور
  با `force`).
- **فاز ۳** — مانیتورینگ SNMP (v2c) + هشداردهی + گزارش Compliance:
  poll دوره‌ای CPU/حافظه/نشست، دریافت trap زنده (فرآیند مستقل)، هشدار
  ایمیل/تلگرام روی رویدادهای قطعی/عبور از آستانه/trap، و گزارش وضعیت فلیت
  (اتصال، آخرین بکاپ، متریک‌های زنده، هشدارهای باز).
- **فاز ۴** — توزیع آفلاین سیگنیچر و فرم‌ور: منبع FTP (پیش‌فرض
  ارائه‌دهنده یا سفارشی مشتری) با همگام‌سازی خودکار شبانه و دستی، مدیریت
  بسته‌ها (آپلود دستی هم پشتیبانی می‌شود)، پوش به FortiGate از طریق یک
  FTP relay داخلی (فرآیند مستقل) با فرمان CLI روی SSH برای سیگنیچر و REST
  API برای فرم‌ور، پوش مستقیم HTTP به FortiWeb، تاریخچه پوش با health-check
  خودکار پس از هر پوش.
- **فاز ۵ (این نسخه)** — احراز هویت دو مرحله‌ای پیامکی از طریق RADIUS: یک
  سرور RADIUS مستقل (PAP) که taktaplus را جایگزین/تکمیل‌کننده بک‌اند
  احراز هویت ورود ادمین، SSL VPN و IPsec VPN فورتی‌گیت می‌کند - رمز اصلی
  توسط خودِ taktaplus نگهداری و بررسی می‌شود، سپس یک کد پیامکی به‌عنوان
  عامل دوم درخواست می‌شود. یک الگوریتم واحد (بدون فلگ per-NAS) هم جریان
  challenge/response (ادمین/SSL VPN) و هم جریان concatenated
  password+OTP (IPsec) را پوشش می‌دهد. سرویس پیامکی کاملاً توسط مشتری
  پیکربندی می‌شود (کاوه‌نگار تأییدشده + یک مسیر HTTP سفارشی برای هر
  ارائه‌دهنده دیگر) - برخلاف منبع FTP فاز ۴، هیچ حساب پیش‌فرضی وجود ندارد.
  جزئیات کامل در `docs/radius-2fa.md`.

## Stack

- **Backend**: FastAPI, SQLAlchemy 2.0, Alembic, PostgreSQL, Celery+Redis
- **Frontend**: React 18, TypeScript, Vite (RTL از پایه)
- **رمزنگاری**: AES-256-GCM سطح اپلیکیشن برای credential ها (`app/core/security.py`)
- **لایسنسینگ**: اتصال آنلاین به License Server (محصول/ریپوی جدا، بعداً ساخته می‌شود) — قرارداد API در `docs/licensing.md`

## راه‌اندازی سریع (Docker)

```bash
cp .env.example .env
# TAKTAPLUS_JWT_SECRET_KEY رو با یه مقدار تصادفی طولانی پر کن

docker compose up --build
docker compose run --rm backend python -m app.core.security --init   # ساخت کلید رمزنگاری
docker compose exec backend alembic upgrade head
docker compose exec backend python -m app.db.seed                    # ساخت ادمین اولیه
```

Backend: http://localhost:8000/docs · Frontend: http://localhost:5173 ·
Mock License Server (فقط برای توسعه محلی): http://localhost:8100

برای فعال‌سازی لایسنس در محیط dev از کد `DEMO-0001` استفاده کن (تعریف‌شده در
`dev/mock-license-server/app.py`).

## راه‌اندازی محلی (بدون Docker)

```bash
cd backend
python3 -m venv .venv && source .venv/bin/activate
pip install -r requirements-dev.txt
export TAKTAPLUS_JWT_SECRET_KEY="..."
export TAKTAPLUS_DATABASE_URL="postgresql+psycopg://taktaplus:taktaplus@localhost:5432/taktaplus"
export TAKTAPLUS_MASTER_KEY_PATH="/tmp/taktaplus-master.key"
python -m app.core.security --init
alembic upgrade head
python -m app.db.seed
uvicorn app.main:app --reload
```

## تست‌ها

```bash
cd backend
source .venv/bin/activate
pytest
```

تست‌ها روی SQLite درون‌حافظه‌ای اجرا می‌شن (نیازی به Postgres واقعی نیست)؛
تولید همیشه روی Postgres اجرا می‌شه.

## ساختار مخزن

```
backend/app/
  core/        تنظیمات، JWT، رمزنگاری AES-256-GCM، مدیریت کلید
  db/          session، declarative base، model registry، انواع cross-dialect، seed
  domains/     یک پکیج به ازای هر بخش:
               identity (کاربران/نقش‌های خودِ taktaplus)، audit (لاگ hash-chained)،
               licensing (فعال‌سازی/heartbeat/enforcement)، devices (مدیریت
               FortiGate/FortiWeb + درایورهای هر vendor)، backups (بکاپ/ریستور
               رمزنگاری‌شده + اعتبارسنجی پیش از ریستور)، monitoring (SNMP
               poller + trap classification + alert ها)، alerting (اعلان
               ایمیل/تلگرام)، reporting (گزارش وضعیت فلیت)، distribution
               (منبع FTP، مدیریت بسته‌ها، پوش سیگنیچر/فرم‌ور)، radius
               (کاربران 2FA، کلاینت‌های RADIUS/NAS، تنظیمات سرویس پیامکی،
               الگوریتم authenticate اصلی)، self_update (تایید بسته آپدیت
               امضاشده)، diagnostics (خروجی بسته تشخیصی)
  api/v1/      تجمیع روتر، دیپندنسی‌های auth/RBAC
  workers/     Celery app + task های heartbeat لایسنس، بکاپ دیتابیس داخلی،
               بکاپ زمان‌بندی‌شده دستگاه‌ها، poll دوره‌ای SNMP، همگام‌سازی
               شبانه FTP، و پاک‌سازی روزانه OTP challenge های منقضی؛
               snmp_trap_receiver.py، ftp_relay_server.py و radius_server.py
               هم اینجان ولی هرکدوم یک process کاملاً جدا و مستقل از
               FastAPI/Celery هستن (چون هرکدوم یک socket خودشون رو باز
               نگه می‌دارن)
  alembic/     migration ها

frontend/src/
  app/         روتر
  components/  layout (RTL shell, sidebar)
  modules/     auth (لاگین)، dashboard، devices (لیست/افزودن/تست اتصال)،
               backups (لیست/بکاپ‌گیری/دانلود/ریستور به‌ازای هر دستگاه)،
               monitoring (تنظیمات SNMP + متریک‌ها + هشدارها به‌ازای هر
               دستگاه)، distribution (مدیریت بسته‌ها + منبع FTP + پوش
               به‌ازای هر دستگاه)، radius (کاربران 2FA، کلاینت‌های RADIUS،
               تنظیمات سرویس پیامکی)، reports (گزارش وضعیت فلیت)، license
               (بنر وضعیت + صفحه فعال‌سازی)
  services/    کلاینت API
  stores/      Zustand auth store

dev/mock-license-server/   License Server جعلی فقط برای توسعه محلی —
                           هرگز روی مشتری دیپلوی نشود؛ License Server واقعی
                           محصول/ریپوی جداییست که بعداً ساخته می‌شود.

docs/          مستندات معماری: قرارداد API لایسنسینگ، مدیریت کلید رمزنگاری،
               معماری بکاپ/ریستور، معماری مانیتورینگ SNMP، معماری توزیع
               آفلاین سیگنیچر/فرم‌ور
```

## چیزهایی که در فاز ۰ عمداً کامل نشده

- اعمال واقعی بسته آپدیت (`self_update`): فقط اعتبارسنجی امضا پیاده شده؛
  توقف سرویس/جایگزینی کد/اجرای migration به CLI نصب‌کننده در فاز بعد موکول شده.
- کلید عمومی امضای بسته آپدیت (`UPDATE_SIGNING_PUBLIC_KEY_HEX`) فقط placeholder
  است — قبل از اولین آپدیت واقعی باید با کلید واقعی جایگزین شود.
- RBAC فعلاً flat permission-string روی نقش است، نه ماتریس کامل — برای
  اسکوپ فاز ۰ کافیست، در فازهای بعد در صورت نیاز نرمالایز می‌شود.

## نکته مهم فاز ۱/۲: چیزهایی که هنوز نیاز به تایید روی دستگاه واقعی دارن

درایور FortiGate (`backend/app/domains/devices/drivers/fortigate.py`) با توکن
API استاندارد Fortinet ساخته شده و برای `test_connection`/`backup` با یک سرور
HTTPS جعلی end-to-end تست شده (نه فقط mock در سطح تست‌های واحد) و قابل اعتماده.
یک نکته‌ی باز روی همین درایور: **`restore()`** - مستندات Fortinet نشون می‌ده
بعضی وقت‌ها این endpoint نیاز به پروفایل API نزدیک `super_admin` داره (نه فقط
`sysgrp`)؛ اگه با ۴۰۱/۴۰۳ مواجه شدید، سطح دسترسی API user رو بالا ببرید. فرمت
دقیق multipart (اسم فیلد `file`) هم روی دستگاه واقعی باید تایید بشه.

درایور FortiWeb (`drivers/fortiweb.py`) چون مستندات رسمی و پایداری در حد
FortiGate برایش پیدا نشد، کاملاً حدسی ساخته شده و **باید روی FortiWeb واقعی که
در دسترس دارید تست بشه** - هم مکانیزم auth (`POST /logincheck` + کوکی/هدر
CSRF)، هم مسیرهای backup/restore (`/api/v2.0/system/config/backup` و
`.../restore`) که صرفاً با قیاس به نام‌گذاری FortiGate حدس زده شدن. اگه هرکدوم
از این عملیات‌ها روی FortiWeb واقعی fail داد، فقط همین یک فایل نیاز به اصلاح
داره؛ چون پشت اینترفیس `FortinetDriver` قرار داره، تغییرش به بقیه‌ی کد سرایت
نمی‌کنه.

## نکته مهم فاز ۳: مانیتورینگ SNMP

جزئیات کامل در `docs/monitoring.md`. خلاصه‌اش:
- فقط **SNMPv2c** پیاده‌سازی شده (نه v3) - تصمیم آگاهانه، نه کمبود.
- Poller و Trap Receiver هر دو با یک `snmpd` واقعی و ابزار `snmptrap` واقعی
  حین توسعه end-to-end تست شدن (نه فقط mock) - رفتار پروتکل و مسیر خطا
  واقعی و تست‌شده‌ست.
- OID های CPU/حافظه فورتی‌گیت مستندسازی نسبتاً معتبری دارن؛ **OID تعداد
  نشست (`fgSysSesCount`)** فقط از منابع کامیونیتی گرفته شده و باید روی
  دستگاه واقعی تایید بشه (اگه اشتباه باشه، فقط مقدار خالی برمی‌گرده، خطا
  نمی‌ده).
- طبقه‌بندی trap ها: فقط trap های استاندارد SNMPv2 (coldStart/linkDown/...)
  با اطمینان شناسایی می‌شن؛ trap های اختصاصی Fortinet به‌عنوان «رویداد
  Fortinet» با OID خام نمایش داده می‌شن چون نگاشت دقیق OID→معنی برای این
  کدبیس تایید نشده (به‌جای حدس زدن معنی، داده خام رو نشون می‌ده).
- **بدون نیاز به ری‌استارت**: تغییر تنظیمات SNMP یک دستگاه هر ۳۰ ثانیه
  (قابل‌تنظیم) به‌صورت خودکار توسط خودِ process `snmp-trap-receiver`
  دوباره از دیتابیس خونده می‌شه - با یک تست واقعی (ارسال trap واقعی قبل و
  بعد از افزودن دستگاه، بدون ری‌استارت) تایید شده. جزئیات در
  `docs/monitoring.md`.
- Trap Receiver (و همچنین poller) به `asyncore`/`asynchat` (ماژول‌های
  deprecated پایتون که در ۳.۱۲ حذف شدن) وابسته‌ست؛ با نصب پکیج‌های
  جایگزین `pyasyncore`/`pyasynchat` به‌علاوه یک fix دوم برای باگ
  `import imp` در خودِ pysnmp (`app/core/pysnmp_compat.py`)، این وابستگی
  رفع شد و Dockerfile الان روی Python 3.12 اجرا می‌شه - با تست واقعی
  (snmpd/snmptrap واقعی) روی ۳.۱۲ و ۳.۱۳ تایید شده. جزئیات در
  `docs/monitoring.md`.

## نکته مهم فاز ۴: توزیع آفلاین سیگنیچر/فرم‌ور

جزئیات کامل در `docs/signature-distribution.md`. خلاصه‌اش:
- فورتی‌گیت REST API برای تریگر آپدیت سیگنیچر از FTP سفارشی **نداره** (فقط
  CLI) — به همین دلیل این یک عملیات از طریق **SSH** انجام می‌شه (تنها جای
  کدبیس که از REST API استفاده نمی‌کنه). فرم‌ور فرق داره: FortiGate واقعاً
  یه REST endpoint داره (`monitor/system/firmware/upgrade` با
  `file_content` به صورت base64) که این شکل درخواست از روی Ansible
  Collection رسمی Fortinet تایید شده، نه حدس.
- فورتی‌وب اصلاً CLI پول از FTP نداره — سیگنیچر/فرم‌ور مستقیم از طریق همون
  session کوکی HTTP آپلود می‌شن (بدون relay). مسیر دقیق endpoint اونها
  هنوز حدسیه (مثل بکاپ/ریستور فاز ۲).
- بین SSH و netmiko، netmiko رد شد: درایور `fortinet` نتمیکو حین تست واقعی
  خطای `Unexpected FortiOS Version encountered` داد (چون تشخیص نسخه از
  خروجی `get system status` واقعی FortiOS انتظار داره). به‌جاش از
  `paramiko` خام با یک حلقه drain مبتنی بر idle-timeout استفاده شده که
  end-to-end روی یک SSH سرور واقعی تست شده.
- یک باگ واقعی حین تست پیدا و رفع شد: `ftplib`'s NLST همیشه مسیر کامل
  برنمی‌گردونه (بعضی سرورها مثل pyftpdlib فقط basename می‌دن)؛ کد اولیه
  این مسیرها رو مستقیم برای RETR بعدی استفاده می‌کرد که باعث می‌شد
  sync بی‌صدا صفر بسته وارد کنه. الان مسیرها صریحاً ساخته می‌شن.
- **بدون rollback خودکار واقعی** — فقط health-check بعد از پوش (همون تست
  اتصال فاز ۱) روی `PushRecord.post_push_check_ok` ثبت می‌شه. اگه false یا
  null بود یعنی باید دستی بررسی کنی، نه اینکه سیستم خودش برگردونده باشه.
- `pyftpdlib` هم مثل pysnmp به `asyncore`/`asynchat` وابسته‌ست، ولی خودش
  به‌صورت شرطی برای Python ≥ 3.12 پکیج‌های جایگزین رو نیاز داره - چون
  این‌ها به‌خاطر pysnmp هم نصب هستن، نیازی به کد اضافه نبود. حساب FTP relay
  هم یک حساب ثابت نصب‌کل از تنظیمات/env هست نه یک لیست دیتابیسی، پس
  اصلاً محدودیت hot-reload‌ای نداره (برخلاف چیزی که قبلاً اینجا نوشته
  بودم).

## نکته مهم فاز ۵: احراز هویت دو مرحله‌ای پیامکی (RADIUS)

جزئیات کامل در `docs/radius-2fa.md`. خلاصه‌اش:
- taktaplus خودش سرور RADIUS (PAP) هست، نه یک لایه OTP اضافه روی یک سرور
  RADIUS دیگه — رمز اصلی کاربر (`TwoFactorUser`) هم توسط خودِ taktaplus
  رمزنگاری و بررسی می‌شه.
- یک الگوریتم واحد و stateless-per-request، بدون فلگ «mode» جدا برای هر
  NAS، هم جریان challenge/response (ورود ادمین و SSL VPN، که فورتی‌گیت
  Access-Challenge رو به کاربر نهایی فوروارد می‌کنه) و هم جریان
  password+OTP به‌هم‌چسبیده (IPsec، که اصلاً challenge فوروارد نمی‌شه) رو
  پوشش می‌ده. تبعاً محدودیت IPsec: تلاش اول (فقط رمز) همیشه توسط NAS رد
  می‌شه ولی پیامک رو بی‌صدا می‌فرسته؛ تلاش دوم با رمز+کد چسبیده موفق می‌شه.
- دو باگ واقعی pyrad حین تست پروتکلی با کلاینت/سرور واقعی پیدا و رفع شد:
  (۱) خوندن `User-Password` باید از مسیر خام (`dict.__getitem__` قبل از
  `PwDecrypt`) انجام بشه، چون `pkt["User-Password"]` مقدار رو طبق نوع
  دیکشنری ("string") دیکد می‌کنه که همون obfuscation رمزی PAP رو خراب
  می‌کنه؛ (۲) `AuthPacket.CreateReply()` فیلدهای `.source`/`.fd` رو از
  روی request کپی نمی‌کنه، بدون ست کردن دستی `reply.source` هر پاسخی
  (accept/challenge/reject) با `AttributeError` کرش می‌کرد و اصلاً به NAS
  نمی‌رسید.
- سرویس پیامکی کاملاً توسط مشتری تنظیم می‌شه — برخلاف منبع FTP فاز ۴ هیچ
  حساب پیش‌فرض ارائه‌دهنده‌ای وجود نداره. کاوه‌نگار طبق مستندات رسمی‌اش
  تایید شده؛ برای هر سرویس دیگه (مثلاً ملی‌پیامک) یک مسیر HTTP سفارشی
  قابل‌تنظیم (`generic_http`) هست چون شکل دقیق API اون سرویس‌ها تایید
  نشده بود.
- **بدون نیاز به ری‌استارت**: مثل SNMP trap receiver، اضافه/حذف/ویرایش یک
  `RadiusClient` (NAS) هر ۳۰ ثانیه (قابل‌تنظیم) خودکار از دیتابیس دوباره
  خونده می‌شه - چون pyrad برخلاف pysnmp هیچ timer-hook داخلی نداره، این با
  یک ترد پس‌زمینه (فقط با جایگزینی کامل دیکشنری hosts، نه ویرایش درجا)
  پیاده شده. با یک تست واقعی (درخواست از NAS ناشناخته timeout می‌خوره، بعد
  از افزودن ردیف و صبر برای بازه رفرش، همون کلاینت موفق می‌شه) تایید شده.
- نرخ‌محدودسازی پیامک (پیش‌فرض ۵ در ساعت) و قفل‌شدن حساب بعد از تلاش‌های
  ناموفق مکرر (پیش‌فرض ۵ بار، ۱۵ دقیقه) پیاده‌سازی شده؛ عملیات باز کردن
  دستی قفل هم روی پنل موجوده.
