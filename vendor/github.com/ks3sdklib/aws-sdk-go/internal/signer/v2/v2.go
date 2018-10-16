package v2

import(
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64" 
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ks3sdklib/aws-sdk-go/aws/credentials"
	"github.com/ks3sdklib/aws-sdk-go/internal/protocol/rest"

	"github.com/ks3sdklib/aws-sdk-go/aws"
)

const (
	authHeaderPrefix = "AWS"
	timeFormat       = "Mon, 02 Jan 2006 15:04:05 GMT"
)
var signQuerys = map[string]bool {
	"acl":true, 
	"lifecycle":true,
	"location":true, 
	"logging":true, 
	"notification":true, 
	"policy":true, 
	"requestPayment":true, 
	"torrent":true, 
	"uploadId":true, 
	"uploads":true, 
	"versionId":true,
	"versioning":true, 
	"versions":true, 
	"website":true, 
	"delete":true, 
	"thumbnail":true,
	"cors":true,
	"pfop":true,
	"querypfop":true,
	"adp":true,
	"queryadp":true,
	"partNumber":true,
	"response-content-type":true,
	"response-content-language":true,
	"response-expires":true, 
	"response-cache-control":true,
	"response-content-disposition":true, 
	"response-content-encoding":true,
}

type signer struct {
	Service     *aws.Service
	Request     *http.Request
	Time        time.Time
	ExpireTime  time.Duration
	ServiceName string
	Region      string
	CredValues  credentials.Value
	Credentials *credentials.Credentials
	Query       url.Values
	Body        io.ReadSeeker
	Debug       uint
	Logger      io.Writer

	isPresign          bool
	formattedTime      string

	canonicalHeaders string
	canonicalResource  string
	stringToSign     string
	signature        string
	authorization    string
}

func Sign(req *aws.Request) {
	if req.Service.Config.Credentials == credentials.AnonymousCredentials {
		return
	}

	region := req.Service.SigningRegion
	if region == "" {
		region = req.Service.Config.Region
	}

	name := req.Service.SigningName
	if name == "" {
		name = req.Service.ServiceName
	}

	s := signer{
		Service: req.Service,
		Request:     req.HTTPRequest,
		Time:        req.Time,
		ExpireTime:  req.ExpireTime,
		Query:       req.HTTPRequest.URL.Query(),
		Body:        req.Body,
		ServiceName: name,
		Region:      region,
		Credentials: req.Service.Config.Credentials,
		Debug:       req.Service.Config.LogLevel,
		Logger:      req.Service.Config.Logger,
	}

	req.Error = s.sign()
}

func (v2 *signer) sign() error {
	if v2.ExpireTime != 0 {
		v2.isPresign = true
	}

	if v2.isRequestSigned() {
		if !v2.Credentials.IsExpired() {
			// If the request is already signed, and the credentials have not
			// expired yet ignore the signing request.
			return nil
		}

		// The credentials have expired for this request. The current signing
		// is invalid, and needs to be request because the request will fail.
		if v2.isPresign {
			v2.removePresign()
			// Update the request's query string to ensure the values stays in
			// sync in the case retrieving the new credentials fails.
			v2.Request.URL.RawQuery = v2.Query.Encode()
		}
	}

	var err error
	v2.CredValues, err = v2.Credentials.Get()
	if err != nil {
		return err
	}


	v2.build()

	if v2.Debug > 0 {
		v2.logSigningInfo()
	}

	return nil
}

func (v2 *signer) logSigningInfo() {
	out := v2.Logger
	fmt.Fprintf(out, "---[ STRING TO SIGN ]--------------------------------\n")
	fmt.Fprintln(out, v2.stringToSign)
	if v2.isPresign {
		fmt.Fprintf(out, "---[ SIGNED URL ]--------------------------------\n")
		fmt.Fprintln(out, v2.Request.URL)
	}
	fmt.Fprintf(out, "-----------------------------------------------------\n")
}

func (v2 *signer) build() {

	v2.buildTime()             // no depends
	v2.buildCanonicalHeaders() // depends on cred string
	v2.buildCanonicalResource()  // depends on canon headers / signed headers
	v2.buildStringToSign()     // depends on canon string
	v2.buildSignature()        // depends on string to sign

	if v2.isPresign {
		var querys url.Values 
		querys = make(map[string][]string)
		querys.Add("Signature",v2.signature)
		querys.Add("AWSAccessKeyId",v2.CredValues.AccessKeyID)
		v2.Request.URL.RawQuery+= "&"+querys.Encode()
	} else {
		v2.Request.Header.Set("Authorization","AWS "+v2.CredValues.AccessKeyID+":"+v2.signature)
	}
}

func (v2 *signer) buildTime() {
	v2.formattedTime = v2.Time.UTC().Format(timeFormat)

	if v2.isPresign {
		duration := int64(v2.ExpireTime / time.Second)
		v2.Query.Set("Expires", strconv.FormatInt(duration, 10))
	} else {
		v2.Request.Header.Set("Date", v2.formattedTime)
	}
}

