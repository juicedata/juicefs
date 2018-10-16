// Package utilassert provides testing assertion generation functions.
package utilassert

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/ks3sdklib/aws-sdk-go/internal/model/api"
	"github.com/ks3sdklib/aws-sdk-go/internal/util/utilsort"
)

// findMember searches the shape for the member with the matching key name.
func findMember(shape *api.Shape, key string) string {
	for actualKey := range shape.MemberRefs {
		if strings.ToLower(key) == strings.ToLower(actualKey) {
			return actualKey
		}
	}
	return ""
}

// GenerateAssertions builds assertions for a shape based on its type.
//
// The shape's recursive values also will have assertions generated for them.
func GenerateAssertions(out interface{}, shape *api.Shape, prefix string) string {
	switch t := out.(type) {
	case map[string]interface{}:
		keys := utilsort.SortedKeys(t)

		code := ""
		if shape.Type == "map" {
			for _, k := range keys {
				v := t[k]
				s := shape.ValueRef.Shape
				code += GenerateAssertions(v, s, prefix+"[\""+k+"\"]")
			}
		} else {
			for _, k := range keys {
				v := t[k]
				m := findMember(shape, k)
				s := shape.MemberRefs[m].Shape
				code += GenerateAssertions(v, s, prefix+"."+m+"")
			}
		}
		return code
	case []interface{}:
		code := ""
		for i, v := range t {
			s := shape.MemberRef.Shape
			code += GenerateAssertions(v, s, prefix+"["+strconv.Itoa(i)+"]")
		}
		return code
	default:
		switch shape.Type {
		case "timestamp":
			return fmt.Sprintf("assert.Equal(t, time.Unix(%#v, 0).UTC().String(), %s.String())\n", out, prefix)
		case "blob":
			return fmt.Sprintf("assert.Equal(t, %#v, string(%s))\n", out, prefix)
		case "integer", "long":
			return fmt.Sprintf("assert.Equal(t, int64(%#v), *%s)\n", out, prefix)
		default:
			return fmt.Sprintf("assert.Equal(t, %#v, *%s)\n", out, prefix)
		}
	}
}

// Match is a testing helper to test for testing error by comparing expected
// with a regular expression.
func Match(t *testing.T, regex, expected string) {
	if !regexp.MustCompile(regex).Match([]byte(expected)) {
		t.Errorf("%q\n\tdoes not match /%s/", expected, regex)
	}
}
