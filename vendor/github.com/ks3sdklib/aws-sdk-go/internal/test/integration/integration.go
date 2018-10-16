// Package integration performs initialization and validation for integration
// tests.
package integration

import (
	"crypto/rand"
	"fmt"
	"io"
	"os"

	"github.com/ks3sdklib/aws-sdk-go/aws"
)

// Imported is a marker to ensure that this package's init() function gets
// executed.
//
// To use this package, import it and add:
//
// 	 var _ = integration.Imported
const Imported = true

func init() {
	if os.Getenv("DEBUG") != "" {
		aws.DefaultConfig.LogLevel = 1
	}
	if os.Getenv("DEBUG_BODY") != "" {
		aws.DefaultConfig.LogLevel = 1
		aws.DefaultConfig.LogHTTPBody = true
	}

	if aws.DefaultConfig.Region == "" {
		panic("AWS_REGION must be configured to run integration tests")
	}
}

// UniqueID returns a unique UUID-like identifier for use in generating
// resources for integration tests.
func UniqueID() string {
	uuid := make([]byte, 16)
	io.ReadFull(rand.Reader, uuid)
	return fmt.Sprintf("%x", uuid)
}
