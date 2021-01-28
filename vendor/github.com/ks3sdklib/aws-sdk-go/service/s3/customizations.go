package s3

import "github.com/ks3sdklib/aws-sdk-go/aws"

func init() {
	initService = func(s *aws.Service) {
		// Support building custom host-style bucket endpoints
		s.Handlers.Build.PushFront(updateHostWithBucket)

		// Require SSL when using SSE keys
		s.Handlers.Validate.PushBack(validateSSERequiresSSL)
		s.Handlers.Build.PushBack(computeSSEKeys)

		// S3 uses custom error unmarshaling logic
		s.Handlers.UnmarshalError.Clear()
		s.Handlers.UnmarshalError.PushBack(unmarshalError)
	}

	initRequest = func(r *aws.Request) {
		switch r.Operation {
		case opPutBucketCORS, opPutBucketLifecycle, opPutBucketPolicy, opPutBucketTagging, opDeleteObjects:
			// These S3 operations require Content-MD5 to be set
			r.Handlers.Build.PushBack(contentMD5)
		case opGetBucketLocation:
			// GetBucketLocation has custom parsing logic
			r.Handlers.Unmarshal.PushFront(buildGetBucketLocation)
		case opCreateBucket:
			// Auto-populate LocationConstraint with current region
			r.Handlers.Validate.PushFront(populateLocationConstraint)
		}
	}
}
