-- +goose Up
INSERT INTO report_templates (name, description, widgets, builtin) VALUES
 ('خلاصه‌ی مدیریتی', 'وضعیت دستگاه‌ها، هشدارها، بکاپ‌ها و اشتراک‌ها همراه با امنیت، حملات WAF و VPN، با مقایسه با دوره‌ی قبل',
  '{sys_devices,sys_alerts,kpi_overview,ts_bandwidth_threats,top_threats,kpi_waf,kpi_vpn,sys_backups,sys_expiring}', true);

-- +goose Down
DELETE FROM report_templates WHERE name = 'خلاصه‌ی مدیریتی' AND builtin;
