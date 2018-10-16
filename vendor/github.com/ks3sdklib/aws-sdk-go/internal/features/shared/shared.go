// Package shared contains shared step definitions that are used across integration tests
package shared

import (
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/ks3sdklib/aws-sdk-go/aws"
	"github.com/ks3sdklib/aws-sdk-go/aws/awserr"
	"github.com/ks3sdklib/aws-sdk-go/aws/awsutil"
	. "github.com/lsegal/gucumber"
	"github.com/stretchr/testify/assert"
)

// Imported is a marker to ensure that this package's init() function gets
// executed.
//
// To use this package, import it and add:
//
// 	 var _ = shared.Imported
const Imported = true

func init() {
	if os.Getenv("DEBUG") != "" {
		aws.DefaultConfig.LogLevel = 1
	}
	if os.Getenv("DEBUG_BODY") != "" {
		aws.DefaultConfig.LogLevel = 1
		aws.DefaultConfig.LogHTTPBody = true
	}

	When(`^I call the "(.+?)" API$`, func(op string) {
		call(op, nil, false)
	})

	When(`^I call the "(.+?)" API with:$`, func(op string, args [][]string) {
		call(op, args, false)
	})

	Then(`^the value at "(.+?)" should be a list$`, func(member string) {
		vals := awsutil.ValuesAtAnyPath(World["response"], member)
		assert.NotEmpty(T, vals)
	})

	Then(`^the response should contain a "(.+?)"$`, func(member string) {
		vals := awsutil.ValuesAtAnyPath(World["response"], member)
		assert.NotEmpty(T, vals)
	})

	When(`^I attempt to call the "(.+?)" API with:$`, func(op string, args [][]string) {
		call(op, args, true)
	})

	Then(`^I expect the response error code to be "(.+?)"$`, func(code string) {
		err, ok := World["error"].(awserr.Error)
		assert.True(T, ok, "no error returned")
		if ok {
			assert.Equal(T, code, err.Code())
		}
	})

	And(`^I expect the response error message to include:$`, func(data string) {
		err, ok := World["error"].(awserr.Error)
		assert.True(T, ok, "no error returned")
		if ok {
			assert.Contains(T, err.Message(), data)
		}
	})

	And(`^I expect the response error message to include one of:$`, func(table [][]string) {
		err, ok := World["error"].(awserr.Error)
		assert.True(T, ok, "no error returned")
		if ok {
			found := false
			for _, row := range table {
				if strings.Contains(err.Message(), row[0]) {
					found = true
					break
				}
			}

			assert.True(T, found, fmt.Sprintf("no error messages matched: \"%s\"", err.Message()))
		}
	})
}

// findMethod finds the op operation on the v structure using a case-insensitive
// lookup. Returns nil if no method is found.
func findMethod(v reflect.Value, op string) *reflect.Value {
	t := v.Type()
	op = strings.ToLower(op)
	for i := 0; i < t.NumMethod(); i++ {
		name := t.Method(i).Name
		if strings.ToLower(name) == op {
			m := v.MethodByName(name)
			return &m
		}
	}
	return nil
}

// call calls an operation on World["client"] by the name op using the args
// table of arguments to set.
func call(op string, args [][]string, allowError bool) {
	v := reflect.ValueOf(World["client"])
	if m := findMethod(v, op); m != nil {
		t := m.Type()
		in := reflect.New(t.In(0).Elem())
		fillArgs(in, args)

		resps := m.Call([]reflect.Value{in})
		World["response"] = resps[0].Interface()
		World["error"] = resps[1].Interface()

		if !allowError {
			err, _ := World["error"].(error)
			assert.NoError(T, err)
		}
	} else {
		assert.Fail(T, "failed to find operation "+op)
	}
}

// reIsNum is a regular expression matching a numeric input (integer)
var reIsNum = regexp.MustCompile(`^\d+$`)

// reIsArray is a regular expression matching a list
var reIsArray = regexp.MustCompile(`^\['.*?'\]$`)
var reArrayElem = regexp.MustCompile(`'(.+?)'`)

// fillArgs fills arguments on the input structure using the args table of
// arguments.
func fillArgs(in reflect.Value, args [][]string) {
	if args == nil {
		return
	}

	for _, row := range args {
		path := row[0]
		var val interface{} = row[1]
		if reIsArray.MatchString(row[1]) {
			quotedStrs := reArrayElem.FindAllString(row[1], -1)
			strs := make([]*string, len(quotedStrs))
			for i, e := range quotedStrs {
				str := e[1 : len(e)-1]
				strs[i] = &str
			}
			val = strs
		} else if reIsNum.MatchString(row[1]) { // handle integer values
			num, err := strconv.ParseInt(row[1], 10, 64)
			if err == nil {
				val = num
			}
		}
		awsutil.SetValueAtAnyPath(in.Interface(), path, val)
	}
}
