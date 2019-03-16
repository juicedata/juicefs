package examples

/**
 * This sample demonstrates how to do bucket-related operations
 * (such as do bucket ACL/CORS/Lifecycle/Logging/Website/Location/Tagging)
 * on OBS using the OBS SDK for Go.
 */
import (
	"fmt"
	"obs"
	"strings"
	"time"
)

type BucketOperationsSample struct {
	bucketName string
	location   string
	obsClient  *obs.ObsClient
}

func newBucketOperationsSample(ak, sk, endpoint, bucketName, location string) *BucketOperationsSample {
	obsClient, err := obs.New(ak, sk, endpoint)
	if err != nil {
		panic(err)
	}
	return &BucketOperationsSample{obsClient: obsClient, bucketName: bucketName, location: location}
}

func (sample BucketOperationsSample) CreateBucket() {
	input := &obs.CreateBucketInput{}
	input.Bucket = sample.bucketName
	input.Location = sample.location
	_, err := sample.obsClient.CreateBucket(input)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Create bucket:%s successfully!\n", sample.bucketName)
	fmt.Println()
}

func (sample BucketOperationsSample) GetBucketLocation() {
	output, err := sample.obsClient.GetBucketLocation(sample.bucketName)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Bucket location - %s\n", output.Location)
	fmt.Println()
}

func (sample BucketOperationsSample) GetBucketStorageInfo() {
	output, err := sample.obsClient.GetBucketStorageInfo(sample.bucketName)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Bucket storageInfo - ObjectNumber:%d, Size:%d\n", output.ObjectNumber, output.Size)
	fmt.Println()
}

func (sample BucketOperationsSample) DoBucketQuotaOperation() {
	input := &obs.SetBucketQuotaInput{}
	input.Bucket = sample.bucketName
	// Set bucket quota to 1GB
	input.Quota = 1024 * 1024 * 1024
	_, err := sample.obsClient.SetBucketQuota(input)
	if err != nil {
		panic(err)
	}
	fmt.Println("Set bucket quota successfully!")
	fmt.Println()

	output, err := sample.obsClient.GetBucketQuota(sample.bucketName)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Bucket quota - %d", output.Quota)
	fmt.Println()
}

func (sample BucketOperationsSample) DoBucketVersioningOperation() {
	output, err := sample.obsClient.GetBucketVersioning(sample.bucketName)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Initial bucket versioning status - %s", output.Status)
	fmt.Println()

	// Enable bucket versioning
	input := &obs.SetBucketVersioningInput{}
	input.Bucket = sample.bucketName
	input.Status = obs.VersioningStatusEnabled
	sample.obsClient.SetBucketVersioning(input)

	output, err = sample.obsClient.GetBucketVersioning(sample.bucketName)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Current bucket versioning status - %s", output.Status)
	fmt.Println()

	// Suspend bucket versioning
	input = &obs.SetBucketVersioningInput{}
	input.Bucket = sample.bucketName
	input.Status = obs.VersioningStatusSuspended
	sample.obsClient.SetBucketVersioning(input)

	output, err = sample.obsClient.GetBucketVersioning(sample.bucketName)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Current bucket versioning status - %s", output.Status)
	fmt.Println()
}

func (sample BucketOperationsSample) DoBucketAclOperation() {
	input := &obs.SetBucketAclInput{}
	input.Bucket = sample.bucketName
	// Setting bucket ACL to public-read
	input.ACL = obs.AclPublicRead
	_, err := sample.obsClient.SetBucketAcl(input)
	if err != nil {
		panic(err)
	}
	fmt.Println("Set bucket acl successfully!")
	fmt.Println()

	output, err := sample.obsClient.GetBucketAcl(sample.bucketName)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Bucket owner - ownerId:%s, ownerName:%s\n", output.Owner.ID, output.Owner.DisplayName)
	for index, grant := range output.Grants {
		fmt.Printf("Grant[%d]\n", index)
		fmt.Printf("GranteeUri:%s, GranteeId:%s, GranteeName:%s\n", grant.Grantee.URI, grant.Grantee.ID, grant.Grantee.DisplayName)
		fmt.Printf("Permission:%s\n", grant.Permission)
	}
	fmt.Println()
}

