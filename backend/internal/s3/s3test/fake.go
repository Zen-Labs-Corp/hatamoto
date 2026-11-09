// Package s3test is an in-memory S3 server for tests. It re-verifies every
// SigV4 signature and every payload hash, so a client that signs wrongly or
// lies about its body fails here the way it would against a real provider.
package s3test

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Credentials every Server accepts.
const (
	Region    = "test-region-1"
	Bucket    = "backups"
	AccessKey = "AKIATESTACCESSKEY"
	SecretKey = "test-secret-key-0123456789abcdef" // gitleaks:allow
)

// Object is a stored object.
type Object struct {
	Data []byte
	Meta map[string]string // lower-case keys
}

// Request is one received request.
type Request struct {
	Method string
	Path   string
	Query  string
	Header http.Header
}

type upload struct {
	key   string
	meta  map[string]string
	parts map[int][]byte
}

// Server is the fake. Create it with NewServer.
type Server struct {
	URL string

	// PageSize caps keys per list page (default 1000).
	PageSize int
	// FailPart, when >0, answers that part number with a 500.
	FailPart int
	// FlakyPart, when >0, answers that part number once with a 503 SlowDown.
	FlakyPart int
	// Tamper, when set, may change an object right after it was stored, to
	// model a provider that keeps something other than what it was sent.
	Tamper func(key string, o *Object)

	mu      sync.Mutex
	objects map[string]Object
	uploads map[string]*upload
	log     []Request
	nextID  int
	srv     *httptest.Server
}

// NewServer starts a fake bucket named Bucket and stops it with the test.
func NewServer(t testing.TB) *Server {
	t.Helper()
	s := &Server{PageSize: 1000, objects: map[string]Object{}, uploads: map[string]*upload{}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	s.URL = s.srv.URL
	t.Cleanup(s.srv.Close)
	return s
}

// Objects returns a copy of the stored objects keyed by object key.
func (s *Server) Objects() map[string]Object {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Object, len(s.objects))
	for k, o := range s.objects {
		out[k] = o
	}
	return out
}

// Put stores an object directly, bypassing the wire.
func (s *Server) Put(key string, data []byte, meta map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = Object{Data: append([]byte(nil), data...), Meta: meta}
}

// Corrupt flips one byte of the stored object, keeping its size.
func (s *Server) Corrupt(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.objects[key]
	d := append([]byte(nil), o.Data...)
	if len(d) > 0 {
		d[len(d)/2] ^= 0xff
	}
	o.Data = d
	s.objects[key] = o
}

// Truncate drops the last byte of the stored object.
func (s *Server) Truncate(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := s.objects[key]
	if len(o.Data) > 0 {
		o.Data = o.Data[:len(o.Data)-1]
	}
	s.objects[key] = o
}

// OpenUploads is the number of multipart uploads neither completed nor aborted.
func (s *Server) OpenUploads() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.uploads)
}

