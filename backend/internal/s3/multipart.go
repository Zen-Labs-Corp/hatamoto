package s3

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// partSize is the multipart part size and the single-request limit. A variable
// so tests can use tiny parts.
var partSize int64 = 16 << 20

const abortTimeout = 30 * time.Second

func (c *Client) putMultipart(ctx context.Context, key string, r io.ReaderAt, size int64, meta map[string]string) (err error) {
	resp, err := c.do(ctx, request{op: "multipart start", method: http.MethodPost, key: key,
		query: url.Values{"uploads": {""}}, header: metaHeader(meta)})
	if err != nil {
		return err
	}
	var init struct {
		UploadID string `xml:"UploadId"`
	}
	derr := xml.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&init)
	resp.Body.Close()
	if derr != nil || init.UploadID == "" {
		return fmt.Errorf("s3 multipart start: bad response")
	}
	defer func() {
		if err != nil {
			// Abort on a context that survives cancellation of the failed upload.
			abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), abortTimeout)
			defer cancel()
			if aresp, aerr := c.do(abortCtx, request{op: "multipart abort", method: http.MethodDelete,
				key: key, query: url.Values{"uploadId": {init.UploadID}}}); aerr == nil {
				drain(aresp)
			}
		}
	}()

	var etags []string
	for off, n := int64(0), 1; off < size; off, n = off+partSize, n+1 {
		length := min(partSize, size-off)
		sum, herr := hashRange(r, off, length)
		if herr != nil {
			return fmt.Errorf("s3 multipart: read source: %w", herr)
		}
		presp, perr := c.doRetry(ctx, func() request {
			return request{op: "multipart part", method: http.MethodPut, key: key,
				query: url.Values{"partNumber": {strconv.Itoa(n)}, "uploadId": {init.UploadID}},
				body:  io.NewSectionReader(r, off, length), size: length, payload: sum}
		})
		if perr != nil {
			return perr
		}
		etag := presp.Header.Get("Etag")
		drain(presp)
		if etag == "" {
			return fmt.Errorf("s3 multipart part %d: no ETag returned", n)
		}
		etags = append(etags, etag)
	}

	var body bytes.Buffer
	body.WriteString("<CompleteMultipartUpload>")
	for i, e := range etags {
		fmt.Fprintf(&body, "<Part><PartNumber>%d</PartNumber><ETag>%s</ETag></Part>", i+1, xmlEscape(e))
	}
	body.WriteString("</CompleteMultipartUpload>")
	payload := body.Bytes()
	cresp, err := c.do(ctx, request{op: "multipart complete", method: http.MethodPost, key: key,
		query:  url.Values{"uploadId": {init.UploadID}},
		header: http.Header{"Content-Type": {"application/xml"}},
		body:   bytes.NewReader(payload), size: int64(len(payload)), payload: hexSHA256(payload)})
	if err != nil {
		return err
	}
	defer cresp.Body.Close()
	// S3 can answer 200 and still carry an <Error> document.
	b, _ := io.ReadAll(io.LimitReader(cresp.Body, 1<<20))
	if strings.Contains(string(b), "<Error>") {
		var x struct {
			Code string `xml:"Code"`
		}
		_ = xml.Unmarshal(b, &x)
		return &Error{Op: "multipart complete", Status: cresp.StatusCode, Code: cleanCode(x.Code)}
	}
	return nil
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
