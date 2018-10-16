package api

var svcCustomizations = map[string]func(*API){
	"s3":         s3Customizations,
	"cloudfront": cloudfrontCustomizations,
}

// customizationPasses Executes customization logic for the API by package name.
func (a *API) customizationPasses() {
	if fn := svcCustomizations[a.PackageName()]; fn != nil {
		fn(a)
	}
}

// s3Customizations customizes the API generation to replace values specific to S3.
func s3Customizations(a *API) {
	// Remove ContentMD5 members
	for _, s := range a.Shapes {
		if _, ok := s.MemberRefs["ContentMD5"]; ok {
			delete(s.MemberRefs, "ContentMD5")
		}
	}

	// Rename "Rule" to "LifecycleRule"
	if s, ok := a.Shapes["Rule"]; ok {
		s.Rename("LifecycleRule")
	}
}

// cloudfrontCustomizations customized the API generation to replace values
// specific to CloudFront.
func cloudfrontCustomizations(a *API) {
	// MaxItems members should always be integers
	for _, s := range a.Shapes {
		if ref, ok := s.MemberRefs["MaxItems"]; ok {
			ref.ShapeName = "Integer"
			ref.Shape = a.Shapes["Integer"]
		}
	}
}
