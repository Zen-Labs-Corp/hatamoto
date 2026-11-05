package jsonx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSON(rec, http.StatusCreated, map[string]any{"b": "<x>", "a": []string(nil)})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if got, want := rec.Body.String(), `{"a":[],"b":"\u003cx\u003e"}`+"\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestWriteJSONNilWritesNoBody(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSON(rec, http.StatusNoContent, nil)
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("code %d body %q", rec.Code, rec.Body.String())
	}
}

func TestWriteJSONEncodeErrorIs500(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSON(rec, http.StatusOK, make(chan int))
	if rec.Code != http.StatusInternalServerError || rec.Body.String() != `{"error":"internal error"}`+"\n" {
		t.Fatalf("code %d body %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type %q", ct)
	}
}

func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, http.StatusNotFound, "nope")
	if rec.Code != http.StatusNotFound || rec.Body.String() != `{"error":"nope"}`+"\n" {
		t.Fatalf("code %d body %q", rec.Code, rec.Body.String())
	}
}

func decode(t *testing.T, body string, max int64) (int, named, error) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	var v named
	status, err := DecodeRequest(httptest.NewRecorder(), r, &v, max)
	return status, v, err
}

func TestDecodeRequest(t *testing.T) {
	if status, v, err := decode(t, `{"name":"a"}`, 0); status != 0 || err != nil || v.Name != "a" {
		t.Fatalf("ok: %d %v %+v", status, err, v)
	}
	if status, _, err := decode(t, `{"name":`, 0); status != http.StatusBadRequest || err == nil {
		t.Fatalf("bad json: %d %v", status, err)
	}
	if status, _, err := decode(t, `{"name":"a"} junk`, 0); status != http.StatusBadRequest || err == nil {
		t.Fatalf("trailing: %d %v", status, err)
	}
	if status, _, err := decode(t, `{"name":"`+strings.Repeat("a", 100)+`"}`, 32); status != http.StatusRequestEntityTooLarge || err == nil {
		t.Fatalf("over limit: %d %v", status, err)
	}
}

func TestDecodeRequestNilBody(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Body = nil
	var v named
	status, err := DecodeRequest(httptest.NewRecorder(), r, &v, 0)
	if status != http.StatusBadRequest || err == nil || err.Error() != "missing request body" {
		t.Fatalf("nil body: %d %v", status, err)
	}
}