// Requests returns the request log.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.log...)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = append(s.log, Request{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone()})
	if code := verify(r, body); code != "" {
		fail(w, http.StatusForbidden, code)
		return
	}
	// Real providers refuse a chunked request body (411 or 501).
	if len(r.TransferEncoding) > 0 || r.ContentLength < 0 {
		fail(w, http.StatusLengthRequired, "MissingContentLength")
		return
	}
	for k := range r.Header {
		if strings.HasPrefix(strings.ToLower(k), "x-amz-checksum-") {
			fail(w, http.StatusBadRequest, "InvalidRequest")
			return
		}
	}
	prefix := "/" + Bucket
	if r.URL.Path != prefix && !strings.HasPrefix(r.URL.Path, prefix+"/") {
		fail(w, http.StatusNotFound, "NoSuchBucket")
		return
	}
	key := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, prefix), "/")
	q := r.URL.Query()
	switch {
	case key == "":
		s.bucket(w, r, q)
	case r.Method == http.MethodPost && q.Has("uploads"):
		s.nextID++
		id := "upload-" + strconv.Itoa(s.nextID)
		s.uploads[id] = &upload{key: key, meta: metaOf(r), parts: map[int][]byte{}}
		fmt.Fprintf(w, "<InitiateMultipartUploadResult><UploadId>%s</UploadId></InitiateMultipartUploadResult>", id)
	case r.Method == http.MethodPut && q.Has("uploadId"):
		s.part(w, q, body)
	case r.Method == http.MethodPost && q.Has("uploadId"):
		s.complete(w, q, key, body)
	case r.Method == http.MethodDelete && q.Has("uploadId"):
		delete(s.uploads, q.Get("uploadId"))
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPut:
		s.store(key, Object{Data: body, Meta: metaOf(r)})
		w.Header().Set("ETag", etag(body))
	case r.Method == http.MethodDelete:
		delete(s.objects, key)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		o, ok := s.objects[key]
		if !ok {
			fail(w, http.StatusNotFound, "NoSuchKey")
			return
		}
		for k, v := range o.Meta {
			w.Header().Set("X-Amz-Meta-"+k, v)
		}
		w.Header().Set("ETag", etag(o.Data))
		data := o.Data
		status := http.StatusOK
		if rng := r.Header.Get("Range"); rng != "" && r.Method == http.MethodGet {
			start, end, ok := parseRange(rng, len(o.Data))
			if !ok {
				w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", len(o.Data)))
				fail(w, http.StatusRequestedRangeNotSatisfiable, "InvalidRange")
				return
			}
			data = o.Data[start : end+1]
			status = http.StatusPartialContent
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(o.Data)))
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		if r.Method == http.MethodGet {
			w.WriteHeader(status)
			_, _ = w.Write(data)
		}
	default:
		fail(w, http.StatusMethodNotAllowed, "MethodNotAllowed")
	}
}

