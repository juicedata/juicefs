package object

import (
	"context"
	"errors"
	"net"
	"net/http"
	"reflect"
	"testing"
	"time"
)

func TestObjectStorageInterfaceMethodsUseContext(t *testing.T) {
	typ := reflect.TypeOf((*ObjectStorage)(nil)).Elem()

	methodsRequiringCtx := map[string]bool{
		"Create":                true,
		"Get":                   true,
		"Put":                   true,
		"Copy":                  true,
		"Delete":                true,
		"Head":                  true,
		"List":                  true,
		"ListAll":               true,
		"CreateMultipartUpload": true,
		"UploadPart":            true,
		"UploadPartCopy":        true,
		"AbortUpload":           true,
		"CompleteUpload":        true,
		"ListUploads":           true,
		"Restore":               true,
	}

	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
	for i := 0; i < typ.NumMethod(); i++ {
		m := typ.Method(i)
		if !methodsRequiringCtx[m.Name] {
			continue
		}
		if m.Type.NumIn() < 1 {
			t.Fatalf("method %s has no context parameter", m.Name)
		}
		if m.Type.In(0) != ctxType {
			t.Fatalf("method %s should take context.Context as first argument, got %s", m.Name, m.Type.In(0))
		}
	}
}

func TestDialParallel_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	dialer := &net.Dialer{Timeout: 2 * time.Second}
	_, err := dialParallel(ctx, dialer, "tcp", []net.IP{net.ParseIP("127.0.0.1")}, nil, "65535")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestRestfulStorageGet_ContextCanceled(t *testing.T) {
	storage := &RestfulStorage{
		endpoint: "http://127.0.0.1:1",
		signer:   func(*http.Request, string, string, string) {},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := storage.Get(ctx, "object", 0, -1)
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.Canceled or DeadlineExceeded, got %v", err)
	}
}

func TestRestfulStoragePut_ContextCanceled(t *testing.T) {
	storage := &RestfulStorage{
		endpoint: "http://127.0.0.1:1",
		signer:   func(*http.Request, string, string, string) {},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := storage.Put(ctx, "object", http.NoBody)
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.Canceled or DeadlineExceeded, got %v", err)
	}
}
