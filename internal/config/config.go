// Package config loads TaktaPlus settings from environment variables.
package config

import (
	"errors"
	"os"
	"strconv"
	"time"

	"github.com/farshadfighter/taktaplus/internal/secretbox"
)

// Config holds runtime settings.
type Config struct {
	Listen          string // TAKTAPLUS_LISTEN, default ":8443" (":8080" with TLS off)
	TLS             string // TAKTAPLUS_TLS: "on" (default) or "off" for local development behind a proxy
	TLSCert         string // TAKTAPLUS_TLS_CERT and TAKTAPLUS_TLS_KEY: PEM files; empty = self-signed in DATA_DIR/tls
	TLSKey          string
	DatabaseURL     string         // TAKTAPLUS_DATABASE_URL, required
	DataDir         string         // TAKTAPLUS_DATA_DIR, default /var/lib/taktaplus
	HostRoot        string         // TAKTAPLUS_HOST_ROOT: where host identifiers are mounted, default "/"
	MasterKey       *secretbox.Box // TAKTAPLUS_MASTER_KEY: base64 of 32 random bytes, required
	CookieSecure    bool           // TAKTAPLUS_COOKIE_SECURE, default true; false only for local HTTP
	LicenseServer   string         // TAKTAPLUS_LICENSE_SERVER: license server base URL for online activation
	ShutdownTimeout time.Duration  // TAKTAPLUS_SHUTDOWN_TIMEOUT, default 15s
	LogLevel        string         // TAKTAPLUS_LOG_LEVEL: debug, info, warn, error
	TFTPListen      string         // TAKTAPLUS_TFTP_LISTEN, default ":6969" (publish as UDP 69); "off" disables
	FTPListen       string         // TAKTAPLUS_FTP_LISTEN, default ":2121" (publish as TCP 21); "off" disables
	FilePublicIP    string         // TAKTAPLUS_FILESERVER_IP: address devices use to reach TaktaPlus
	FTPPassive      string         // TAKTAPLUS_FTP_PASSIVE_PORTS, default "30000-30009"
	FTPPublicPort   int            // TAKTAPLUS_FTP_PUBLIC_PORT: FTP port as devices see it; default the listen port
	FortiWebUpload  string         // TAKTAPLUS_FORTIWEB_UPLOAD_PATH: FortiWeb API path for signature uploads
	RadiusListen    string         // TAKTAPLUS_RADIUS_LISTEN, default ":1812" (UDP); "off" disables
	SMTPListen      string         // TAKTAPLUS_SMTP_LISTEN, default ":2525" (publish as TCP 25); "off" disables
	ClickHouseURL   string         // TAKTAPLUS_CLICKHOUSE_URL, e.g. http://user:pass@clickhouse:8123/taktaplus; empty disables logs
	SyslogUDP       string         // TAKTAPLUS_SYSLOG_UDP, default ":5514" (publish as UDP 514); "off" disables
	SyslogTCP       string         // TAKTAPLUS_SYSLOG_TCP, default ":5514" (publish as TCP 514); "off" disables
	SyslogTLS       string         // TAKTAPLUS_SYSLOG_TLS, default ":6514"; "off" disables
	Chromium        string         // TAKTAPLUS_CHROMIUM: browser that prints PDF reports; empty = look for chromium on PATH
	TimeZone        *time.Location // TAKTAPLUS_TIMEZONE, default Asia/Tehran: report schedules and dates
}

