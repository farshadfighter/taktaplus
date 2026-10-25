# TaktaPlus

مدیریت متمرکز FortiGate و FortiWeb به صورت On-Premise: بروزرسانی سیگنیچر بدون دسترسی به FortiGuard، بکاپ و ریستور، مانیتورینگ، احراز هویت دو مرحله‌ای پیامکی، و گزارش‌گیری.

- معماری: [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)
- سرور لایسنس: [taktaplus-Lic](https://github.com/farshadfighter/taktaplus-Lic)

## وضعیت

فاز ۰: اسکلت پروژه. سرویس `api` بالا می‌آید (سلامت، نسخه، رابط وب)، Migrationها اجرا می‌شوند و شناسه‌ی نصب ساخته می‌شود. بقیه‌ی سرویس‌ها در فازهای بعد اضافه می‌شوند.

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
make run        # http://localhost:8080
make test lint
```

برای رابط وب با Hot Reload: `cd web && npm run dev`.

اجرای کامل با Docker:

```sh
docker compose -f deploy/docker-compose.yml up -d --build
```
