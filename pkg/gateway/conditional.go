package gateway

import (
	"bytes"
	"context"
	"encoding/xml"
	"net/http"
	"strconv"
	"strings"
	"time"

	xhttp "github.com/minio/minio/cmd/http"
)

type Conditions struct {
	IfMatch           []string
	IfNoneMatch       []string
	IfModifiedSince   *time.Time
	IfUnmodifiedSince *time.Time
}

type contextKey string

const targetConditionsContextKey contextKey = "juicefs-gateway-target-conditions"
const conditionalResponseStateContextKey contextKey = "juicefs-gateway-conditional-response-state"

type conditionalFailure struct {
	StatusCode int
	Code       string
	Message    string
}

type conditionalResponseState struct {
	failure *conditionalFailure
}

func WithTargetConditions(ctx context.Context, conds Conditions) context.Context {
	return context.WithValue(ctx, targetConditionsContextKey, conds)
}

func withConditionalResponseState(ctx context.Context, state *conditionalResponseState) context.Context {
	return context.WithValue(ctx, conditionalResponseStateContextKey, state)
}

func TargetConditionsFromContext(ctx context.Context) (Conditions, bool) {
	conds, ok := ctx.Value(targetConditionsContextKey).(Conditions)
	return conds, ok
}

func conditionalResponseStateFromContext(ctx context.Context) (*conditionalResponseState, bool) {
	state, ok := ctx.Value(conditionalResponseStateContextKey).(*conditionalResponseState)
	return state, ok
}

func HasTargetConditions(ctx context.Context) bool {
	conds, ok := TargetConditionsFromContext(ctx)
	return ok && conds.HasWriteConditions()
}

func ConditionalRequestMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conds := ParseConditions(
			r.Header,
			xhttp.IfMatch,
			xhttp.IfNoneMatch,
			xhttp.IfModifiedSince,
			xhttp.IfUnmodifiedSince,
		)
		if conds.HasReadConditions() {
			r = r.WithContext(WithTargetConditions(r.Context(), conds))
		}
		if !shouldBufferConditionalResponse(r.Method, conds) {
			next.ServeHTTP(w, r)
			return
		}

		state := &conditionalResponseState{}
		r = r.WithContext(withConditionalResponseState(r.Context(), state))
		recorder := newBufferedResponseWriter()
		next.ServeHTTP(recorder, r)
		if !rewriteConditionalFailureResponse(w, r, recorder, state) {
			recorder.FlushTo(w)
		}
	})
}

func shouldBufferConditionalResponse(method string, conds Conditions) bool {
	if !conds.HasWriteConditions() {
		return false
	}
	switch method {
	case http.MethodPut, http.MethodPost, http.MethodDelete:
		return true
	default:
		return false
	}
}

func (c Conditions) HasReadConditions() bool {
	return len(c.IfMatch) > 0 || len(c.IfNoneMatch) > 0 || c.IfModifiedSince != nil || c.IfUnmodifiedSince != nil
}

func (c Conditions) HasWriteConditions() bool {
	return len(c.IfMatch) > 0 || len(c.IfNoneMatch) > 0
}

func ParseConditions(h http.Header, ifMatchKey, ifNoneMatchKey, ifModifiedSinceKey, ifUnmodifiedSinceKey string) Conditions {
	return Conditions{
		IfMatch:           parseETagList(h.Get(ifMatchKey)),
		IfNoneMatch:       parseETagList(h.Get(ifNoneMatchKey)),
		IfModifiedSince:   parseHTTPTime(h.Get(ifModifiedSinceKey)),
		IfUnmodifiedSince: parseHTTPTime(h.Get(ifUnmodifiedSinceKey)),
	}
}

