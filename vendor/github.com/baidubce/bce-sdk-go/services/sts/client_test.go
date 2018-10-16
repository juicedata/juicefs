package sts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/baidubce/bce-sdk-go/util/log"
)

var CLIENT *Client

// For security reason, ak/sk should not hard write here.
type Conf struct {
	AK string
	SK string
}

func init() {
	_, f, _, _ := runtime.Caller(0)
	for i := 0; i < 7; i++ {
		f = filepath.Dir(f)
	}
	conf := filepath.Join(f, "config.json")
	fp, err := os.Open(conf)
	if err != nil {
		log.Fatal("config json file of ak/sk not given:", conf)
		os.Exit(1)
	}
	decoder := json.NewDecoder(fp)
	confObj := &Conf{}
	decoder.Decode(confObj)

	CLIENT, _ = NewClient(confObj.AK, confObj.SK)
	//log.SetLogHandler(log.STDERR)
	//log.SetLogLevel(log.INFO)
}

// ExpectEqual is the helper function for test each case
func ExpectEqual(alert func(format string, args ...interface{}),
	expected interface{}, actual interface{}) bool {
	expectedValue, actualValue := reflect.ValueOf(expected), reflect.ValueOf(actual)
	equal := false
	switch {
	case expected == nil && actual == nil:
		return true
	case expected != nil && actual == nil:
		equal = expectedValue.IsNil()
	case expected == nil && actual != nil:
		equal = actualValue.IsNil()
	default:
		if actualType := reflect.TypeOf(actual); actualType != nil {
			if expectedValue.IsValid() && expectedValue.Type().ConvertibleTo(actualType) {
				equal = reflect.DeepEqual(expectedValue.Convert(actualType).Interface(), actual)
			}
		}
	}
	if !equal {
		_, file, line, _ := runtime.Caller(1)
		alert("%s:%d: missmatch, expect %v but %v", file, line, expected, actual)
		return false
	}
	return true
}

func TestGetSessionToken(t *testing.T) {
	res, err := CLIENT.GetSessionToken(-1, "")
	ExpectEqual(t.Errorf, err, nil)
	t.Logf("%+v", res)

	acl := `
{
    "id":"10eb6f5ff6ff4605bf044313e8f3ffa5",
    "accessControlList": [
    {
        "eid": "10eb6f5ff6ff4605bf044313e8f3ffa5-1",
        "effect": "Deny",
        "resource": ["bos-rd-ssy/*"],
        "region": "bj",
        "service": "bce:bos",
        "permission": ["WRITE"]
    }
    ]
}`
	res, err = CLIENT.GetSessionToken(10, acl)
	ExpectEqual(t.Fatalf, err, nil)
	t.Logf("ak: %v", res.AccessKeyId)
	t.Logf("sk: %v", res.SecretAccessKey)
	t.Logf("sessionToken: %v", res.SessionToken)
	t.Logf("createTime: %v", res.CreateTime)
	t.Logf("expiration: %v", res.Expiration)
	t.Logf("userId: %v", res.UserId)
}
