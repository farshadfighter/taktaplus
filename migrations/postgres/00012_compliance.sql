-- +goose Up
-- F3: configuration templates, compliance rules and evaluation results.
CREATE TABLE compliance_templates (
    id          bigserial PRIMARY KEY,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    platform    text NOT NULL CHECK (platform IN ('fortigate', 'fortiweb')),
    body        text NOT NULL,                 -- CLI with {{ .Vars.x }} variables
    group_ids   bigint[] NOT NULL DEFAULT '{}', -- empty: every device of the platform
    auto_fix    boolean NOT NULL DEFAULT false,  -- open a change request when drift is found
    version     integer NOT NULL DEFAULT 1,
    updated_by  text NOT NULL DEFAULT '',
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX compliance_templates_name_key ON compliance_templates (lower(name));

CREATE TABLE compliance_rules (
    id          bigserial PRIMARY KEY,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    severity    text NOT NULL CHECK (severity IN ('critical', 'high', 'medium', 'low')),
    platform    text NOT NULL CHECK (platform IN ('fortigate', 'fortiweb')),
    group_id    bigint REFERENCES device_groups (id) ON DELETE CASCADE,
    section     text NOT NULL,                  -- "firewall policy", "system global"
    scope       text NOT NULL CHECK (scope IN ('section', 'each')),
    match       jsonb NOT NULL DEFAULT '[]',     -- conditions selecting entries (scope each)
    require     jsonb NOT NULL DEFAULT '[]',     -- conditions that must hold; empty: matching entries are forbidden
    builtin     boolean NOT NULL DEFAULT false,
    enabled     boolean NOT NULL DEFAULT true,
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE compliance_results (
    device_id    bigint PRIMARY KEY REFERENCES devices (id) ON DELETE CASCADE,
    backup_id    bigint REFERENCES backups (id) ON DELETE SET NULL,
    evaluated_at timestamptz NOT NULL,
    score        integer NOT NULL,
    drift        jsonb NOT NULL DEFAULT '[]',
    violations   jsonb NOT NULL DEFAULT '[]',
    error        text NOT NULL DEFAULT ''
);

CREATE TABLE compliance_exceptions (
    id         bigserial PRIMARY KEY,
    device_id  bigint NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    ref        text NOT NULL,     -- "rule:<id>:<entry>" or "drift:<template id>:<path>|<key>"
    reason     text NOT NULL,
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (device_id, ref)
);

ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_kind_check;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_kind_check
    CHECK (kind IN ('offline', 'cpu', 'mem', 'disk', 'contract_expiry', 'license_expiry', 'log_silence', 'log_attacks', 'vpn_failures',
                    'compliance_score', 'config_drift'));
INSERT INTO alert_rules (name, kind, threshold, severity) VALUES
    ('Compliance score below target', 'compliance_score', 70, 'warning'),
    ('Configuration drift from template', 'config_drift', 0, 'warning');

-- Built-in rules; administrators can disable them or change their severity.
INSERT INTO compliance_rules (name, severity, platform, section, scope, match, require, builtin) VALUES
 ('هیچ Policy با مبدأ all، مقصد all و سرویس ALL با اقدام accept وجود نداشته باشد', 'critical', 'fortigate', 'firewall policy', 'each',
  '[{"key":"action","op":"eq","value":"accept","default":"deny"},{"key":"srcaddr","op":"contains","value":"all"},{"key":"dstaddr","op":"contains","value":"all"},{"key":"service","op":"contains","value":"ALL"}]', '[]', true),
 ('دسترسی HTTP و Telnet مدیریتی روی اینترفیس‌های WAN خاموش باشد', 'critical', 'fortigate', 'system interface', 'each',
  '[{"key":"role","op":"eq","value":"wan","default":"undefined"}]',
  '[{"key":"allowaccess","op":"not_contains","value":"http"},{"key":"allowaccess","op":"not_contains","value":"telnet"}]', true),
 ('حداقل طول رمز ادمین ۱۲ کاراکتر', 'high', 'fortigate', 'system password-policy', 'section', '[]',
  '[{"key":"status","op":"eq","value":"enable","default":"disable"},{"key":"minimum-length","op":"gte","value":"12","default":"8"}]', true),
 ('همه‌ی ادمین‌ها Trusted Host داشته باشند', 'high', 'fortigate', 'system admin', 'each', '[]',
  '[{"key":"trusthost1","op":"ne","value":"0.0.0.0 0.0.0.0","default":"0.0.0.0 0.0.0.0"}]', true),
 ('ارسال Syslog فعال باشد', 'medium', 'fortigate', 'log syslogd setting', 'section', '[]',
  '[{"key":"status","op":"eq","value":"enable","default":"disable"}]', true),
 ('strong-crypto فعال باشد', 'medium', 'fortigate', 'system global', 'section', '[]',
  '[{"key":"strong-crypto","op":"eq","value":"enable","default":"enable"}]', true),
 ('Policyهای accept ترافیک را لاگ کنند', 'low', 'fortigate', 'firewall policy', 'each',
  '[{"key":"action","op":"eq","value":"accept","default":"deny"}]',
  '[{"key":"logtraffic","op":"ne","value":"disable","default":"utm"}]', true),
 ('خروج خودکار ادمین حداکثر پس از ۱۵ دقیقه', 'low', 'fortigate', 'system global', 'section', '[]',
  '[{"key":"admintimeout","op":"lte","value":"15","default":"5"}]', true),
 ('ارسال Syslog فعال باشد (FortiWeb)', 'medium', 'fortiweb', 'log syslogd', 'section', '[]',
  '[{"key":"status","op":"eq","value":"enable","default":"disable"}]', true),
 ('سیاست رمز ادمین با حداقل ۱۲ کاراکتر (FortiWeb)', 'high', 'fortiweb', 'system password-policy', 'section', '[]',
  '[{"key":"status","op":"eq","value":"enable","default":"disable"},{"key":"minimum-length","op":"gte","value":"12","default":"8"}]', true),
 ('HTTP مدیریتی روی اینترفیس‌ها خاموش باشد (FortiWeb)', 'critical', 'fortiweb', 'system interface', 'each', '[]',
  '[{"key":"allowaccess","op":"not_contains","value":"http"}]', true);

INSERT INTO report_templates (name, description, widgets, builtin) VALUES
 ('انطباق پیکربندی', 'امتیاز انطباق، انحراف از قالب‌ها و قوانین نقض‌شده‌ی هر دستگاه', '{sys_compliance_kpi,sys_compliance}', true);
UPDATE report_templates SET widgets = array_append(widgets, 'sys_compliance_kpi')
 WHERE name = 'خلاصه‌ی مدیریتی' AND builtin;

-- +goose Down
UPDATE report_templates SET widgets = array_remove(widgets, 'sys_compliance_kpi') WHERE name = 'خلاصه‌ی مدیریتی' AND builtin;
DELETE FROM report_templates WHERE name = 'انطباق پیکربندی' AND builtin;
DELETE FROM alert_rules WHERE kind IN ('compliance_score', 'config_drift');
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_kind_check;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_kind_check
    CHECK (kind IN ('offline', 'cpu', 'mem', 'disk', 'contract_expiry', 'license_expiry', 'log_silence', 'log_attacks', 'vpn_failures'));
DROP TABLE compliance_exceptions;
DROP TABLE compliance_results;
DROP TABLE compliance_rules;
DROP TABLE compliance_templates;
