package object

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	storage := &RestfulStorage{
		endpoint: server.URL,
		signer:   func(*http.Request, string, string, string) {},
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	_, err := storage.Get(ctx, "slow-object", 0, -1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestRestfulStoragePut_ContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	storage := &RestfulStorage{
		endpoint: server.URL,
		signer:   func(*http.Request, string, string, string) {},
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	err := storage.Put(ctx, "slow-object", http.NoBody)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
