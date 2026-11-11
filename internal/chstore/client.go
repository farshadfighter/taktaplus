// Package chstore talks to ClickHouse over its HTTP interface and owns the
// log schema. Values are always passed as query parameters ({name:Type}),
// never spliced into SQL.
package chstore

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a ClickHouse HTTP client bound to one database.
type Client struct {
	endpoint string // scheme://host:port/
	user     string
	password string
	database string
	hc       *http.Client
}

// Open parses a URL such as http://user:pass@clickhouse:8123/taktaplus and
// checks that the server answers.
func Open(ctx context.Context, raw string) (*Client, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("chstore: URL must look like http://user:password@host:8123/database")
	}
	c := &Client{
		endpoint: u.Scheme + "://" + u.Host + "/",
		database: strings.Trim(u.Path, "/"),
		hc:       &http.Client{Timeout: 5 * time.Minute},
	}
	if c.database == "" {
		c.database = "default"
	}
	if u.User != nil {
		c.user = u.User.Username()
		c.password, _ = u.User.Password()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := c.Exec(ctx, "SELECT 1", nil); err != nil {
		return nil, fmt.Errorf("chstore: connect: %w", err)
	}
	return c, nil
}

// Database is the database the client works in.
func (c *Client) Database() string { return c.database }

// WithDatabase returns a client for another database on the same server.
func (c *Client) WithDatabase(db string) *Client {
	cp := *c
	cp.database = db
	return &cp
}

// Params are query parameters, referenced in SQL as {name:Type}.
type Params map[string]any

func (c *Client) do(ctx context.Context, query string, params Params, body io.Reader) (*http.Response, error) {
	q := url.Values{}
	q.Set("database", c.database)
	q.Set("session_timezone", "UTC")
	q.Set("date_time_output_format", "iso")
	q.Set("output_format_json_quote_64bit_integers", "0")
	q.Set("date_time_input_format", "best_effort")
	q.Set("input_format_skip_unknown_fields", "1")
	for k, v := range params {
		q.Set("param_"+k, paramString(v))
	}
	var req *http.Request
	var err error
	if body == nil {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"?"+q.Encode(), strings.NewReader(query))
	} else {
		q.Set("query", query)
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"?"+q.Encode(), body)
	}
	if err != nil {
		return nil, err
	}
	if c.user != "" {
		req.Header.Set("X-ClickHouse-User", c.user)
		req.Header.Set("X-ClickHouse-Key", c.password)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("clickhouse: %s", strings.TrimSpace(string(msg)))
	}
	return resp, nil
}

// paramString renders a value in the text form ClickHouse expects for
// parameters.
func paramString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case time.Time:
		return x.UTC().Format("2006-01-02 15:04:05.000")
	case []string:
		var b strings.Builder
		b.WriteByte('[')
		for i, s := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteByte('\'')
			b.WriteString(strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s))
			b.WriteByte('\'')
		}
		b.WriteByte(']')
		return b.String()
	case []uint32:
		parts := make([]string, len(x))
		for i, n := range x {
			parts[i] = fmt.Sprint(n)
		}
		return "[" + strings.Join(parts, ",") + "]"
	default:
		return fmt.Sprint(v)
	}
}

// Exec runs a statement and discards its output.
func (c *Client) Exec(ctx context.Context, query string, params Params) error {
	resp, err := c.do(ctx, query, params, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}

// Rows streams JSONEachRow results.
type Rows struct {
	body io.ReadCloser
	sc   *bufio.Scanner
	err  error
}

// Query runs a SELECT; FORMAT JSONEachRow is appended.
func (c *Client) Query(ctx context.Context, query string, params Params) (*Rows, error) {
	resp, err := c.do(ctx, query+" FORMAT JSONEachRow", params, nil)
	if err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	return &Rows{body: resp.Body, sc: sc}, nil
}

// Next decodes the next row into v; it returns false at the end or on error.
func (r *Rows) Next(v any) bool {
	if r.err != nil || !r.sc.Scan() {
		return false
	}
	line := r.sc.Bytes()
	// ClickHouse reports errors that happen mid-stream as a trailing text line.
	if !bytes.HasPrefix(line, []byte("{")) {
		r.err = fmt.Errorf("clickhouse: %s", strings.TrimSpace(string(line)))
		return false
	}
	if err := json.Unmarshal(line, v); err != nil {
		r.err = err
		return false
	}
	return true
}

// Err reports the first error seen while reading.
func (r *Rows) Err() error {
	if r.err != nil {
		return r.err
	}
	return r.sc.Err()
}

// Close releases the response.
func (r *Rows) Close() error { return r.body.Close() }

// Select runs a query and decodes every row into a new T.
func Select[T any](ctx context.Context, c *Client, query string, params Params) ([]T, error) {
	rows, err := c.Query(ctx, query, params)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for {
		var v T
		if !rows.Next(&v) {
			break
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Insert writes rows (each marshalled to one JSON object) into table.
func (c *Client) Insert(ctx context.Context, table string, rows []any) error {
	if len(rows) == 0 {
		return nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	resp, err := c.do(ctx, "INSERT INTO "+table+" FORMAT JSONEachRow", nil, &buf)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}
