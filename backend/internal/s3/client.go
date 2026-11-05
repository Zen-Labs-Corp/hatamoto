// Package s3 is a small S3-compatible client: Signature Version 4 over
// net/http, path-style URLs and the handful of operations off-site backups
// need. It imports nothing from hatamoto and never logs keys or signatures.
package s3

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Config configures a Client.
type Config struct {
	Endpoint  string // https://host[:port], no path
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	HTTP      *http.Client     // nil: a default client; redirects are never followed
	Now       func() time.Time // nil: time.Now
}

// Object describes a stored object.
type Object struct {
	Key          string
	Size         int64
	ETag         string
	LastModified time.Time
	Meta         map[string]string // user metadata, keys lower-case, from Head and Get only
}

// Error is a failed request. It carries the status and provider code only:
// never a URL, header or credential.
type Error struct {
	Op     string
	Status int
	Code   string
}

func (e *Error) Error() string {
	s := fmt.Sprintf("s3 %s: status %d", e.Op, e.Status)
	if e.Code != "" {
		s += " " + e.Code
	}
	switch e.Code {
	case "RequestTimeTooSkewed":
		s += " (the server clock differs from the provider's by too much; fix the time)"
	case "SignatureDoesNotMatch", "InvalidAccessKeyId":
		s += " (check the access key, secret key and region)"
	}
	return s
}

// NotFound reports whether err is a 404 from the provider.
func NotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

// Client talks to one bucket.
type Client struct {
	base      *url.URL
	region    string
	bucket    string
	accessKey string
	secretKey string
	http      *http.Client
	now       func() time.Time
}

// ValidateEndpoint checks an endpoint URL: https is required unless the host is
// localhost or a loopback or private IP; no userinfo, path, query or fragment.
func ValidateEndpoint(endpoint string) (*url.URL, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return nil, errors.New("endpoint is not a valid URL")
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("endpoint must be a bare scheme and host")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !localHost(u.Hostname()) {
			return nil, errors.New("endpoint must use https unless it is a local or private address")
		}
	default:
		return nil, errors.New("endpoint must use https")
	}
	u.Path = ""
	return u, nil
}

func localHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(h)
	if err != nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate()
}

