//Package storagegateway provides gucumber integration tests suppport.
package storagegateway

import (
	"github.com/ks3sdklib/aws-sdk-go/internal/features/shared"
	"github.com/ks3sdklib/aws-sdk-go/service/storagegateway"
	. "github.com/lsegal/gucumber"
)

var _ = shared.Imported

func init() {
	Before("@storagegateway", func() {
		World["client"] = storagegateway.New(nil)
	})
}
