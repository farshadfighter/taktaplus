package otp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/farshadfighter/taktaplus/internal/sms"
)

// LDAPConfig is the directory setting ("ldap").
type LDAPConfig struct {
	URL          string `json:"url"`       // ldap://dc.example.local:389 or ldaps://…:636
	StartTLS     bool   `json:"start_tls"` // for ldap:// URLs
	BindDN       string `json:"bind_dn"`   // service account
	BindPassword string `json:"bind_password"`
	BaseDN       string `json:"base_dn"`
	UserFilter   string `json:"user_filter"`   // %s is the user name
	ImportFilter string `json:"import_filter"` // users to import
	UserAttr     string `json:"user_attr"`
	MobileAttr   string `json:"mobile_attr"`
	NameAttr     string `json:"name_attr"`
}

// DefaultLDAP fits Active Directory.
var DefaultLDAP = LDAPConfig{
	UserFilter: "(&(objectClass=user)(sAMAccountName=%s))", ImportFilter: "(&(objectClass=user)(mobile=*)(!(userAccountControl:1.2.840.113556.1.4.803:=2)))",
	UserAttr: "sAMAccountName", MobileAttr: "mobile", NameAttr: "displayName",
}

// Redacted hides the bind password.
func (c LDAPConfig) Redacted() LDAPConfig {
	if c.BindPassword != "" {
		c.BindPassword = "********"
	}
	return c
}

type ldapConn interface {
	Bind(user, pass string) error
	Search(req *ldap.SearchRequest) (*ldap.SearchResult, error)
	Close() error
}

type ldapDialer func(c LDAPConfig) (ldapConn, error)

func dialLDAP(c LDAPConfig) (ldapConn, error) {
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "ldap" && u.Scheme != "ldaps") {
		return nil, errors.New("directory URL must start with ldap:// or ldaps://")
	}
	conn, err := ldap.DialURL(c.URL, ldap.DialWithTLSConfig(&tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12}))
	if err != nil {
		return nil, err
	}
	conn.SetTimeout(15 * time.Second)
	if c.StartTLS && u.Scheme == "ldap" {
		if err := conn.StartTLS(&tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12}); err != nil {
			conn.Close()
			return nil, err
		}
	}
	return conn, nil
}

// LDAP returns the stored directory settings.
func (s *Service) LDAP(ctx context.Context) (LDAPConfig, error) {
	c := DefaultLDAP
	err := s.settings.Get(ctx, "ldap", &c)
	return c, err
}

// SaveLDAP stores directory settings; a redacted password keeps the old one.
func (s *Service) SaveLDAP(ctx context.Context, c LDAPConfig) error {
	old, err := s.LDAP(ctx)
	if err != nil {
		return err
	}
	if c.BindPassword == "" || c.BindPassword == "********" {
		c.BindPassword = old.BindPassword
	}
	if c.URL != "" && !strings.Contains(c.UserFilter, "%s") {
		return invalidf("the user filter must contain %%s for the user name")
	}
	return s.settings.Set(ctx, "ldap", c)
}

func (s *Service) ldapSession(ctx context.Context) (ldapConn, LDAPConfig, error) {
	c, err := s.LDAP(ctx)
	if err != nil {
		return nil, c, err
	}
	if c.URL == "" {
		return nil, c, errors.New("directory is not configured")
	}
	conn, err := s.ldap(c)
	if err != nil {
		return nil, c, err
	}
	if err := conn.Bind(c.BindDN, c.BindPassword); err != nil {
		conn.Close()
		return nil, c, fmt.Errorf("service account bind: %w", err)
	}
	return conn, c, nil
}

// ldapCheck finds the user's DN with the service account and binds as the
// user to check the password.
func (s *Service) ldapCheck(ctx context.Context, username, password string) (bool, error) {
	if password == "" {
		return false, nil // an empty password would be an anonymous bind
	}
	conn, c, err := s.ldapSession(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	res, err := conn.Search(ldap.NewSearchRequest(c.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 15, false,
		fmt.Sprintf(c.UserFilter, ldap.EscapeFilter(username)), []string{"dn"}, nil))
	if err != nil {
		return false, err
	}
	if len(res.Entries) != 1 {
		return false, nil
	}
	if err := conn.Bind(res.Entries[0].DN, password); err != nil {
		var le *ldap.Error
		if errors.As(err, &le) && le.ResultCode == ldap.LDAPResultInvalidCredentials {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// DirectoryUser is a user found in the directory.
type DirectoryUser struct {
	Username string `json:"username"`
	Name     string `json:"name"`
	Mobile   string `json:"mobile"`
	Exists   bool   `json:"exists"`
	Valid    bool   `json:"valid"` // has a usable mobile number
}

// SearchDirectory lists users matching the import filter.
func (s *Service) SearchDirectory(ctx context.Context) ([]DirectoryUser, error) {
	conn, c, err := s.ldapSession(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	res, err := conn.Search(ldap.NewSearchRequest(c.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2000, 30, false,
		c.ImportFilter, []string{c.UserAttr, c.MobileAttr, c.NameAttr}, nil))
	if err != nil && !ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded) {
		return nil, err
	}
	existing, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, u := range existing {
		have[strings.ToLower(u.Username)] = true
	}
	out := []DirectoryUser{}
	for _, e := range res.Entries {
		d := DirectoryUser{Username: e.GetAttributeValue(c.UserAttr), Name: e.GetAttributeValue(c.NameAttr), Mobile: e.GetAttributeValue(c.MobileAttr)}
		if d.Username == "" {
			continue
		}
		if m, err := sms.NormalizeMobile(d.Mobile); err == nil {
			d.Mobile, d.Valid = m, true
		}
		d.Exists = have[strings.ToLower(d.Username)]
		out = append(out, d)
	}
	return out, nil
}

// ImportDirectory adds the named directory users as LDAP users.
func (s *Service) ImportDirectory(ctx context.Context, usernames []string, group string) (int, error) {
	found, err := s.SearchDirectory(ctx)
	if err != nil {
		return 0, err
	}
	want := map[string]bool{}
	for _, u := range usernames {
		want[strings.ToLower(u)] = true
	}
	var add []DirectoryUser
	for _, d := range found {
		if want[strings.ToLower(d.Username)] && d.Valid && !d.Exists {
			add = append(add, d)
		}
	}
	if err := s.checkCapacity(ctx, len(add)); err != nil {
		return 0, err
	}
	for _, d := range add {
		if _, err := s.Create(ctx, UserInput{Username: d.Username, Name: d.Name, Mobile: d.Mobile, Source: "ldap", FortinetGroup: group, Active: true}); err != nil {
			return 0, err
		}
	}
	return len(add), nil
}