// New validates c and returns a Client.
func New(c Config) (*Client, error) {
	u, err := ValidateEndpoint(c.Endpoint)
	if err != nil {
		return nil, err
	}
	if c.Region == "" || c.Bucket == "" || c.AccessKey == "" || c.SecretKey == "" {
		return nil, errors.New("region, bucket, access key and secret key are required")
	}
	hc := &http.Client{}
	if c.HTTP != nil {
		cp := *c.HTTP
		hc = &cp
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	now := c.Now
	if now == nil {
		now = time.Now
	}
	return &Client{base: u, region: c.Region, bucket: c.Bucket, accessKey: c.AccessKey,
		secretKey: c.SecretKey, http: hc, now: now}, nil
}

// request is one signed call.
type request struct {
	op      string
	method  string
	key     string // empty for bucket-level calls
	query   url.Values
	header  http.Header
	body    io.Reader
	size    int64
	payload string // hex SHA-256 of the body
}

const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// do sends r and returns the response; a non-2xx status becomes an *Error.
func (c *Client) do(ctx context.Context, r request) (*http.Response, error) {
	u := *c.base
	u.Path = "/" + c.bucket
	if r.key != "" {
		u.Path += "/" + r.key
	}
	u.RawPath = uriEncode(u.Path, false)
	u.RawQuery = canonicalQuery(r.query)
	if r.payload == "" {
		r.payload = emptySHA256
	}
	body := r.body
	if body == nil {
		body = http.NoBody
	}
	req, err := http.NewRequestWithContext(ctx, r.method, u.String(), io.NopCloser(body))
	if err != nil {
		return nil, fmt.Errorf("s3 %s: %w", r.op, err)
	}
	req.ContentLength = r.size
	for k, vs := range r.header {
		req.Header[k] = vs
	}
	req.Header.Set("X-Amz-Content-Sha256", r.payload)
	sign(req, serviceS3, r.payload, c.region, c.accessKey, c.secretKey, c.now())
	resp, err := c.http.Do(req)
	if err != nil {
		// url.Error embeds the URL, which holds no secret, but keep it terse.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("s3 %s: %w", r.op, err)
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		e := &Error{Op: r.op, Status: resp.StatusCode}
		var x struct {
			Code string `xml:"Code"`
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if xml.Unmarshal(b, &x) == nil {
			e.Code = x.Code
		}
		return nil, e
	}
	return resp, nil
}

// drain reads and closes a response body we do not need.
func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
}

func metaHeader(meta map[string]string) http.Header {
	h := http.Header{}
	for k, v := range meta {
		h.Set("X-Amz-Meta-"+k, v)
	}
	return h
}

func hashRange(r io.ReaderAt, off, n int64) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(r, off, n)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Put stores size bytes of r under key with user metadata meta. Objects up to
// one part go in a single request, larger ones as a multipart upload that is
// aborted on any error. Memory use is bounded: the body is streamed from r,
// hashed in a first pass so the server can verify the bytes.
func (c *Client) Put(ctx context.Context, key string, r io.ReaderAt, size int64, meta map[string]string) error {
	if size <= partSize {
		sum, err := hashRange(r, 0, size)
		if err != nil {
			return fmt.Errorf("s3 put: read source: %w", err)
		}
		resp, err := c.do(ctx, request{op: "put", method: http.MethodPut, key: key,
			header: metaHeader(meta), body: io.NewSectionReader(r, 0, size), size: size, payload: sum})
		if err != nil {
			return err
		}
		drain(resp)
		return nil
	}
	return c.putMultipart(ctx, key, r, size, meta)
}

// Head returns the size, ETag and metadata of key.
func (c *Client) Head(ctx context.Context, key string) (Object, error) {
	resp, err := c.do(ctx, request{op: "head", method: http.MethodHead, key: key})
	if err != nil {
		return Object{}, err
	}
	defer drain(resp)
	return objectFromHeaders(key, resp), nil
}

func objectFromHeaders(key string, resp *http.Response) Object {
	o := Object{Key: key, Size: resp.ContentLength, ETag: strings.Trim(resp.Header.Get("Etag"), `"`),
		Meta: map[string]string{}}
	o.LastModified, _ = http.ParseTime(resp.Header.Get("Last-Modified"))
	for k, vs := range resp.Header {
		if name, ok := strings.CutPrefix(strings.ToLower(k), "x-amz-meta-"); ok && len(vs) > 0 {
			o.Meta[name] = vs[0]
		}
	}
	return o
}

// Get opens key for reading; the caller closes the body.
func (c *Client) Get(ctx context.Context, key string) (io.ReadCloser, Object, error) {
	resp, err := c.do(ctx, request{op: "get", method: http.MethodGet, key: key})
	if err != nil {
		return nil, Object{}, err
	}
	return resp.Body, objectFromHeaders(key, resp), nil
}

// Delete removes key; a missing key is not an error.
func (c *Client) Delete(ctx context.Context, key string) error {
	resp, err := c.do(ctx, request{op: "delete", method: http.MethodDelete, key: key})
	if err != nil {
		if NotFound(err) {
			return nil
		}
		return err
	}
	drain(resp)
	return nil
}

type listResult struct {
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
	Contents              []struct {
		Key          string `xml:"Key"`
		Size         int64  `xml:"Size"`
		ETag         string `xml:"ETag"`
		LastModified string `xml:"LastModified"`
	} `xml:"Contents"`
}

func (c *Client) listPage(ctx context.Context, prefix, token string, maxKeys int) (listResult, error) {
	q := url.Values{"list-type": {"2"}}
	if prefix != "" {
		q.Set("prefix", prefix)
	}
	if token != "" {
		q.Set("continuation-token", token)
	}
	if maxKeys > 0 {
		q.Set("max-keys", strconv.Itoa(maxKeys))
	}
	resp, err := c.do(ctx, request{op: "list", method: http.MethodGet, query: q})
	if err != nil {
		return listResult{}, err
	}
	defer resp.Body.Close()
	var lr listResult
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&lr); err != nil {
		return listResult{}, fmt.Errorf("s3 list: bad response: %w", err)
	}
	return lr, nil
}

// List returns every object whose key starts with prefix, following pagination.
func (c *Client) List(ctx context.Context, prefix string) ([]Object, error) {
	var out []Object
	token := ""
	for {
		lr, err := c.listPage(ctx, prefix, token, 0)
		if err != nil {
			return nil, err
		}
		for _, e := range lr.Contents {
			o := Object{Key: e.Key, Size: e.Size, ETag: strings.Trim(e.ETag, `"`)}
			o.LastModified, _ = time.Parse(time.RFC3339, e.LastModified)
			out = append(out, o)
		}
		if !lr.IsTruncated || lr.NextContinuationToken == "" {
			return out, nil
		}
		token = lr.NextContinuationToken
	}
}

// Test checks that the bucket is reachable with these credentials.
func (c *Client) Test(ctx context.Context) error {
	_, err := c.listPage(ctx, "", "", 1)
	return err
}
