package s3

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	algorithm   = "AWS4-HMAC-SHA256"
	serviceS3   = "s3"
	dateLayout  = "20060102T150405Z"
	shortLayout = "20060102"
)

// sign adds X-Amz-Date and Authorization (Signature Version 4) to req. Every
// header already on req, plus Host, is signed, so callers set x-amz-* and
// content headers first. payloadHash is the hex SHA-256 of the body.
func sign(req *http.Request, service, payloadHash, region, accessKey, secretKey string, t time.Time) {
	t = t.UTC()
	req.Header.Set("X-Amz-Date", t.Format(dateLayout))
	canonical, signed := canonicalRequest(req, payloadHash)
	scope := strings.Join([]string{t.Format(shortLayout), region, service, "aws4_request"}, "/")
	toSign := strings.Join([]string{algorithm, t.Format(dateLayout), scope, hexSHA256([]byte(canonical))}, "\n")

	k := hmacSHA256([]byte("AWS4"+secretKey), t.Format(shortLayout))
	k = hmacSHA256(k, region)
	k = hmacSHA256(k, service)
	k = hmacSHA256(k, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(k, toSign))

	req.Header.Set("Authorization", algorithm+" Credential="+accessKey+"/"+scope+
		", SignedHeaders="+signed+", Signature="+sig)
}

// canonicalRequest returns the canonical request text and the signed header list.
func canonicalRequest(req *http.Request, payloadHash string) (canonical, signedHeaders string) {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	headers := map[string]string{"host": host}
	for k, vs := range req.Header {
		lk := strings.ToLower(k)
		if lk == "authorization" {
			continue
		}
		parts := make([]string, len(vs))
		for i, v := range vs {
			parts[i] = strings.Join(strings.Fields(v), " ")
		}
		headers[lk] = strings.Join(parts, ",")
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	var hb strings.Builder
	for _, k := range names {
		hb.WriteString(k + ":" + headers[k] + "\n")
	}
	signedHeaders = strings.Join(names, ";")
	path := req.URL.Path
	if path == "" {
		path = "/"
	}
	canonical = strings.Join([]string{
		req.Method, uriEncode(path, false), canonicalQuery(req.URL.Query()),
		hb.String(), signedHeaders, payloadHash,
	}, "\n")
	return canonical, signedHeaders
}

// canonicalQuery encodes and sorts query parameters.
func canonicalQuery(q url.Values) string {
	pairs := make([]string, 0, len(q))
	for k, vs := range q {
		for _, v := range vs {
			pairs = append(pairs, uriEncode(k, true)+"="+uriEncode(v, true))
		}
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "&")
}

// uriEncode percent-encodes s as SigV4 requires; slash is kept for paths.
func uriEncode(s string, encodeSlash bool) string {
	const hexUpper = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexUpper[c>>4])
			b.WriteByte(hexUpper[c&15])
		}
	}
	return b.String()
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func hexSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