func (s *Server) bucket(w http.ResponseWriter, r *http.Request, q map[string][]string) {
	if r.Method == http.MethodHead {
		return
	}
	get := func(k string) string {
		if v := q[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	prefix, token := get("prefix"), get("continuation-token")
	limit := s.PageSize
	if n, err := strconv.Atoi(get("max-keys")); err == nil && n > 0 && n < limit {
		limit = n
	}
	var keys []string
	for k := range s.objects {
		if strings.HasPrefix(k, prefix) && k > token {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	truncated := len(keys) > limit
	if truncated {
		keys = keys[:limit]
	}
	var b strings.Builder
	b.WriteString("<ListBucketResult>")
	fmt.Fprintf(&b, "<IsTruncated>%t</IsTruncated>", truncated)
	if truncated {
		fmt.Fprintf(&b, "<NextContinuationToken>%s</NextContinuationToken>", keys[len(keys)-1])
	}
	for _, k := range keys {
		fmt.Fprintf(&b, "<Contents><Key>%s</Key><Size>%d</Size><ETag>%s</ETag><LastModified>2026-01-02T03:04:05Z</LastModified></Contents>",
			k, len(s.objects[k].Data), etag(s.objects[k].Data))
	}
	b.WriteString("</ListBucketResult>")
	_, _ = io.WriteString(w, b.String())
}

func (s *Server) part(w http.ResponseWriter, q map[string][]string, body []byte) {
	u := s.uploads[q["uploadId"][0]]
	n, _ := strconv.Atoi(q["partNumber"][0])
	if u == nil {
		fail(w, http.StatusNotFound, "NoSuchUpload")
		return
	}
	if s.FailPart > 0 && n == s.FailPart {
		fail(w, http.StatusInternalServerError, "InternalError")
		return
	}
	if s.FlakyPart > 0 && n == s.FlakyPart {
		s.FlakyPart = 0
		fail(w, http.StatusServiceUnavailable, "SlowDown")
		return
	}
	u.parts[n] = body
	w.Header().Set("ETag", etag(body))
}

func (s *Server) complete(w http.ResponseWriter, q map[string][]string, key string, body []byte) {
	id := q["uploadId"][0]
	u := s.uploads[id]
	if u == nil {
		fail(w, http.StatusNotFound, "NoSuchUpload")
		return
	}
	var req struct {
		Parts []struct {
			N    int    `xml:"PartNumber"`
			ETag string `xml:"ETag"`
		} `xml:"Part"`
	}
	if err := xml.Unmarshal(body, &req); err != nil || len(req.Parts) == 0 {
		fail(w, http.StatusBadRequest, "MalformedXML")
		return
	}
	var data []byte
	for i, p := range req.Parts {
		part, ok := u.parts[p.N]
		if p.N != i+1 || !ok || p.ETag != etag(part) {
			fail(w, http.StatusBadRequest, "InvalidPart")
			return
		}
		data = append(data, part...)
	}
	s.store(key, Object{Data: data, Meta: u.meta})
	delete(s.uploads, id)
	_, _ = io.WriteString(w, "<CompleteMultipartUploadResult><Key>"+key+"</Key></CompleteMultipartUploadResult>")
}

func (s *Server) store(key string, o Object) {
	if s.Tamper != nil {
		s.Tamper(key, &o)
	}
	s.objects[key] = o
}

func metaOf(r *http.Request) map[string]string {
	m := map[string]string{}
	for k, vs := range r.Header {
		if n, ok := strings.CutPrefix(strings.ToLower(k), "x-amz-meta-"); ok {
			m[n] = vs[0]
		}
	}
	return m
}

func etag(b []byte) string {
	sum := md5.Sum(b)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

func fail(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	fmt.Fprintf(w, "<Error><Code>%s</Code><Message>fake</Message></Error>", code)
}

// verify re-computes the signature and the payload hash. It returns an S3
// error code, or "" when the request is valid.
func verify(r *http.Request, body []byte) string {
	auth := r.Header.Get("Authorization")
	rest, ok := strings.CutPrefix(auth, "AWS4-HMAC-SHA256 ")
	if !ok {
		return "AccessDenied"
	}
	fields := map[string]string{}
	for _, part := range strings.Split(rest, ", ") {
		k, v, _ := strings.Cut(part, "=")
		fields[k] = v
	}
	cred := strings.Split(fields["Credential"], "/")
	if len(cred) != 5 || cred[0] != AccessKey {
		return "InvalidAccessKeyId"
	}
	date, region, service := cred[1], cred[2], cred[3]
	if region != Region || service != "s3" {
		return "SignatureDoesNotMatch"
	}
	payload := r.Header.Get("X-Amz-Content-Sha256")
	sum := sha256.Sum256(body)
	if payload != hex.EncodeToString(sum[:]) {
		return "XAmzContentSHA256Mismatch"
	}
	var hb strings.Builder
	for _, name := range strings.Split(fields["SignedHeaders"], ";") {
		v := strings.Join(strings.Fields(strings.Join(r.Header.Values(name), ",")), " ")
		if name == "host" {
			v = r.Host
		}
		hb.WriteString(name + ":" + v + "\n")
	}
	canonical := strings.Join([]string{r.Method, enc(r.URL.Path, false), canonQuery(r), hb.String(),
		fields["SignedHeaders"], payload}, "\n")
	cs := sha256.Sum256([]byte(canonical))
	scope := strings.Join(cred[1:], "/")
	toSign := "AWS4-HMAC-SHA256\n" + r.Header.Get("X-Amz-Date") + "\n" + scope + "\n" + hex.EncodeToString(cs[:])
	k := mac([]byte("AWS4"+SecretKey), date)
	k = mac(k, region)
	k = mac(k, service)
	k = mac(k, "aws4_request")
	if hex.EncodeToString(mac(k, toSign)) != fields["Signature"] {
		return "SignatureDoesNotMatch"
	}
	return ""
}

func canonQuery(r *http.Request) string {
	var pairs []string
	for k, vs := range r.URL.Query() {
		for _, v := range vs {
			pairs = append(pairs, enc(k, true)+"="+enc(v, true))
		}
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "&")
}

func enc(s string, slash bool) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.IndexByte("-_.~", c) >= 0 || (c == '/' && !slash) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func mac(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

// parseRange reads one "bytes=a-b", "bytes=a-" or "bytes=-n" spec against an
// object of size bytes and returns the inclusive first and last byte.
func parseRange(h string, size int) (start, end int, ok bool) {
	spec, found := strings.CutPrefix(h, "bytes=")
	first, last, found2 := strings.Cut(spec, "-")
	if !found || !found2 || size == 0 {
		return 0, 0, false
	}
	a, errA := strconv.Atoi(first)
	b, errB := strconv.Atoi(last)
	switch {
	case first == "" && errB == nil && b > 0: // the last b bytes
		return max(size-b, 0), size - 1, true
	case errA != nil || a >= size:
		return 0, 0, false
	case last == "":
		return a, size - 1, true
	case errB != nil || b < a:
		return 0, 0, false
	}
	return a, min(b, size-1), true
}