func (sample BucketOperationsSample) DoBucketCorsOperation() {
	input := &obs.SetBucketCorsInput{}
	input.Bucket = sample.bucketName
	var corsRules [2]obs.CorsRule
	corsRule0 := obs.CorsRule{}
	corsRule0.ID = "rule1"
	corsRule0.AllowedOrigin = []string{"http://www.a.com", "http://www.b.com"}
	corsRule0.AllowedMethod = []string{"GET", "PUT", "POST", "HEAD"}
	corsRule0.AllowedHeader = []string{"header1", "header2"}
	corsRule0.MaxAgeSeconds = 100
	corsRule0.ExposeHeader = []string{"obs-1", "obs-2"}
	corsRules[0] = corsRule0
	corsRule1 := obs.CorsRule{}

	corsRule1.ID = "rule2"
	corsRule1.AllowedOrigin = []string{"http://www.c.com", "http://www.d.com"}
	corsRule1.AllowedMethod = []string{"GET", "PUT", "POST", "HEAD"}
	corsRule1.AllowedHeader = []string{"header3", "header4"}
	corsRule1.MaxAgeSeconds = 50
	corsRule1.ExposeHeader = []string{"obs-3", "obs-4"}
	corsRules[1] = corsRule1
	input.CorsRules = corsRules[:]
	// Setting bucket CORS
	_, err := sample.obsClient.SetBucketCors(input)
	if err != nil {
		panic(err)
	}
	fmt.Println("Set bucket cors successfully!")
	fmt.Println()

	output, _ := sample.obsClient.GetBucketCors(sample.bucketName)
	for index, corsRule := range output.CorsRules {
		fmt.Printf("CorsRule[%d]\n", index)
		fmt.Printf("ID:%s, AllowedOrigin:%s, AllowedMethod:%s, AllowedHeader:%s, MaxAgeSeconds:%d, ExposeHeader:%s\n",
			corsRule.ID, strings.Join(corsRule.AllowedOrigin, "|"), strings.Join(corsRule.AllowedMethod, "|"),
			strings.Join(corsRule.AllowedHeader, "|"), corsRule.MaxAgeSeconds, strings.Join(corsRule.ExposeHeader, "|"))
	}
	fmt.Println()

	_, err = sample.obsClient.DeleteBucketCors(sample.bucketName)
	if err != nil {
		panic(err)
	}
	fmt.Println("Delete bucket cors successfully!")
	fmt.Println()
}

func (sample BucketOperationsSample) GetBucketMetadata() {
	input := &obs.GetBucketMetadataInput{}
	input.Bucket = sample.bucketName
	output, err := sample.obsClient.GetBucketMetadata(input)
	if err != nil {
		panic(err)
	}
	fmt.Printf("StorageClass:%s\n", output.StorageClass)
	fmt.Printf("AllowOrigin:%s\n", output.AllowOrigin)
	fmt.Printf("AllowMethod:%s\n", output.AllowMethod)
	fmt.Printf("AllowHeader:%s\n", output.AllowHeader)
	fmt.Printf("ExposeHeader:%s\n", output.ExposeHeader)
	fmt.Printf("MaxAgeSeconds:%d\n", output.MaxAgeSeconds)
	fmt.Println()
}

func (sample BucketOperationsSample) DoBucketLifycleOperation() {
	input := &obs.SetBucketLifecycleConfigurationInput{}
	input.Bucket = sample.bucketName

	var lifecycleRules [2]obs.LifecycleRule
	lifecycleRule0 := obs.LifecycleRule{}
	lifecycleRule0.ID = "rule0"
	lifecycleRule0.Prefix = "prefix0"
	lifecycleRule0.Status = obs.RuleStatusEnabled

	var transitions [2]obs.Transition
	transitions[0] = obs.Transition{}
	transitions[0].Days = 30
	transitions[0].StorageClass = obs.StorageClassWarm

	transitions[1] = obs.Transition{}
	transitions[1].Days = 60
	transitions[1].StorageClass = obs.StorageClassCold
	lifecycleRule0.Transitions = transitions[:]

	lifecycleRule0.Expiration.Days = 100
	lifecycleRule0.NoncurrentVersionExpiration.NoncurrentDays = 20

	lifecycleRules[0] = lifecycleRule0

	lifecycleRule1 := obs.LifecycleRule{}
	lifecycleRule1.Status = obs.RuleStatusEnabled
	lifecycleRule1.ID = "rule1"
	lifecycleRule1.Prefix = "prefix1"
	lifecycleRule1.Expiration.Date = time.Now().Add(time.Duration(24) * time.Hour)

	var noncurrentTransitions [2]obs.NoncurrentVersionTransition
	noncurrentTransitions[0] = obs.NoncurrentVersionTransition{}
	noncurrentTransitions[0].NoncurrentDays = 30
	noncurrentTransitions[0].StorageClass = obs.StorageClassWarm

	noncurrentTransitions[1] = obs.NoncurrentVersionTransition{}
	noncurrentTransitions[1].NoncurrentDays = 60
	noncurrentTransitions[1].StorageClass = obs.StorageClassCold
	lifecycleRule1.NoncurrentVersionTransitions = noncurrentTransitions[:]
	lifecycleRules[1] = lifecycleRule1

	input.LifecycleRules = lifecycleRules[:]

	_, err := sample.obsClient.SetBucketLifecycleConfiguration(input)
	if err != nil {
		panic(err)
	}
	fmt.Println("Set bucket lifecycle successfully!")
	fmt.Println()

	output, err := sample.obsClient.GetBucketLifecycleConfiguration(sample.bucketName)
	if err != nil {
		panic(err)
	}
	fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
	for index, lifecycleRule := range output.LifecycleRules {
		fmt.Printf("LifecycleRule[%d]:\n", index)
		fmt.Printf("ID:%s, Prefix:%s, Status:%s\n", lifecycleRule.ID, lifecycleRule.Prefix, lifecycleRule.Status)

		date := ""
		for _, transition := range lifecycleRule.Transitions {
			if !transition.Date.IsZero() {
				date = transition.Date.String()
			}
			fmt.Printf("transition.StorageClass:%s, Transition.Date:%s, Transition.Days:%d\n", transition.StorageClass, date, transition.Days)
		}

		date = ""
		if !lifecycleRule.Expiration.Date.IsZero() {
			date = lifecycleRule.Expiration.Date.String()
		}
		fmt.Printf("Expiration.Date:%s, Expiration.Days:%d\n", lifecycleRule.Expiration.Date, lifecycleRule.Expiration.Days)

		for _, noncurrentVersionTransition := range lifecycleRule.NoncurrentVersionTransitions {
			fmt.Printf("noncurrentVersionTransition.StorageClass:%s, noncurrentVersionTransition.NoncurrentDays:%d\n",
				noncurrentVersionTransition.StorageClass, noncurrentVersionTransition.NoncurrentDays)
		}
		fmt.Printf("NoncurrentVersionExpiration.NoncurrentDays:%d\n", lifecycleRule.NoncurrentVersionExpiration.NoncurrentDays)
	}
	fmt.Println()

	_, err = sample.obsClient.DeleteBucketLifecycleConfiguration(sample.bucketName)
	if err != nil {
		panic(err)
	}
	fmt.Println("Delete bucket lifecycle successfully!")
	fmt.Println()
}

