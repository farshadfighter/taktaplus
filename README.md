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
- **فاز ۲ (این نسخه)** — بکاپ/ریستور: بکاپ دستی و زمان‌بندی‌شده (شبانه) با
  نگهداری (retention) قابل‌تنظیم، محتوای رمزنگاری‌شده در دیتابیس، دانلود
  بکاپ خام، و اعتبارسنجی پیش از ریستور (تطبیق سریال/فرم‌ور با هشدار قابل‌عبور
  با `force`).
- فاز ۳ — مانیتورینگ SNMP + هشداردهی + گزارش Compliance
- فاز ۴ — توزیع آفلاین سیگنیچر و فرم‌ور
- فاز ۵ — احراز هویت دو مرحله‌ای پیامکی (RADIUS)

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
               رمزنگاری‌شده + اعتبارسنجی پیش از ریستور)، self_update (تایید بسته
               آپدیت امضاشده)، diagnostics (خروجی بسته تشخیصی)
  api/v1/      تجمیع روتر، دیپندنسی‌های auth/RBAC
  workers/     Celery app + task های heartbeat لایسنس، بکاپ دیتابیس داخلی، و
               بکاپ زمان‌بندی‌شده دستگاه‌ها
  alembic/     migration ها

frontend/src/
  app/         روتر
  components/  layout (RTL shell, sidebar)
  modules/     auth (لاگین)، dashboard، devices (لیست/افزودن/تست اتصال)،
               backups (لیست/بکاپ‌گیری/دانلود/ریستور به‌ازای هر دستگاه)،
               license (بنر وضعیت + صفحه فعال‌سازی)
  services/    کلاینت API
  stores/      Zustand auth store

dev/mock-license-server/   License Server جعلی فقط برای توسعه محلی —
                           هرگز روی مشتری دیپلوی نشود؛ License Server واقعی
                           محصول/ریپوی جداییست که بعداً ساخته می‌شود.

docs/          مستندات معماری: قرارداد API لایسنسینگ، مدیریت کلید رمزنگاری،
               معماری بکاپ/ریستور
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
