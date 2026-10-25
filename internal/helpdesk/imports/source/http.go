package source

import (
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

	"github.com/useopendesk/opendesk/internal/netsafe"
)

// Options configure how a source reaches its API.
type Options struct {
	// Dev allows http and loopback targets (development instances and tests).
	Dev bool
	// BaseURL replaces the API's address (tests); "" uses the source's own.
	BaseURL string
	// HTTP is the client for API calls; nil is a guarded client (netsafe).
	HTTP *http.Client
	// Files is the client for attachment downloads; nil is a guarded client.
	Files *http.Client
}

// Factory builds a source from its credentials. It checks their shape only; Check signs in.
type Factory func(c Credentials, o Options) (Source, error)

// maxJSON bounds one API answer.
const maxJSON = 32 << 20

// Client calls one source's API: JSON in and out, its credentials on every request to
// its own host and nowhere else.
type Client struct {
	Base  string // "https://acme.zendesk.com"
	HTTP  *http.Client
	Files *http.Client
	Dev   bool
	// Auth adds the credentials to a request for Base's host.
	Auth func(*http.Request)
	// NotFound is what a 404 on the account means (ErrNotFound while checking a new
	// account); nil makes a 404 a RemoteError.
	NotFound error
	// Header is added to every API request (Intercom-Version).
	Header http.Header
}

// NewClient returns a Client for base with o's clients, or guarded ones.
func NewClient(base string, o Options) *Client {
	if o.BaseURL != "" {
		base = o.BaseURL
	}
	c := &Client{Base: strings.TrimRight(base, "/"), HTTP: o.HTTP, Files: o.Files, Dev: o.Dev}
	if c.HTTP == nil {
		c.HTTP = netsafe.Client(o.Dev, 60*time.Second)
	}
	if c.Files == nil {
		c.Files = netsafe.Client(o.Dev, 3*time.Minute)
	}
	return c
}

// URL joins a path (and query) to Base. A full URL (a next-page link) is used as it is
// when it is on Base's host.
func (c *Client) URL(path string, q url.Values) (string, error) {
	raw := path
	if !strings.HasPrefix(path, "https://") && !strings.HasPrefix(path, "http://") {
		raw = c.Base + path
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	base, _ := url.Parse(c.Base)
	if base == nil || !strings.EqualFold(u.Host, base.Host) || u.Scheme != base.Scheme {
		return "", fmt.Errorf("refusing a link off the API origin")
	}
	if len(q) > 0 {
		v := u.Query()
		for k, vals := range q {
			v[k] = vals
		}
		u.RawQuery = v.Encode()
	}
	return u.String(), nil
}

// Get fetches path as JSON into out.
func (c *Client) Get(ctx context.Context, path string, q url.Values, out any) error {
	return c.Do(ctx, http.MethodGet, path, q, nil, out)
}

// Do sends a request with an optional JSON body and decodes a JSON answer into out.
func (c *Client) Do(ctx context.Context, method, path string, q url.Values, body, out any) error {
	resp, err := c.Raw(ctx, method, path, q, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return nil
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, maxJSON))
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("read %s: %w", strings.SplitN(path, "?", 2)[0], err)
	}
	return nil
}

// Raw sends a request and returns a successful response; the caller closes it.
func (c *Client) Raw(ctx context.Context, method, path string, q url.Values, body any) (*http.Response, error) {
	u, err := c.URL(path, q)
	if err != nil {
		return nil, err
	}
	var rd io.Reader
	if body != nil {
		switch b := body.(type) {
		case url.Values:
			rd = strings.NewReader(b.Encode())
		default:
			buf, err := json.Marshal(b)
			if err != nil {
				return nil, err
			}
			rd = bytes.NewReader(buf)
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "opendesk-import/1 (+https://opendesk.sh)")
	if body != nil {
		if _, ok := body.(url.Values); ok {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		} else {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	for k, v := range c.Header {
		req.Header[k] = v
	}
	if c.Auth != nil {
		c.Auth(req)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, unreachable(err)
	}
	if err := Status(resp, c.NotFound); err != nil {
		_ = resp.Body.Close()
		return nil, err
	}
	return resp, nil
}

// Unreachable wraps a transport failure: the source did not answer.
type Unreachable struct{ Err error }

func (e *Unreachable) Error() string { return "the source did not answer: " + e.Err.Error() }
func (e *Unreachable) Unwrap() error { return e.Err }

func unreachable(err error) error {
	// Our own shutdown or cancel is not the source's fault.
	if errors.Is(err, context.Canceled) {
		return err
	}
	return &Unreachable{Err: err}
}

// Open downloads a file at raw, following at most three redirects, each re-checked by the
// outbound URL rules. The source's credentials go only to its API host (Auth), never to
// a redirect target elsewhere (file storage, a CDN).
func (c *Client) Open(ctx context.Context, raw string) (io.ReadCloser, error) {
	g := netsafe.Guard{Dev: c.Dev}
	base, _ := url.Parse(c.Base)
	for hop := 0; hop < 4; hop++ {
		valid, err := g.ValidateURL(ctx, raw)
		if err != nil {
			return nil, fmt.Errorf("attachment link: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, valid, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "opendesk-import/1 (+https://opendesk.sh)")
		if base != nil && strings.EqualFold(req.URL.Host, base.Host) {
			for k, v := range c.Header {
				req.Header[k] = v
			}
			if c.Auth != nil {
				c.Auth(req)
			}
		}
		onAPI := base != nil && strings.EqualFold(req.URL.Host, base.Host)
		resp, err := c.Files.Do(req)
		if err != nil {
			return nil, unreachable(err)
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			loc := resp.Header.Get("Location")
			_ = resp.Body.Close()
			if loc == "" {
				return nil, &RemoteError{Status: resp.StatusCode, Detail: "redirect without a location"}
			}
			next, err := req.URL.Parse(loc)
			if err != nil {
				return nil, err
			}
			raw = next.String()
			continue
		}
		// A file host refusing a link (an expired signed URL) is that file's problem, not
		// refused credentials.
		if !onAPI && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			_ = resp.Body.Close()
			return nil, &RemoteError{Status: resp.StatusCode, Detail: "the file host refused the link"}
		}
		if err := Status(resp, nil); err != nil {
			_ = resp.Body.Close()
			return nil, err
		}
		return resp.Body, nil
	}
	return nil, errors.New("attachment link: too many redirects")
}
