package awsutil_test

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"testing"

	"github.com/ks3sdklib/aws-sdk-go/aws/awsutil"
	"github.com/stretchr/testify/assert"
)

func ExampleCopy() {
	type Foo struct {
		A int
		B []*string
	}

	// Create the initial value
	str1 := "hello"
	str2 := "bye bye"
	f1 := &Foo{A: 1, B: []*string{&str1, &str2}}

	// Do the copy
	var f2 Foo
	awsutil.Copy(&f2, f1)

	// Print the result
	fmt.Println(awsutil.StringValue(f2))

	// Output:
	// {
	//   A: 1,
	//   B: ["hello","bye bye"]
	// }
}

func TestCopy(t *testing.T) {
	type Foo struct {
		A int
		B []*string
		C map[string]*int
	}

	// Create the initial value
	str1 := "hello"
	str2 := "bye bye"
	int1 := 1
	int2 := 2
	f1 := &Foo{
		A: 1,
		B: []*string{&str1, &str2},
		C: map[string]*int{
			"A": &int1,
			"B": &int2,
		},
	}

	// Do the copy
	var f2 Foo
	awsutil.Copy(&f2, f1)

	// Values are equal
	assert.Equal(t, f2.A, f1.A)
	assert.Equal(t, f2.B, f1.B)
	assert.Equal(t, f2.C, f1.C)

	// But pointers are not!
	str3 := "nothello"
	int3 := 57
	f2.A = 100
	f2.B[0] = &str3
	f2.C["B"] = &int3
	assert.NotEqual(t, f2.A, f1.A)
	assert.NotEqual(t, f2.B, f1.B)
	assert.NotEqual(t, f2.C, f1.C)
}

func TestCopyIgnoreNilMembers(t *testing.T) {
	type Foo struct {
		A *string
	}

	f := &Foo{}
	assert.Nil(t, f.A)

	var f2 Foo
	awsutil.Copy(&f2, f)
	assert.Nil(t, f2.A)

	fcopy := awsutil.CopyOf(f)
	f3 := fcopy.(*Foo)
	assert.Nil(t, f3.A)
}

func TestCopyPrimitive(t *testing.T) {
	str := "hello"
	var s string
	awsutil.Copy(&s, &str)
	assert.Equal(t, "hello", s)
}

func TestCopyNil(t *testing.T) {
	var s string
	awsutil.Copy(&s, nil)
	assert.Equal(t, "", s)
}

func TestCopyReader(t *testing.T) {
	var buf io.Reader = bytes.NewReader([]byte("hello world"))
	var r io.Reader
	awsutil.Copy(&r, buf)
	b, err := ioutil.ReadAll(r)
	assert.NoError(t, err)
	assert.Equal(t, []byte("hello world"), b)

	// empty bytes because this is not a deep copy
	b, err = ioutil.ReadAll(buf)
	assert.NoError(t, err)
	assert.Equal(t, []byte(""), b)
}

func TestCopyDifferentStructs(t *testing.T) {
	type SrcFoo struct {
		A                int
		B                []*string
		C                map[string]*int
		SrcUnique        string
		SameNameDiffType int
	}
	type DstFoo struct {
		A                int
		B                []*string
		C                map[string]*int
		DstUnique        int
		SameNameDiffType string
	}

	// Create the initial value
	str1 := "hello"
	str2 := "bye bye"
	int1 := 1
	int2 := 2
	f1 := &SrcFoo{
		A: 1,
		B: []*string{&str1, &str2},
		C: map[string]*int{
			"A": &int1,
			"B": &int2,
		},
		SrcUnique:        "unique",
		SameNameDiffType: 1,
	}

	// Do the copy
	var f2 DstFoo
	awsutil.Copy(&f2, f1)

	// Values are equal
	assert.Equal(t, f2.A, f1.A)
	assert.Equal(t, f2.B, f1.B)
	assert.Equal(t, f2.C, f1.C)
	assert.Equal(t, "unique", f1.SrcUnique)
	assert.Equal(t, 1, f1.SameNameDiffType)
	assert.Equal(t, 0, f2.DstUnique)
	assert.Equal(t, "", f2.SameNameDiffType)
}

func ExampleCopyOf() {
	type Foo struct {
		A int
		B []*string
	}

	// Create the initial value
	str1 := "hello"
	str2 := "bye bye"
	f1 := &Foo{A: 1, B: []*string{&str1, &str2}}

	// Do the copy
	v := awsutil.CopyOf(f1)
	var f2 *Foo = v.(*Foo)

	// Print the result
	fmt.Println(awsutil.StringValue(f2))

	// Output:
	// {
	//   A: 1,
	//   B: ["hello","bye bye"]
	// }
}
