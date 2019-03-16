package main

import (
	"examples"
	"fmt"
	"obs"
	"os"
	"strings"
	"time"
)

const (
	endpoint   = "https://your-endpoint"
	ak         = "*** Provide your Access Key ***"
	sk         = "*** Provide your Secret Key ***"
	bucketName = "bucket-test"
	objectKey  = "object-test"
	location   = "yourbucketlocation"
)

var obsClient *obs.ObsClient

func getObsClient() *obs.ObsClient {
	var err error
	if obsClient == nil {
		obsClient, err = obs.New(ak, sk, endpoint)
		if err != nil {
			panic(err)
		}
	}
	return obsClient
}

func createBucket() {
	input := &obs.CreateBucketInput{}
	input.Bucket = bucketName
	input.StorageClass = obs.StorageClassWarm
	input.ACL = obs.AclPublicRead
	output, err := getObsClient().CreateBucket(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func listBuckets() {
	input := &obs.ListBucketsInput{}
	input.QueryLocation = true
	output, err := getObsClient().ListBuckets(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		fmt.Printf("Owner.DisplayName:%s, Owner.ID:%s\n", output.Owner.DisplayName, output.Owner.ID)
		for index, val := range output.Buckets {
			fmt.Printf("Bucket[%d]-Name:%s,CreationDate:%s,Location:%s\n", index, val.Name, val.CreationDate, val.Location)
		}
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func setBucketStoragePolicy() {
	input := &obs.SetBucketStoragePolicyInput{}
	input.Bucket = bucketName
	input.StorageClass = obs.StorageClassCold
	output, err := getObsClient().SetBucketStoragePolicy(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func getBucketStoragePolicy() {
	output, err := getObsClient().GetBucketStoragePolicy(bucketName)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		fmt.Printf("StorageClass:%s\n", output.StorageClass)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func deleteBucket() {
	output, err := getObsClient().DeleteBucket(bucketName)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func listObjects() {
	input := &obs.ListObjectsInput{}
	input.Bucket = bucketName
	input.MaxKeys = 10
	//	input.Prefix = "src/"
	output, err := getObsClient().ListObjects(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		for index, val := range output.Contents {
			fmt.Printf("Content[%d]-OwnerId:%s, OwnerName:%s, ETag:%s, Key:%s, LastModified:%s, Size:%d, StorageClass:%s\n",
				index, val.Owner.ID, val.Owner.DisplayName, val.ETag, val.Key, val.LastModified, val.Size, val.StorageClass)
		}
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func listVersions() {
	input := &obs.ListVersionsInput{}
	input.Bucket = bucketName
	input.MaxKeys = 10
	//	input.Prefix = "src/"
	output, err := getObsClient().ListVersions(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		for index, val := range output.Versions {
			fmt.Printf("Version[%d]-OwnerId:%s, OwnerName:%s, ETag:%s, Key:%s, VersionId:%s, LastModified:%s, Size:%d, StorageClass:%s\n",
				index, val.Owner.ID, val.Owner.DisplayName, val.ETag, val.Key, val.VersionId, val.LastModified, val.Size, val.StorageClass)
		}
		for index, val := range output.DeleteMarkers {
			fmt.Printf("DeleteMarker[%d]-OwnerId:%s, OwnerName:%s, Key:%s, VersionId:%s, LastModified:%s, StorageClass:%s\n",
				index, val.Owner.ID, val.Owner.DisplayName, val.Key, val.VersionId, val.LastModified, val.StorageClass)
		}
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func setBucketQuota() {
	input := &obs.SetBucketQuotaInput{}
	input.Bucket = bucketName
	input.Quota = 1024 * 1024 * 1024
	output, err := getObsClient().SetBucketQuota(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func getBucketQuota() {
	output, err := getObsClient().GetBucketQuota(bucketName)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		fmt.Printf("Quota:%d\n", output.Quota)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func getBucketStorageInfo() {
	output, err := getObsClient().GetBucketStorageInfo(bucketName)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		fmt.Printf("Size:%d, ObjectNumber:%d\n", output.Size, output.ObjectNumber)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func getBucketLocation() {
	output, err := getObsClient().GetBucketLocation(bucketName)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		fmt.Printf("Location:%s\n", output.Location)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func setBucketAcl() {
	input := &obs.SetBucketAclInput{}
	input.Bucket = bucketName
	//		input.ACL = obs.AclPublicRead
	input.Owner.ID = "ownerid"
	var grants [3]obs.Grant
	grants[0].Grantee.Type = obs.GranteeGroup
	grants[0].Grantee.URI = obs.GroupAuthenticatedUsers
	grants[0].Permission = obs.PermissionRead

	grants[1].Grantee.Type = obs.GranteeUser
	grants[1].Grantee.ID = "userid"
	grants[1].Permission = obs.PermissionWrite

	grants[2].Grantee.Type = obs.GranteeUser
	grants[2].Grantee.ID = "userid"
	grants[2].Grantee.DisplayName = "username"
	grants[2].Permission = obs.PermissionRead
	input.Grants = grants[0:3]
	output, err := getObsClient().SetBucketAcl(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func getBucketAcl() {
	output, err := getObsClient().GetBucketAcl(bucketName)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		fmt.Printf("Owner.DisplayName:%s, Owner.ID:%s\n", output.Owner.DisplayName, output.Owner.ID)
		for index, grant := range output.Grants {
			fmt.Printf("Grant[%d]-Type:%s, ID:%s, URI:%s, Permission:%s\n", index, grant.Grantee.Type, grant.Grantee.ID, grant.Grantee.URI, grant.Permission)
		}
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func setBucketPolicy() {
	input := &obs.SetBucketPolicyInput{}
	input.Bucket = bucketName
	input.Policy = "your policy"
	output, err := getObsClient().SetBucketPolicy(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func getBucketPolicy() {
	output, err := getObsClient().GetBucketPolicy(bucketName)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		fmt.Printf("Policy:%s\n", output.Policy)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func deleteBucketPolicy() {
	output, err := getObsClient().DeleteBucketPolicy(bucketName)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func setBucketCors() {
	input := &obs.SetBucketCorsInput{}
	input.Bucket = bucketName

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
	output, err := getObsClient().SetBucketCors(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func getBucketCors() {
	output, err := getObsClient().GetBucketCors(bucketName)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		for _, corsRule := range output.CorsRules {
			fmt.Printf("ID:%s, AllowedOrigin:%s, AllowedMethod:%s, AllowedHeader:%s, MaxAgeSeconds:%d, ExposeHeader:%s\n",
				corsRule.ID, strings.Join(corsRule.AllowedOrigin, "|"), strings.Join(corsRule.AllowedMethod, "|"),
				strings.Join(corsRule.AllowedHeader, "|"), corsRule.MaxAgeSeconds, strings.Join(corsRule.ExposeHeader, "|"))
		}
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func deleteBucketCors() {
	output, err := getObsClient().DeleteBucketCors(bucketName)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func setBucketVersioning() {
	input := &obs.SetBucketVersioningInput{}
	input.Bucket = bucketName
	input.Status = obs.VersioningStatusEnabled
	output, err := getObsClient().SetBucketVersioning(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func getBucketVersioning() {
	output, err := getObsClient().GetBucketVersioning(bucketName)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		fmt.Printf("Status:%s\n", output.Status)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func headBucket() {
	output, err := getObsClient().HeadBucket(bucketName)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
		} else {
			fmt.Println(err)
		}
	}
}

func getBucketMetadata() {
	input := &obs.GetBucketMetadataInput{}
	input.Bucket = bucketName
	output, err := getObsClient().GetBucketMetadata(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		fmt.Printf("StorageClass:%s\n", output.StorageClass)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Printf("StatusCode:%d\n", obsError.StatusCode)
		} else {
			fmt.Println(err)
		}
	}
}

func setBucketLoggingConfiguration() {
	input := &obs.SetBucketLoggingConfigurationInput{}
	input.Bucket = bucketName
	input.TargetBucket = "target-bucket"
	input.TargetPrefix = "prefix"
	var grants [3]obs.Grant
	grants[0].Grantee.Type = obs.GranteeGroup
	grants[0].Grantee.URI = obs.GroupAuthenticatedUsers
	grants[0].Permission = obs.PermissionRead

	grants[1].Grantee.Type = obs.GranteeUser
	grants[1].Grantee.ID = "userid"
	grants[1].Permission = obs.PermissionWrite

	grants[2].Grantee.Type = obs.GranteeUser
	grants[2].Grantee.ID = "userid"
	grants[2].Grantee.DisplayName = "username"
	grants[2].Permission = obs.PermissionRead
	input.TargetGrants = grants[0:3]
	output, err := getObsClient().SetBucketLoggingConfiguration(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func getBucketLoggingConfiguration() {
	output, err := getObsClient().GetBucketLoggingConfiguration(bucketName)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		fmt.Printf("TargetBucket:%s, TargetPrefix:%s\n", output.TargetBucket, output.TargetPrefix)
		for index, grant := range output.TargetGrants {
			fmt.Printf("Grant[%d]-Type:%s, ID:%s, URI:%s, Permission:%s\n", index, grant.Grantee.Type, grant.Grantee.ID, grant.Grantee.URI, grant.Permission)
		}
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func setBucketWebsiteConfiguration() {
	input := &obs.SetBucketWebsiteConfigurationInput{}
	input.Bucket = bucketName
	//	input.RedirectAllRequestsTo.HostName = "www.a.com"
	//	input.RedirectAllRequestsTo.Protocol = obs.ProtocolHttp
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
	output, err := getObsClient().SetBucketWebsiteConfiguration(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func getBucketWebsiteConfiguration() {
	output, err := getObsClient().GetBucketWebsiteConfiguration(bucketName)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		fmt.Printf("RedirectAllRequestsTo.HostName:%s,RedirectAllRequestsTo.Protocol:%s\n", output.RedirectAllRequestsTo.HostName, output.RedirectAllRequestsTo.Protocol)
		fmt.Printf("Suffix:%s\n", output.IndexDocument.Suffix)
		fmt.Printf("Key:%s\n", output.ErrorDocument.Key)
		for index, routingRule := range output.RoutingRules {
			fmt.Printf("Condition[%d]-KeyPrefixEquals:%s, HttpErrorCodeReturnedEquals:%s\n", index, routingRule.Condition.KeyPrefixEquals, routingRule.Condition.HttpErrorCodeReturnedEquals)
			fmt.Printf("Redirect[%d]-Protocol:%s, HostName:%s, ReplaceKeyPrefixWith:%s, HttpRedirectCode:%s\n",
				index, routingRule.Redirect.Protocol, routingRule.Redirect.HostName, routingRule.Redirect.ReplaceKeyPrefixWith, routingRule.Redirect.HttpRedirectCode)
		}
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func deleteBucketWebsiteConfiguration() {
	output, err := getObsClient().DeleteBucketWebsiteConfiguration(bucketName)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func setBucketLifecycleConfiguration() {
	input := &obs.SetBucketLifecycleConfigurationInput{}
	input.Bucket = bucketName

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

	output, err := getObsClient().SetBucketLifecycleConfiguration(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func getBucketLifecycleConfiguration() {
	output, err := getObsClient().GetBucketLifecycleConfiguration(bucketName)
	if err == nil {
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
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func deleteBucketLifecycleConfiguration() {
	output, err := getObsClient().DeleteBucketLifecycleConfiguration(bucketName)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func setBucketTagging() {
	input := &obs.SetBucketTaggingInput{}
	input.Bucket = bucketName

	var tags [2]obs.Tag
	tags[0] = obs.Tag{Key: "key0", Value: "value0"}
	tags[1] = obs.Tag{Key: "key1", Value: "value1"}
	input.Tags = tags[:]
	output, err := getObsClient().SetBucketTagging(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func getBucketTagging() {
	output, err := getObsClient().GetBucketTagging(bucketName)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		for index, tag := range output.Tags {
			fmt.Printf("Tag[%d]-Key:%s, Value:%s\n", index, tag.Key, tag.Value)
		}
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func deleteBucketTagging() {
	output, err := getObsClient().DeleteBucketTagging(bucketName)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func setBucketNotification() {
	input := &obs.SetBucketNotificationInput{}
	input.Bucket = bucketName
	var topicConfigurations [1]obs.TopicConfiguration
	topicConfigurations[0] = obs.TopicConfiguration{}
	topicConfigurations[0].ID = "001"
	topicConfigurations[0].Topic = "your topic"
	topicConfigurations[0].Events = []obs.EventType{obs.ObjectCreatedAll}

	var filterRules [2]obs.FilterRule

	filterRules[0] = obs.FilterRule{Name: "prefix", Value: "smn"}
	filterRules[1] = obs.FilterRule{Name: "suffix", Value: ".jpg"}
	topicConfigurations[0].FilterRules = filterRules[:]

	input.TopicConfigurations = topicConfigurations[:]
	output, err := getObsClient().SetBucketNotification(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func getBucketNotification() {
	output, err := getObsClient().GetBucketNotification(bucketName)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		for index, topicConfiguration := range output.TopicConfigurations {
			fmt.Printf("TopicConfiguration[%d]\n", index)
			fmt.Printf("ID:%s, Topic:%s, Events:%v\n", topicConfiguration.ID, topicConfiguration.Topic, topicConfiguration.Events)
		}
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func listMultipartUploads() {
	input := &obs.ListMultipartUploadsInput{}
	input.Bucket = bucketName
	input.MaxUploads = 10
	output, err := getObsClient().ListMultipartUploads(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		for index, upload := range output.Uploads {
			fmt.Printf("Upload[%d]-OwnerId:%s, OwnerName:%s, UploadId:%s, Key:%s, Initiated:%s,StorageClass:%s\n",
				index, upload.Owner.ID, upload.Owner.DisplayName, upload.UploadId, upload.Key, upload.Initiated, upload.StorageClass)
		}
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func deleteObject() {
	input := &obs.DeleteObjectInput{}
	input.Bucket = bucketName
	input.Key = objectKey
	output, err := getObsClient().DeleteObject(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		fmt.Printf("VersionId:%s, DeleteMarker:%v\n", output.VersionId, output.DeleteMarker)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func deleteObjects() {
	input := &obs.DeleteObjectsInput{}
	input.Bucket = bucketName
	var objects [3]obs.ObjectToDelete
	objects[0] = obs.ObjectToDelete{Key: "key1"}
	objects[1] = obs.ObjectToDelete{Key: "key2"}
	objects[2] = obs.ObjectToDelete{Key: "key3"}

	input.Objects = objects[:]
	output, err := getObsClient().DeleteObjects(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		for index, deleted := range output.Deleteds {
			fmt.Printf("Deleted[%d]-Key:%s, VersionId:%s\n", index, deleted.Key, deleted.VersionId)
		}
		for index, err := range output.Errors {
			fmt.Printf("Error[%d]-Key:%s, Code:%s\n", index, err.Key, err.Code)
		}
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func setObjectAcl() {
	input := &obs.SetObjectAclInput{}
	input.Bucket = bucketName
	input.Key = objectKey
	// input.ACL = obs.AclPublicRead
	input.Owner.ID = "ownerid"
	var grants [3]obs.Grant
	grants[0].Grantee.Type = obs.GranteeGroup
	grants[0].Grantee.URI = obs.GroupAuthenticatedUsers
	grants[0].Permission = obs.PermissionRead

	grants[1].Grantee.Type = obs.GranteeUser
	grants[1].Grantee.ID = "userid"
	grants[1].Permission = obs.PermissionWrite

	grants[2].Grantee.Type = obs.GranteeUser
	grants[2].Grantee.ID = "userid"
	grants[2].Grantee.DisplayName = "username"
	grants[2].Permission = obs.PermissionRead
	input.Grants = grants[0:3]
	output, err := getObsClient().SetObjectAcl(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func getObjectAcl() {
	input := &obs.GetObjectAclInput{}
	input.Bucket = bucketName
	input.Key = objectKey
	output, err := getObsClient().GetObjectAcl(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		fmt.Printf("Owner.DisplayName:%s, Owner.ID:%s\n", output.Owner.DisplayName, output.Owner.ID)
		for index, grant := range output.Grants {
			fmt.Printf("Grant[%d]-Type:%s, ID:%s, URI:%s, Permission:%s\n", index, grant.Grantee.Type, grant.Grantee.ID, grant.Grantee.URI, grant.Permission)
		}
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func restoreObject() {
	input := &obs.RestoreObjectInput{}
	input.Bucket = bucketName
	input.Key = objectKey
	input.Days = 1
	input.Tier = obs.RestoreTierExpedited
	output, err := getObsClient().RestoreObject(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func getObjectMetadata() {
	input := &obs.GetObjectMetadataInput{}
	input.Bucket = bucketName
	input.Key = objectKey
	output, err := getObsClient().GetObjectMetadata(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		fmt.Printf("StorageClass:%s, ETag:%s, ContentType:%s, ContentLength:%d, LastModified:%s\n",
			output.StorageClass, output.ETag, output.ContentType, output.ContentLength, output.LastModified)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Printf("StatusCode:%d\n", obsError.StatusCode)
		} else {
			fmt.Println(err)
		}
	}
}

func copyObject() {
	input := &obs.CopyObjectInput{}
	input.Bucket = bucketName
	input.Key = objectKey
	input.CopySourceBucket = bucketName
	input.CopySourceKey = objectKey + "-back"
	input.Metadata = map[string]string{"meta": "value"}
	input.MetadataDirective = obs.ReplaceMetadata

	output, err := getObsClient().CopyObject(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		fmt.Printf("ETag:%s, LastModified:%s\n",
			output.ETag, output.LastModified)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func initiateMultipartUpload() {
	input := &obs.InitiateMultipartUploadInput{}
	input.Bucket = bucketName
	input.Key = objectKey
	input.Metadata = map[string]string{"meta": "value"}
	output, err := getObsClient().InitiateMultipartUpload(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		fmt.Printf("Bucket:%s, Key:%s, UploadId:%s\n", output.Bucket, output.Key, output.UploadId)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func abortMultipartUpload() {
	input := &obs.ListMultipartUploadsInput{}
	input.Bucket = bucketName
	output, err := getObsClient().ListMultipartUploads(input)
	if err == nil {
		for _, upload := range output.Uploads {
			input := &obs.AbortMultipartUploadInput{Bucket: bucketName}
			input.UploadId = upload.UploadId
			input.Key = upload.Key
			output, err := getObsClient().AbortMultipartUpload(input)
			if err == nil {
				fmt.Printf("Abort uploadId[%s] successfully\n", input.UploadId)
				fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
			}
		}
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func putObject() {
	input := &obs.PutObjectInput{}
	input.Bucket = bucketName
	input.Key = objectKey
	input.Metadata = map[string]string{"meta": "value"}
	input.Body = strings.NewReader("Hello OBS")
	output, err := getObsClient().PutObject(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		fmt.Printf("ETag:%s, StorageClass:%s\n", output.ETag, output.StorageClass)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func putFile() {
	input := &obs.PutFileInput{}
	input.Bucket = bucketName
	input.Key = objectKey
	input.SourceFile = "localfile"
	output, err := getObsClient().PutFile(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		fmt.Printf("ETag:%s, StorageClass:%s\n", output.ETag, output.StorageClass)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func uploadPart() {
	sourceFile := "localfile"
	var partSize int64 = 1024 * 1024 * 5
	fileInfo, _ := os.Stat(sourceFile)
	partCount := fileInfo.Size() / partSize
	if fileInfo.Size()%partSize > 0 {
		partCount++
	}
	var i int64
	for i = 0; i < partCount; i++ {
		input := &obs.UploadPartInput{}
		input.Bucket = bucketName
		input.Key = objectKey
		input.UploadId = "uploadid"
		input.PartNumber = int(i + 1)
		input.Offset = i * partSize
		if i == partCount-1 {
			input.PartSize = fileInfo.Size()
		} else {
			input.PartSize = partSize
		}
		input.SourceFile = sourceFile
		output, err := getObsClient().UploadPart(input)
		if err == nil {
			fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
			fmt.Printf("ETag:%s\n", output.ETag)
		} else {
			if obsError, ok := err.(obs.ObsError); ok {
				fmt.Println(obsError.StatusCode)
				fmt.Println(obsError.Code)
				fmt.Println(obsError.Message)
			} else {
				fmt.Println(err)
			}
		}
	}
}

func listParts() {
	input := &obs.ListPartsInput{}
	input.Bucket = bucketName
	input.Key = objectKey
	input.UploadId = "uploadid"
	output, err := getObsClient().ListParts(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		for index, part := range output.Parts {
			fmt.Printf("Part[%d]-ETag:%s, PartNumber:%d, LastModified:%s, Size:%d\n", index, part.ETag,
				part.PartNumber, part.LastModified, part.Size)
		}
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func completeMultipartUpload() {
	input := &obs.CompleteMultipartUploadInput{}
	input.Bucket = bucketName
	input.Key = objectKey
	input.UploadId = "uploadid"
	input.Parts = []obs.Part{
		obs.Part{PartNumber: 1, ETag: "etag1"},
		obs.Part{PartNumber: 2, ETag: "etag2"},
		obs.Part{PartNumber: 3, ETag: "etag3"},
	}
	output, err := getObsClient().CompleteMultipartUpload(input)
	if err == nil {
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		fmt.Printf("Location:%s, Bucket:%s, Key:%s, ETag:%s\n", output.Location, output.Bucket, output.Key, output.ETag)
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func copyPart() {

	sourceBucket := "source-bucket"
	sourceKey := "source-key"
	input := &obs.GetObjectMetadataInput{}
	input.Bucket = sourceBucket
	input.Key = sourceKey
	output, err := getObsClient().GetObjectMetadata(input)
	if err == nil {
		objectSize := output.ContentLength
		var partSize int64 = 5 * 1024 * 1024
		partCount := objectSize / partSize
		if objectSize%partSize > 0 {
			partCount++
		}
		var i int64
		for i = 0; i < partCount; i++ {
			input := &obs.CopyPartInput{}
			input.Bucket = bucketName
			input.Key = objectKey
			input.UploadId = "uploadid"
			input.PartNumber = int(i + 1)
			input.CopySourceBucket = sourceBucket
			input.CopySourceKey = sourceKey
			input.CopySourceRangeStart = i * partSize
			if i == partCount-1 {
				input.CopySourceRangeEnd = objectSize - 1
			} else {
				input.CopySourceRangeEnd = (i+1)*partSize - 1
			}
			output, err := getObsClient().CopyPart(input)
			if err == nil {
				fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
				fmt.Printf("ETag:%s, PartNumber:%d\n", output.ETag, output.PartNumber)
			} else {
				if obsError, ok := err.(obs.ObsError); ok {
					fmt.Println(obsError.StatusCode)
					fmt.Println(obsError.Code)
					fmt.Println(obsError.Message)
				} else {
					fmt.Println(err)
				}
			}
		}
	}
}

func getObject() {
	input := &obs.GetObjectInput{}
	input.Bucket = bucketName
	input.Key = objectKey
	output, err := getObsClient().GetObject(input)
	if err == nil {
		defer output.Body.Close()
		fmt.Printf("StatusCode:%d, RequestId:%s\n", output.StatusCode, output.RequestId)
		fmt.Printf("StorageClass:%s, ETag:%s, ContentType:%s, ContentLength:%d, LastModified:%s\n",
			output.StorageClass, output.ETag, output.ContentType, output.ContentLength, output.LastModified)
		p := make([]byte, 1024)
		var readErr error
		var readCount int
		for {
			readCount, readErr = output.Body.Read(p)
			if readCount > 0 {
				fmt.Printf("%s", p[:readCount])
			}
			if readErr != nil {
				break
			}
		}
	} else {
		if obsError, ok := err.(obs.ObsError); ok {
			fmt.Println(obsError.StatusCode)
			fmt.Println(obsError.Code)
			fmt.Println(obsError.Message)
		} else {
			fmt.Println(err)
		}
	}
}

func runExamples() {
	examples.RunBucketOperationsSample()
	//	examples.RunObjectOperationsSample()
	//	examples.RunDownloadSample()
	//	examples.RunCreateFolderSample()
	//	examples.RunDeleteObjectsSample()
	//	examples.RunListObjectsSample()
	//	examples.RunListVersionsSample()
	//	examples.RunListObjectsInFolderSample()
	//	examples.RunConcurrentCopyPartSample()
	//	examples.RunConcurrentDownloadObjectSample()
	//	examples.RunConcurrentUploadPartSample()
	//	examples.RunRestoreObjectSample()

	//	examples.RunSimpleMultipartUploadSample()
	//	examples.RunObjectMetaSample()
	//	examples.RunTemporarySignatureSample()
}

func main() {
	//---- init log ----
	obs.InitLog("/temp/OBS-SDK.log", 1024*1024*100, 5, obs.LEVEL_WARN, false)

	//---- run examples----
	//	runExamples()

	//---- bucket related APIs ----
	//	createBucket()
	//  listBuckets()
	//	obs.FlushLog()
	//	setBucketStoragePolicy()
	//	getBucketStoragePolicy()
	//	deleteBucket()
	//  listObjects()
	//  listVersions()
	//  listMultipartUploads()
	//	setBucketQuota()
	//	getBucketQuota()
	//	getBucketStorageInfo()
	//	getBucketLocation()
	//	setBucketAcl()
	//  getBucketAcl()
	//	setBucketPolicy()
	//  getBucketPolicy()
	//	deleteBucketPolicy()
	//  setBucketCors()
	//  getBucketCors()
	//  deleteBucketCors()
	//  setBucketVersioning()
	//  getBucketVersioning()
	//  headBucket()
	//  getBucketMetadata()
	//  setBucketLoggingConfiguration()
	//  getBucketLoggingConfiguration()
	//  setBucketWebsiteConfiguration()
	//  getBucketWebsiteConfiguration()
	//  deleteBucketWebsiteConfiguration()
	//  setBucketLifecycleConfiguration()
	//  getBucketLifecycleConfiguration()
	//  deleteBucketLifecycleConfiguration()
	//  setBucketTagging()
	//  getBucketTagging()
	//  deleteBucketTagging()
	//  setBucketNotification()
	//  getBucketNotification()

	//---- object related APIs ----
	//  deleteObject()
	//  deleteObjects()
	//  setObjectAcl()
	//  getObjectAcl()
	//  restoreObject()
	//  copyObject()
	//  initiateMultipartUpload()
	//  uploadPart()
	//  copyPart()
	//  listParts()
	//  completeMultipartUpload()
	//  abortMultipartUpload()
	//  putObject()
	//  putFile()
	//  getObjectMetadata()
	//  getObject()
}
