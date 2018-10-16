//Package ssm provides gucumber integration tests suppport.
package ssm

import (
	"github.com/ks3sdklib/aws-sdk-go/internal/features/shared"
	"github.com/ks3sdklib/aws-sdk-go/service/ssm"
	. "github.com/lsegal/gucumber"
)

var _ = shared.Imported

func init() {
	Before("@ssm", func() {
		World["client"] = ssm.New(nil)
	})
}
