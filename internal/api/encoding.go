package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
)

func respond(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Debug("write http response", "error", err)
	}
}

func decodeRaw(w http.ResponseWriter, r *http.Request, out any) ([]byte, error) {
	return decodeRawLimit(w, r, out, 1<<20)
}

func decodeRawLimit(w http.ResponseWriter, r *http.Request, out any, limit int64) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, problem(400, "请求过大或无法读取", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(out); err != nil {
		return raw, problem(400, "请求不是有效的 JSON", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return raw, invalid("请求必须只有一个 JSON 对象")
	}
	return raw, nil
}

func decode(w http.ResponseWriter, r *http.Request, out any) error {
	_, err := decodeRaw(w, r, out)
	return err
}
