//Package sns provides gucumber integration tests suppport.
package sns

import (
	"github.com/ks3sdklib/aws-sdk-go/internal/features/shared"
	"github.com/ks3sdklib/aws-sdk-go/service/sns"
	. "github.com/lsegal/gucumber"
)

var _ = shared.Imported

func init() {
	Before("@sns", func() {
		World["client"] = sns.New(nil)
	})
}