// FromEnv reads the configuration from the process environment.
func FromEnv() (Config, error) {
	c := Config{
		TLS:             getenv("TAKTAPLUS_TLS", "on"),
		TLSCert:         os.Getenv("TAKTAPLUS_TLS_CERT"),
		TLSKey:          os.Getenv("TAKTAPLUS_TLS_KEY"),
		DatabaseURL:     os.Getenv("TAKTAPLUS_DATABASE_URL"),
		DataDir:         getenv("TAKTAPLUS_DATA_DIR", "/var/lib/taktaplus"),
		HostRoot:        getenv("TAKTAPLUS_HOST_ROOT", "/"),
		CookieSecure:    os.Getenv("TAKTAPLUS_COOKIE_SECURE") != "false",
		LicenseServer:   os.Getenv("TAKTAPLUS_LICENSE_SERVER"),
		ShutdownTimeout: 15 * time.Second,
		LogLevel:        getenv("TAKTAPLUS_LOG_LEVEL", "info"),
		TFTPListen:      getenv("TAKTAPLUS_TFTP_LISTEN", ":6969"),
		FTPListen:       getenv("TAKTAPLUS_FTP_LISTEN", ":2121"),
		FilePublicIP:    os.Getenv("TAKTAPLUS_FILESERVER_IP"),
		FTPPassive:      getenv("TAKTAPLUS_FTP_PASSIVE_PORTS", "30000-30009"),
		FortiWebUpload:  os.Getenv("TAKTAPLUS_FORTIWEB_UPLOAD_PATH"),
		RadiusListen:    getenv("TAKTAPLUS_RADIUS_LISTEN", ":1812"),
		SMTPListen:      getenv("TAKTAPLUS_SMTP_LISTEN", ":2525"),
		ClickHouseURL:   os.Getenv("TAKTAPLUS_CLICKHOUSE_URL"),
		SyslogUDP:       getenv("TAKTAPLUS_SYSLOG_UDP", ":5514"),
		SyslogTCP:       getenv("TAKTAPLUS_SYSLOG_TCP", ":5514"),
		SyslogTLS:       getenv("TAKTAPLUS_SYSLOG_TLS", ":6514"),
		Chromium:        os.Getenv("TAKTAPLUS_CHROMIUM"),
	}
	if v := os.Getenv("TAKTAPLUS_FTP_PUBLIC_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 || p > 65535 {
			return c, errors.New("config: TAKTAPLUS_FTP_PUBLIC_PORT must be a port number")
		}
		c.FTPPublicPort = p
	}
	if c.RadiusListen == "off" {
		c.RadiusListen = ""
	}
	if c.SMTPListen == "off" {
		c.SMTPListen = ""
	}
	if c.TFTPListen == "off" {
		c.TFTPListen = ""
	}
	if c.FTPListen == "off" {
		c.FTPListen = ""
	}
	for _, v := range []*string{&c.SyslogUDP, &c.SyslogTCP, &c.SyslogTLS} {
		if *v == "off" {
			*v = ""
		}
	}
	if c.TLS != "on" && c.TLS != "off" {
		return c, errors.New("config: TAKTAPLUS_TLS must be on or off")
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return c, errors.New("config: set both TAKTAPLUS_TLS_CERT and TAKTAPLUS_TLS_KEY, or neither")
	}
	c.Listen = getenv("TAKTAPLUS_LISTEN", map[string]string{"on": ":8443", "off": ":8080"}[c.TLS])
	if v := os.Getenv("TAKTAPLUS_SHUTDOWN_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return c, errors.New("config: TAKTAPLUS_SHUTDOWN_TIMEOUT must be a duration such as 15s")
		}
		c.ShutdownTimeout = d
	}
	tz, err := time.LoadLocation(getenv("TAKTAPLUS_TIMEZONE", "Asia/Tehran"))
	if err != nil {
		return c, errors.New("config: TAKTAPLUS_TIMEZONE must be a time zone name such as Asia/Tehran")
	}
	c.TimeZone = tz
	if c.DatabaseURL == "" {
		return c, errors.New("config: TAKTAPLUS_DATABASE_URL is required")
	}
	key := os.Getenv("TAKTAPLUS_MASTER_KEY")
	if key == "" {
		return c, errors.New("config: TAKTAPLUS_MASTER_KEY is required (generate with: head -c32 /dev/urandom | base64)")
	}
	box, err := secretbox.FromBase64(key)
	if err != nil {
		return c, errors.New("config: TAKTAPLUS_MASTER_KEY must be base64 of 32 bytes")
	}
	c.MasterKey = box
	return c, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
