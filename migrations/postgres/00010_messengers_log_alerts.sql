-- +goose Up
-- Bale and Eitaa bots as notification channels.
ALTER TABLE notification_channels DROP CONSTRAINT notification_channels_kind_check;
ALTER TABLE notification_channels ADD CONSTRAINT notification_channels_kind_check
    CHECK (kind IN ('email', 'webhook', 'syslog', 'bale', 'eitaa'));

-- Rules over the log store. window_minutes is the period counted by
-- log_attacks and vpn_failures; log_silence's threshold is in minutes.
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_kind_check;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_kind_check
    CHECK (kind IN ('offline', 'cpu', 'mem', 'disk', 'contract_expiry', 'license_expiry', 'log_silence', 'log_attacks', 'vpn_failures'));
ALTER TABLE alert_rules ADD COLUMN window_minutes integer NOT NULL DEFAULT 0;

INSERT INTO alert_rules (name, kind, threshold, window_minutes, severity) VALUES
    ('No logs received',             'log_silence',  60,  0,  'warning'),
    ('Attack burst from one source', 'log_attacks',  100, 10, 'critical'),
    ('Repeated VPN login failures',  'vpn_failures', 10,  15, 'warning');

-- +goose Down
DELETE FROM alert_rules WHERE kind IN ('log_silence', 'log_attacks', 'vpn_failures');
ALTER TABLE alert_rules DROP COLUMN window_minutes;
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_kind_check;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_kind_check
    CHECK (kind IN ('offline', 'cpu', 'mem', 'disk', 'contract_expiry', 'license_expiry'));
ALTER TABLE notification_channels DROP CONSTRAINT notification_channels_kind_check;
ALTER TABLE notification_channels ADD CONSTRAINT notification_channels_kind_check
    CHECK (kind IN ('email', 'webhook', 'syslog'));
