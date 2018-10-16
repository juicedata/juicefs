//Package emr provides gucumber integration tests suppport.
package emr

import (
	"github.com/ks3sdklib/aws-sdk-go/internal/features/shared"
	"github.com/ks3sdklib/aws-sdk-go/service/emr"
	. "github.com/lsegal/gucumber"
)

var _ = shared.Imported

func init() {
	Before("@emr", func() {
		World["client"] = emr.New(nil)
	})
}