func parseETagList(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		values = append(values, canonicalizeETag(part))
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

func parseHTTPTime(raw string) *time.Time {
	if raw == "" {
		return nil
	}
	t, err := time.Parse(http.TimeFormat, raw)
	if err != nil {
		return nil
	}
	return &t
}

func canonicalizeETag(etag string) string {
	return strings.Trim(etag, "\"")
}

func etagMatches(current string, candidates []string, exists bool) bool {
	current = canonicalizeETag(current)
	for _, candidate := range candidates {
		candidate = canonicalizeETag(candidate)
		if candidate == "*" {
			if exists {
				return true
			}
			continue
		}
		if current == candidate {
			return true
		}
	}
	return false
}

func ifModifiedSince(objTime, givenTime time.Time) bool {
	return objTime.After(givenTime.Add(1 * time.Second))
}

type ConditionFailure int

const (
	ConditionMatch ConditionFailure = iota
	ConditionNotFound
	ConditionPreconditionFailed
	ConditionNotModified
)

func EvaluateReadConditions(etag string, modTime time.Time, conds Conditions, modifiedSinceFailure, noneMatchFailure ConditionFailure) ConditionFailure {
	if len(conds.IfMatch) > 0 {
		if !etagMatches(etag, conds.IfMatch, true) {
			return ConditionPreconditionFailed
		}
	} else if conds.IfUnmodifiedSince != nil && !modTime.IsZero() && !modTime.Equal(time.Unix(0, 0)) {
		if ifModifiedSince(modTime, *conds.IfUnmodifiedSince) {
			return ConditionPreconditionFailed
		}
	}

	if len(conds.IfNoneMatch) > 0 {
		if etagMatches(etag, conds.IfNoneMatch, true) {
			return noneMatchFailure
		}
	} else if conds.IfModifiedSince != nil && !modTime.IsZero() && !modTime.Equal(time.Unix(0, 0)) {
		if !ifModifiedSince(modTime, *conds.IfModifiedSince) {
			return modifiedSinceFailure
		}
	}

	return ConditionMatch
}

func EvaluateWriteConditions(exists bool, etag string, conds Conditions) ConditionFailure {
	if len(conds.IfMatch) > 0 {
		if !exists {
			return ConditionNotFound
		}
		if !etagMatches(etag, conds.IfMatch, true) {
			return ConditionPreconditionFailed
		}
	}

	if len(conds.IfNoneMatch) > 0 && etagMatches(etag, conds.IfNoneMatch, exists) {
		return ConditionPreconditionFailed
	}

	return ConditionMatch
}

func RequiresCreateOnlyRename(conds Conditions) bool {
	for _, candidate := range conds.IfNoneMatch {
		if canonicalizeETag(candidate) == "*" {
			return true
		}
	}
	return false
}

func markConditionalFailure(ctx context.Context, status int, code, message string) {
	if state, ok := conditionalResponseStateFromContext(ctx); ok {
		state.failure = &conditionalFailure{
			StatusCode: status,
			Code:       code,
			Message:    message,
		}
	}
}

type bufferedResponseWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func newBufferedResponseWriter() *bufferedResponseWriter {
	return &bufferedResponseWriter{header: make(http.Header)}
}

func (w *bufferedResponseWriter) Header() http.Header {
	return w.header
}

func (w *bufferedResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
}

func (w *bufferedResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(p)
}

func (w *bufferedResponseWriter) Flush() {}

func (w *bufferedResponseWriter) StatusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *bufferedResponseWriter) FlushTo(dst http.ResponseWriter) {
	copyHeaders(dst.Header(), w.header)
	dst.WriteHeader(w.StatusCode())
	_, _ = dst.Write(w.body.Bytes())
}

type s3ErrorResponse struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	Resource  string   `xml:"Resource,omitempty"`
	RequestID string   `xml:"RequestId,omitempty"`
	HostID    string   `xml:"HostId,omitempty"`
}

func rewriteConditionalFailureResponse(dst http.ResponseWriter, r *http.Request, src *bufferedResponseWriter, state *conditionalResponseState) bool {
	if state == nil || state.failure == nil || src.StatusCode() == state.failure.StatusCode {
		return false
	}

	body, err := xml.Marshal(s3ErrorResponse{
		Code:      state.failure.Code,
		Message:   state.failure.Message,
		Resource:  r.URL.Path,
		RequestID: src.Header().Get(xhttp.AmzRequestID),
	})
	if err != nil {
		return false
	}
	payload := append([]byte(xml.Header), body...)

	headers := dst.Header()
	copyHeaders(headers, src.Header())
	headers.Set("Content-Type", "application/xml")
	headers.Set("Content-Length", strconv.Itoa(len(payload)))
	dst.WriteHeader(state.failure.StatusCode)
	_, _ = dst.Write(payload)
	return true
}

func copyHeaders(dst, src http.Header) {
	for k := range dst {
		delete(dst, k)
	}
	for k, values := range src {
		copied := append([]string(nil), values...)
		dst[k] = copied
	}
}