func (sample BucketOperationsSample) DoBucketLoggingOperation() {
	_, err := sample.obsClient.SetBucketAcl(&obs.SetBucketAclInput{Bucket: sample.bucketName, ACL: obs.AclLogDeliveryWrite})
	if err != nil {
		panic(err)
	}
	fmt.Println("Set bucket acl to log-delivery-write successfully!")
	fmt.Println()

	input := &obs.SetBucketLoggingConfigurationInput{}
	input.Bucket = sample.bucketName
	input.TargetBucket = sample.bucketName
	input.TargetPrefix = "prefix"
	var grants [2]obs.Grant
	grants[0].Grantee.Type = obs.GranteeGroup
	grants[0].Grantee.URI = obs.GroupAuthenticatedUsers
	grants[0].Permission = obs.PermissionRead

	grants[1].Grantee.Type = obs.GranteeGroup
	grants[1].Grantee.URI = obs.GroupAuthenticatedUsers
	grants[1].Permission = obs.PermissionWrite

	input.TargetGrants = grants[:]

	_, err = sample.obsClient.SetBucketLoggingConfiguration(input)
	if err != nil {
		panic(err)
	}
	fmt.Println("Set bucket logging successfully!")
	fmt.Println()

	output, _ := sample.obsClient.GetBucketLoggingConfiguration(sample.bucketName)
	fmt.Printf("TargetBucket:%s, TargetPrefix:%s\n", output.TargetBucket, output.TargetPrefix)
	for index, grant := range output.TargetGrants {
		fmt.Printf("Grant[%d]-Type:%s, ID:%s, URI:%s, Permission:%s\n", index, grant.Grantee.Type, grant.Grantee.ID, grant.Grantee.URI, grant.Permission)
	}
	fmt.Println()

	_, err = sample.obsClient.SetBucketLoggingConfiguration(&obs.SetBucketLoggingConfigurationInput{Bucket: sample.bucketName})
	if err != nil {
		panic(err)
	}
	fmt.Println("Delete bucket logging successfully!")
	fmt.Println()
}