func (v2 *signer) buildCanonicalHeaders() {
	var headers []string
	for k := range v2.Request.Header {
		if strings.HasPrefix(strings.ToLower(http.CanonicalHeaderKey(k)), "x-amz-"){
			headers = append(headers, k)
		}
	}
	sort.Strings(headers)

	headerValues := make([]string, len(headers))
	for i, k := range headers {
		headerValues[i] = strings.ToLower(http.CanonicalHeaderKey(k)) + ":" +
				strings.Join(v2.Request.Header[http.CanonicalHeaderKey(k)], ",")
	}

	v2.canonicalHeaders = strings.Join(headerValues, "\n")
}

func (v2 *signer) buildCanonicalResource(){
	endpoint := v2.Service.Endpoint
	
	v2.Request.URL.RawQuery = strings.Replace(v2.Query.Encode(), "+", "%20", -1)
	url := v2.Request.URL.String()
	//在aws.service.go,buildEndpoint会把sheme也加上
	pathStyle := strings.HasPrefix(url,endpoint)
	uri := v2.Request.URL.Opaque

	bucketInHost:=""
	if !pathStyle{
		if strings.HasPrefix(url,"http://") {
			url = url[7:]
			endpoint = endpoint[7:]
		}else if strings.HasPrefix(url,"https://")	{
			url = url[8:]
			endpoint = endpoint[8:]
		}
		bucketInHost = url[0:strings.Index(url,endpoint)-1]
	}
	if uri != "" {
		uris := strings.Split(uri, "/")[3:]
		append := false
		if len(uris) == 1 && uris[0]!=""&&bucketInHost == ""{
			//只有bucket
			append = true
		}else if len(uris) == 0 && bucketInHost != ""{
			append = true
		}
		uri = "/" + strings.Join(strings.Split(uri, "/")[3:],"/")
		if bucketInHost != ""{
			uri = "/"+bucketInHost +uri
		}
		if append{
			uri += "/"
		}
	} else {
		uri = v2.Request.URL.Path
	}
	if uri == "" {
		uri = "/"
	}

	if v2.ServiceName != "s3" {
		uri = rest.EscapePath(uri, false)
	}

	var querys []string
	for k := range v2.Query {
		if _, ok := signQuerys[k]; ok {
			querys = append(querys,k)
		}
	}
	sort.Strings(querys)

	queryValues := make([]string, len(querys))
	for i, k := range querys {
		v := v2.Query[k]
		vString := strings.Join(v,",")
		if vString != ""{
			queryValues[i] = k + "=" + vString;
		}else{
			queryValues[i] = k
		}
	}
	queryString := strings.Join(queryValues, "&")
	if queryString == ""{
		v2.canonicalResource = uri
	}else{
		v2.canonicalResource = uri + "?" + queryString
	}
}

func (v2 *signer) buildStringToSign() {
	md5list := v2.Request.Header["Content-Md5"]
	md5 := ""
	if len(md5list)>0{
		md5 =  v2.Request.Header["Content-Md5"][0]
	}

	typelist := v2.Request.Header["Content-Type"]
	contenttype := ""
	if len(typelist)>0{
		contenttype =  v2.Request.Header["Content-Type"][0]
	}

	signItems := [] string{v2.Request.Method,md5,contenttype}
	if v2.isPresign {
		signItems = append(signItems,v2.Query["Expires"][0])
	}else{
		signItems = append(signItems,v2.formattedTime)
	}
	if v2.canonicalHeaders != ""{
		signItems = append(signItems,v2.canonicalHeaders)
	}
	signItems = append(signItems,v2.canonicalResource)

	v2.stringToSign = strings.Join(signItems, "\n")

}

func (v2 *signer) buildSignature() {
	secret := v2.CredValues.SecretAccessKey
	signature := string(base64Encode(makeHmac([]byte(secret), []byte(v2.stringToSign))))
	v2.signature = signature
}
// isRequestSigned returns if the request is currently signed or presigned
func (v2 *signer) isRequestSigned() bool {
	if v2.isPresign && v2.Query.Get("Signature") != "" {
		return true
	}
	if v2.Request.Header.Get("Authorization") != "" {
		return true
	}

	return false
}

// unsign removes signing flags for both signed and presigned requests.
func (v2 *signer) removePresign() {
	v2.Query.Del("AWSAccessKeyId")
	v2.Query.Del("Signature")
	v2.Query.Del("Expires")

}

func makeHmac(key []byte, data []byte) []byte {
	hash := hmac.New(sha1.New, key)
	hash.Write(data)
	return hash.Sum(nil)
}
func base64Encode(src []byte) []byte {  
    return []byte(base64.StdEncoding.EncodeToString(src))  
}  
