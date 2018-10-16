//Package elasticache provides gucumber integration tests suppport.
package elasticache

import (
	"github.com/ks3sdklib/aws-sdk-go/internal/features/shared"
	"github.com/ks3sdklib/aws-sdk-go/service/elasticache"
	. "github.com/lsegal/gucumber"
)

var _ = shared.Imported

func init() {
	Before("@elasticache", func() {
		World["client"] = elasticache.New(nil)
	})
}
