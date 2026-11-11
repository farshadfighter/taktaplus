-- +goose Up
-- Report templates: built-in ones are seeded here and cannot be edited.
CREATE TABLE report_templates (
    id          bigserial PRIMARY KEY,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    widgets     text[] NOT NULL,
    builtin     boolean NOT NULL DEFAULT false,
    updated_by  text NOT NULL DEFAULT '',
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX report_templates_name_key ON report_templates (lower(name));

INSERT INTO report_templates (name, description, widgets, builtin) VALUES
 ('خلاصه‌ی امنیتی', 'تهدیدها، میزبان‌های پرخطر، اپلیکیشن‌ها و روند ترافیک و تهدید',
  '{kpi_overview,ts_bandwidth_threats,top_threats,risky_hosts,top_apps,top_countries}', true),
 ('مصرف پهنای باند', 'مبدأها، مقصدها، اپلیکیشن‌ها و کشورها به همراه روند ارسال و دریافت',
  '{ts_bandwidth,top_sources,top_destinations,top_apps,top_countries}', true),
 ('فعالیت VPN', 'کاربران، تعداد و حجم اتصال‌ها و ورودهای ناموفق',
  '{kpi_vpn,vpn_users,vpn_failures}', true),
 ('WebFilter', 'دسته‌های وب، سایت‌های مسدودشده و کاربران پرتکرار',
  '{kpi_web,web_categories,web_blocked_hosts,web_users}', true),
 ('حملات WAF', 'انواع حمله، مبدأها، کشورها و سایت‌های هدف (FortiWeb)',
  '{kpi_waf,ts_waf,waf_attacks,waf_sources,waf_countries,waf_hosts}', true),
 ('رویدادهای سیستمی', 'رویدادهای دستگاه‌ها به تفکیک نوع و سطح',
  '{kpi_events,events_summary}', true);

CREATE TABLE report_schedules (
    id          bigserial PRIMARY KEY,
    template_id bigint NOT NULL REFERENCES report_templates (id) ON DELETE CASCADE,
    frequency   text NOT NULL CHECK (frequency IN ('daily', 'weekly', 'monthly')),
    weekday     integer NOT NULL DEFAULT 6,   -- 0 = Sunday … 6 = Saturday (weekly)
    monthday    integer NOT NULL DEFAULT 1,   -- 1–28 (monthly)
    at_minute   integer NOT NULL DEFAULT 420, -- minutes after local midnight
    device_ids  bigint[] NOT NULL DEFAULT '{}',
    group_id    bigint REFERENCES device_groups (id) ON DELETE SET NULL,
    formats     text[] NOT NULL DEFAULT '{pdf}',
    channel_id  bigint REFERENCES notification_channels (id) ON DELETE SET NULL,
    recipients  text[] NOT NULL DEFAULT '{}',
    enabled     boolean NOT NULL DEFAULT true,
    next_run_at timestamptz NOT NULL,
    created_by  text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX report_schedules_due_idx ON report_schedules (next_run_at) WHERE enabled;

CREATE TABLE report_runs (
    id          bigserial PRIMARY KEY,
    template_id bigint REFERENCES report_templates (id) ON DELETE SET NULL,
    schedule_id bigint REFERENCES report_schedules (id) ON DELETE SET NULL,
    title       text NOT NULL,
    period_from timestamptz NOT NULL,
    period_to   timestamptz NOT NULL,
    device_ids  bigint[] NOT NULL DEFAULT '{}',
    formats     text[] NOT NULL,
    status      text NOT NULL CHECK (status IN ('running', 'ready', 'sent', 'failed')),
    error       text NOT NULL DEFAULT '',
    files       jsonb NOT NULL DEFAULT '[]',
    created_by  text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz
);
CREATE INDEX report_runs_created_idx ON report_runs (created_at DESC);

-- +goose Down
DROP TABLE report_runs;
DROP TABLE report_schedules;
DROP TABLE report_templates;
