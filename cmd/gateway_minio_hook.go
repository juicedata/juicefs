//go:build !nogateway
// +build !nogateway

package cmd

import (
	"sync"

	"github.com/gorilla/mux"
	jfsgateway "github.com/juicedata/juicefs/pkg/gateway"

	_ "unsafe"
)

//go:linkname minioGlobalHandlers github.com/minio/minio/cmd.globalHandlers
var minioGlobalHandlers []mux.MiddlewareFunc

var registerGatewayConditionalMiddlewareOnce sync.Once

func registerGatewayConditionalMiddleware() {
	registerGatewayConditionalMiddlewareOnce.Do(func() {
		// The juicedata/minio fork currently wires request middleware through the
		// unexported cmd.globalHandlers slice, which configureServerHandler()
		// consumes before requests reach the object layer. There is no public hook
		// for JFS to register a gateway-specific middleware at that point, so keep
		// this linkname in sync with upstream cmd/routers.go when bumping MinIO.
		minioGlobalHandlers = append(
			[]mux.MiddlewareFunc{mux.MiddlewareFunc(jfsgateway.ConditionalRequestMiddleware)},
			minioGlobalHandlers...,
		)
	})
}
