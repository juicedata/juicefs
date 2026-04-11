//go:build !nogateway
// +build !nogateway

package cmd

import (
	"net/http"
	"reflect"
	"sync"
	"testing"

	"github.com/gorilla/mux"
	jfsgateway "github.com/juicedata/juicefs/pkg/gateway"
)

func TestRegisterGatewayConditionalMiddleware(t *testing.T) {
	savedHandlers := append([]mux.MiddlewareFunc(nil), minioGlobalHandlers...)
	wantFirst := reflect.ValueOf(mux.MiddlewareFunc(jfsgateway.ConditionalRequestMiddleware)).Pointer()
	wasRegistered := len(savedHandlers) > 0 && reflect.ValueOf(savedHandlers[0]).Pointer() == wantFirst
	defer func() {
		minioGlobalHandlers = savedHandlers
		registerGatewayConditionalMiddlewareOnce = sync.Once{}
		if wasRegistered {
			registerGatewayConditionalMiddlewareOnce.Do(func() {})
		}
	}()

	sentinel := mux.MiddlewareFunc(func(next http.Handler) http.Handler { return next })
	minioGlobalHandlers = []mux.MiddlewareFunc{sentinel}
	registerGatewayConditionalMiddlewareOnce = sync.Once{}

	registerGatewayConditionalMiddleware()
	registerGatewayConditionalMiddleware()

	if len(minioGlobalHandlers) != 2 {
		t.Fatalf("expected middleware to be registered once, got %d handlers", len(minioGlobalHandlers))
	}

	gotFirst := reflect.ValueOf(minioGlobalHandlers[0]).Pointer()
	if gotFirst != wantFirst {
		t.Fatalf("expected conditional middleware to be prepended, got %v want %v", gotFirst, wantFirst)
	}

	gotSecond := reflect.ValueOf(minioGlobalHandlers[1]).Pointer()
	wantSecond := reflect.ValueOf(sentinel).Pointer()
	if gotSecond != wantSecond {
		t.Fatalf("expected existing MinIO handlers to remain after the conditional middleware")
	}
}
