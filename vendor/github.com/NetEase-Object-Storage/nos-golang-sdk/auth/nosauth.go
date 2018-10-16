package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"sort"
	"strings"
)

var subResources map[string]bool = map[string]bool{
	"acl":           true,
	"location":      true,
	"versioning":    true,
	"versions":      true,
	"versionId":     true,
	"uploadId":      true,
	"uploads":       true,
	"partNumber":    true,
	"delete":        true,
	"deduplication": true,
}

func SignRequest(request *http.Request, publicKey string, secretKey string,
	bucket string, encodedObject string) string {

	stringToSign := ""
	stringToSign += (request.Method + "\n")
	stringToSign += (request.Header.Get("Content-MD5") + "\n")
	stringToSign += (request.Header.Get("Content-Type") + "\n")
	stringToSign += (request.Header.Get("Date") + "\n")

	var headerKeys sort.StringSlice
	for origKey, _ := range request.Header {
		key := strings.ToLower(origKey)
		if strings.HasPrefix(key, "x-nos-") {
			headerKeys = append(headerKeys, origKey)
		}
	}

	headerKeys.Sort()

	for i := 0; i < headerKeys.Len(); i++ {
		key := strings.ToLower(headerKeys[i])
		stringToSign += (key + ":" + request.Header.Get(headerKeys[i]) + "\n")
	}

	stringToSign += (getResource(bucket, encodedObject))

	request.ParseForm()

	var keys sort.StringSlice
	for key := range request.Form {
		if _, ok := subResources[key]; ok {
			keys = append(keys, key)
		}
	}
	keys.Sort()

	for i := 0; i < keys.Len(); i++ {
		if i == 0 {
			stringToSign += "?"
		}
		stringToSign += keys[i]
		if val := request.Form[keys[i]]; val[0] != "" {
			stringToSign += ("=" + val[0])
		}

		if i < keys.Len()-1 {
			stringToSign += "&"
		}
	}
	key := []byte(secretKey)
	h := hmac.New(sha256.New, key)
	h.Write([]byte(stringToSign))
	return "NOS " + publicKey + ":" + base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func getResource(bucket string, encodedObject string) string {
	resource := "/"
	if bucket != "" {
		resource += bucket + "/"
	}
	if encodedObject != "" {
		resource += encodedObject
	}
	return resource
}
