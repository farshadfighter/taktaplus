-- Raw logs: one table per log family, daily partitions, kept raw_days (90 by
-- default; changed from the panel with ALTER ... MODIFY TTL). Every original
-- key=value pair stays in `fields` for the log viewer.

CREATE TABLE IF NOT EXISTS log_traffic (
    ts          DateTime64(3, 'UTC'),
    device_id   UInt32,
    vd          LowCardinality(String),
    subtype     LowCardinality(String),
    level       LowCardinality(String),
    action      LowCardinality(String),
    policyid    UInt32,
    srcip       String,
    srcport     UInt16,
    srcintf     LowCardinality(String),
    srccountry  LowCardinality(String),
    dstip       String,
    dstport     UInt16,
    dstintf     LowCardinality(String),
    dstcountry  LowCardinality(String),
    proto       UInt8,
    service     LowCardinality(String),
    app         LowCardinality(String),
    appcat      LowCardinality(String),
    username    String,
    sentbyte    UInt64,
    rcvdbyte    UInt64,
    duration    UInt32,
    fields      Map(LowCardinality(String), String) CODEC(ZSTD(3))
) ENGINE = MergeTree
PARTITION BY toYYYYMMDD(ts)
ORDER BY (device_id, ts)
TTL toDateTime(ts) + INTERVAL 90 DAY DELETE;

CREATE TABLE IF NOT EXISTS log_threat (
    ts          DateTime64(3, 'UTC'),
    device_id   UInt32,
    vd          LowCardinality(String),
    subtype     LowCardinality(String),
    eventtype   LowCardinality(String),
    level       LowCardinality(String),
    severity    LowCardinality(String),
    action      LowCardinality(String),
    policyid    UInt32,
    srcip       String,
    srcport     UInt16,
    srccountry  LowCardinality(String),
    dstip       String,
    dstport     UInt16,
    dstcountry  LowCardinality(String),
    username    String,
    threat      String,
    category    LowCardinality(String),
    hostname    String,
    url         String,
    fields      Map(LowCardinality(String), String) CODEC(ZSTD(3))
) ENGINE = MergeTree
PARTITION BY toYYYYMMDD(ts)
ORDER BY (device_id, ts)
TTL toDateTime(ts) + INTERVAL 90 DAY DELETE;

CREATE TABLE IF NOT EXISTS log_event (
    ts          DateTime64(3, 'UTC'),
    device_id   UInt32,
    vd          LowCardinality(String),
    subtype     LowCardinality(String),
    level       LowCardinality(String),
    action      LowCardinality(String),
    logdesc     LowCardinality(String),
    username    String,
    srcip       String,
    tunneltype  LowCardinality(String),
    sentbyte    UInt64,
    rcvdbyte    UInt64,
    duration    UInt32,
    msg         String,
    fields      Map(LowCardinality(String), String) CODEC(ZSTD(3))
) ENGINE = MergeTree
PARTITION BY toYYYYMMDD(ts)
ORDER BY (device_id, ts)
TTL toDateTime(ts) + INTERVAL 90 DAY DELETE;

CREATE TABLE IF NOT EXISTS log_waf (
    ts          DateTime64(3, 'UTC'),
    device_id   UInt32,
    subtype     LowCardinality(String),
    severity    LowCardinality(String),
    action      LowCardinality(String),
    attack      LowCardinality(String),
    signature   String,
    policy      LowCardinality(String),
    srcip       String,
    srcport     UInt16,
    srccountry  LowCardinality(String),
    dstip       String,
    dstport     UInt16,
    http_host   String,
    http_method LowCardinality(String),
    http_url    String,
    msg         String,
    fields      Map(LowCardinality(String), String) CODEC(ZSTD(3))
) ENGINE = MergeTree
PARTITION BY toYYYYMMDD(ts)
ORDER BY (device_id, ts)
TTL toDateTime(ts) + INTERVAL 90 DAY DELETE;

-- Hourly rollups for dashboards and reports, kept rollup_days (365 by
-- default). Materialized views fill them as raw rows arrive.

CREATE TABLE IF NOT EXISTS r_traffic_src (
    hour      DateTime('UTC'),
    device_id UInt32,
    srcip     String,
    username  String,
    sessions  UInt64,
    sent      UInt64,
    rcvd      UInt64
) ENGINE = SummingMergeTree
PARTITION BY toYYYYMM(hour)
ORDER BY (device_id, hour, srcip, username)
TTL hour + INTERVAL 365 DAY DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS mv_traffic_src TO r_traffic_src AS
SELECT toStartOfHour(ts) AS hour, device_id, srcip, username,
       count() AS sessions, sum(sentbyte) AS sent, sum(rcvdbyte) AS rcvd
FROM log_traffic GROUP BY hour, device_id, srcip, username;

CREATE TABLE IF NOT EXISTS r_traffic_dst (
    hour       DateTime('UTC'),
    device_id  UInt32,
    dstip      String,
    dstcountry LowCardinality(String),
    sessions   UInt64,
    sent       UInt64,
    rcvd       UInt64
) ENGINE = SummingMergeTree
PARTITION BY toYYYYMM(hour)
ORDER BY (device_id, hour, dstip, dstcountry)
TTL hour + INTERVAL 365 DAY DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS mv_traffic_dst TO r_traffic_dst AS
SELECT toStartOfHour(ts) AS hour, device_id, dstip, dstcountry,
       count() AS sessions, sum(sentbyte) AS sent, sum(rcvdbyte) AS rcvd