func (sample BucketOperationsSample) DoBucketWebsiteOperation() {

	input := &obs.SetBucketWebsiteConfigurationInput{}
	input.Bucket = sample.bucketName
	input.IndexDocument.Suffix = "suffix"
	input.ErrorDocument.Key = "key"

	var routingRules [2]obs.RoutingRule
	routingRule0 := obs.RoutingRule{}

	routingRule0.Redirect.HostName = "www.a.com"
	routingRule0.Redirect.Protocol = obs.ProtocolHttp
	routingRule0.Redirect.ReplaceKeyPrefixWith = "prefix"
	routingRule0.Redirect.HttpRedirectCode = "304"
	routingRules[0] = routingRule0

	routingRule1 := obs.RoutingRule{}

	routingRule1.Redirect.HostName = "www.b.com"
	routingRule1.Redirect.Protocol = obs.ProtocolHttps
	routingRule1.Redirect.ReplaceKeyWith = "replaceKey"
	routingRule1.Redirect.HttpRedirectCode = "304"

	routingRule1.Condition.HttpErrorCodeReturnedEquals = "404"
	routingRule1.Condition.KeyPrefixEquals = "prefix"

	routingRules[1] = routingRule1

	input.RoutingRules = routingRules[:]
	_, err := sample.obsClient.SetBucketWebsiteConfiguration(input)
	if err != nil {
		panic(err)
	}
	fmt.Println("Set bucket website successfully!")
	fmt.Println()

	output, err := sample.obsClient.GetBucketWebsiteConfiguration(sample.bucketName)
	if err != nil {
		panic(err)
	}
	fmt.Printf("RedirectAllRequestsTo.HostName:%s,RedirectAllRequestsTo.Protocol:%s\n", output.RedirectAllRequestsTo.HostName, output.RedirectAllRequestsTo.Protocol)
	fmt.Printf("Suffix:%s\n", output.IndexDocument.Suffix)
	fmt.Printf("Key:%s\n", output.ErrorDocument.Key)
	for index, routingRule := range output.RoutingRules {
		fmt.Printf("Condition[%d]-KeyPrefixEquals:%s, HttpErrorCodeReturnedEquals:%s\n", index, routingRule.Condition.KeyPrefixEquals, routingRule.Condition.HttpErrorCodeReturnedEquals)
		fmt.Printf("Redirect[%d]-Protocol:%s, HostName:%s, ReplaceKeyPrefixWith:%s, HttpRedirectCode:%s\n",
			index, routingRule.Redirect.Protocol, routingRule.Redirect.HostName, routingRule.Redirect.ReplaceKeyPrefixWith, routingRule.Redirect.HttpRedirectCode)
	}
	fmt.Println()

	_, err = sample.obsClient.DeleteBucketWebsiteConfiguration(sample.bucketName)
	if err != nil {
		panic(err)
	}
	fmt.Println("Delete bucket website successfully!")
	fmt.Println()
}

func (sample BucketOperationsSample) DoBucketTaggingOperation() {
	input := &obs.SetBucketTaggingInput{}
	input.Bucket = sample.bucketName
	var tags [2]obs.Tag
	tags[0] = obs.Tag{Key: "key0", Value: "value0"}
	tags[1] = obs.Tag{Key: "key1", Value: "value1"}
	input.Tags = tags[:]
	_, err := sample.obsClient.SetBucketTagging(input)
	if err != nil {
		panic(err)
	}
	fmt.Println("Set bucket tagging successfully!")

	output, err := sample.obsClient.GetBucketTagging(sample.bucketName)
	if err != nil {
		panic(err)
	}
	for index, tag := range output.Tags {
		fmt.Printf("Tag[%d]-Key:%s, Value:%s\n", index, tag.Key, tag.Value)
	}

	_, err = sample.obsClient.DeleteBucketTagging(sample.bucketName)
	if err != nil {
		panic(err)
	}
	fmt.Println("Delete bucket tagging successfully!")
}

func (sample BucketOperationsSample) DeleteBucket() {
	_, err := sample.obsClient.DeleteBucketWebsiteConfiguration(sample.bucketName)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Delete bucket %s successfully!\n", sample.bucketName)
	fmt.Println()
}

func RunBucketOperationsSample() {
	const (
		endpoint   = "https://your-endpoint"
		ak         = "*** Provide your Access Key ***"
		sk         = "*** Provide your Secret Key ***"
		bucketName = "bucket-test"
		location   = "yourbucketlocation"
	)

	sample := newBucketOperationsSample(ak, sk, endpoint, bucketName, location)

	// Put bucket operation
	sample.CreateBucket()

	// Get bucket location operation
	sample.GetBucketLocation()

	// Get bucket storageInfo operation
	sample.GetBucketStorageInfo()

	// Put/Get bucket quota operations
	sample.DoBucketQuotaOperation()

	// Put/Get bucket versioning operations
	sample.DoBucketVersioningOperation()

	// Put/Get bucket acl operations
	sample.DoBucketAclOperation()

	// Put/Get/Delete bucket cors operations
	sample.DoBucketCorsOperation()
	// Get bucket metadata operation
	sample.GetBucketMetadata()

	// Put/Get/Delete bucket lifecycle operations
	sample.DoBucketLifycleOperation()

	// Put/Get/Delete bucket logging operations
	sample.DoBucketLoggingOperation()

	// Put/Get/Delete bucket website operations
	sample.DoBucketWebsiteOperation()

	// Put/Get/Delete bucket tagging operations
	sample.DoBucketTaggingOperation()

	// Delete bucket operation
	sample.DeleteBucket()
}