FROM log_traffic GROUP BY hour, device_id, dstip, dstcountry;

CREATE TABLE IF NOT EXISTS r_traffic_app (
    hour      DateTime('UTC'),
    device_id UInt32,
    app       LowCardinality(String),
    appcat    LowCardinality(String),
    action    LowCardinality(String),
    sessions  UInt64,
    sent      UInt64,
    rcvd      UInt64
) ENGINE = SummingMergeTree
PARTITION BY toYYYYMM(hour)
ORDER BY (device_id, hour, app, appcat, action)
TTL hour + INTERVAL 365 DAY DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS mv_traffic_app TO r_traffic_app AS
SELECT toStartOfHour(ts) AS hour, device_id, app, appcat, action,
       count() AS sessions, sum(sentbyte) AS sent, sum(rcvdbyte) AS rcvd
FROM log_traffic GROUP BY hour, device_id, app, appcat, action;

CREATE TABLE IF NOT EXISTS r_threat (
    hour      DateTime('UTC'),
    device_id UInt32,
    subtype   LowCardinality(String),
    threat    String,
    severity  LowCardinality(String),
    action    LowCardinality(String),
    srcip     String,
    hits      UInt64
) ENGINE = SummingMergeTree
PARTITION BY toYYYYMM(hour)
ORDER BY (device_id, hour, subtype, threat, severity, action, srcip)
TTL hour + INTERVAL 365 DAY DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS mv_threat TO r_threat AS
SELECT toStartOfHour(ts) AS hour, device_id, subtype, threat, severity, action, srcip, count() AS hits
FROM log_threat WHERE subtype != 'webfilter'
GROUP BY hour, device_id, subtype, threat, severity, action, srcip;

CREATE TABLE IF NOT EXISTS r_web (
    hour      DateTime('UTC'),
    device_id UInt32,
    category  LowCardinality(String),
    hostname  String,
    action    LowCardinality(String),
    username  String,
    hits      UInt64
) ENGINE = SummingMergeTree
PARTITION BY toYYYYMM(hour)
ORDER BY (device_id, hour, category, hostname, action, username)
TTL hour + INTERVAL 365 DAY DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS mv_web TO r_web AS
SELECT toStartOfHour(ts) AS hour, device_id, category, hostname, action, username, count() AS hits
FROM log_threat WHERE subtype = 'webfilter'
GROUP BY hour, device_id, category, hostname, action, username;

CREATE TABLE IF NOT EXISTS r_vpn (
    hour       DateTime('UTC'),
    device_id  UInt32,
    username   String,
    tunneltype LowCardinality(String),
    action     LowCardinality(String),
    srcip      String,
    events     UInt64,
    sent       UInt64,
    rcvd       UInt64,
    duration   UInt64
) ENGINE = SummingMergeTree
PARTITION BY toYYYYMM(hour)
ORDER BY (device_id, hour, username, tunneltype, action, srcip)
TTL hour + INTERVAL 365 DAY DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS mv_vpn TO r_vpn AS
SELECT toStartOfHour(ts) AS hour, device_id, username, tunneltype, action, srcip,
       count() AS events, sum(sentbyte) AS sent, sum(rcvdbyte) AS rcvd, sum(duration) AS duration
FROM log_event WHERE subtype = 'vpn'
GROUP BY hour, device_id, username, tunneltype, action, srcip;

CREATE TABLE IF NOT EXISTS r_event (
    hour      DateTime('UTC'),
    device_id UInt32,
    subtype   LowCardinality(String),
    level     LowCardinality(String),
    logdesc   LowCardinality(String),
    hits      UInt64
) ENGINE = SummingMergeTree
PARTITION BY toYYYYMM(hour)
ORDER BY (device_id, hour, subtype, level, logdesc)
TTL hour + INTERVAL 365 DAY DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS mv_event TO r_event AS
SELECT toStartOfHour(ts) AS hour, device_id, subtype, level, logdesc, count() AS hits
FROM log_event GROUP BY hour, device_id, subtype, level, logdesc;

CREATE TABLE IF NOT EXISTS r_waf (
    hour       DateTime('UTC'),
    device_id  UInt32,
    attack     LowCardinality(String),
    severity   LowCardinality(String),
    action     LowCardinality(String),
    srcip      String,
    srccountry LowCardinality(String),
    http_host  String,
    hits       UInt64
) ENGINE = SummingMergeTree
PARTITION BY toYYYYMM(hour)
ORDER BY (device_id, hour, attack, severity, action, srcip, srccountry, http_host)
TTL hour + INTERVAL 365 DAY DELETE;

CREATE MATERIALIZED VIEW IF NOT EXISTS mv_waf TO r_waf AS
SELECT toStartOfHour(ts) AS hour, device_id, attack, severity, action, srcip, srccountry, http_host, count() AS hits
FROM log_waf GROUP BY hour, device_id, attack, severity, action, srcip, srccountry, http_host;
