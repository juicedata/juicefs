package cmdline

import (
	"reflect"
	"testing"
)

// ---------------------------------------------------------------------------

func equalErr(err error, errExp interface{}) bool {

	if err == nil || errExp == nil {
		return err == nil && errExp == nil
	}
	return err.Error() == errExp.(string)
}

// ---------------------------------------------------------------------------

func TestComment(t *testing.T) {

	execSub := false
	ctx := Parser{
		ExecSub: func(code string) (string, error) {
			execSub = true
			return "[" + code + "]", nil
		},
		Escape: func(c byte) string {
			return string(c)
		},
	}

	cmd, codeNext, err := ctx.ParseCode("#abc `calc $(a)+$(b)`")
	if err != EOF || codeNext != "" {
		t.Fatal("ParseCode: eof is expected")
	}
	if execSub {
		t.Fatal("don't execSub")
	}
	if len(cmd) != 0 {
		t.Fatal("len(cmd) != 0")
	}
}

// ---------------------------------------------------------------------------

type caseParse struct {
	code     string
	cmd      []string
	codeNext string
	err      interface{}
}

func TestParse(t *testing.T) {

	cases := []caseParse{
		{
			code: ";b",
			cmd: []string{"b"},
			codeNext: "",
			err: "end of file",
		},
		{
			code: ";b;abc",
			cmd: []string{"b"},
			codeNext: "abc",
			err: nil,
		},
		{
			code: "a`b`\\c",
			cmd: []string{"a[b]c"},
			codeNext: "",
			err: "end of file",
		},
		{
			code: "a`b`c 'c\\n`123`' \"c\\n\"",
			cmd: []string{"a[b]c", "c\\n`123`", "cn"},
			codeNext: "",
			err: "end of file",
		},
		{
			code: "auth qboxtest 'mac AccessKey SecretKey'",
			cmd: []string{"auth", "qboxtest", "mac AccessKey SecretKey"},
			codeNext: "",
			err: "end of file",
		},
		{
			code: "post http://rs.qiniu.com/delete/`base64 Bucket:Key`",
			cmd: []string{"post", "http://rs.qiniu.com/delete/[base64 Bucket:Key]"},
			codeNext: "",
			err: "end of file",
		},
		{
			code: "post http://rs.qiniu.com/delete `base64 Bucket:Key`",
			cmd: []string{"post", "http://rs.qiniu.com/delete", "[base64 Bucket:Key]"},
			codeNext: "",
			err: "end of file",
		},
		{
			code: "post http://rs.qiniu.com/delete/|base64 Bucket:Key|",
			cmd: []string{"post", "http://rs.qiniu.com/delete/[base64 Bucket:Key]"},
			codeNext: "",
			err: "end of file",
		},
		{
			code: `json '[
	{"code": 200}, {"code": 612}
]'`,
			cmd: []string{"json", `[
	{"code": 200}, {"code": 612}
]`},
			codeNext: "",
			err: "end of file",
		},
		{
			code: "auth qboxtest ```\nmac AccessKey SecretKey```",
			cmd: []string{"auth", "qboxtest", "mac AccessKey SecretKey"},
			codeNext: "",
			err: "end of file",
		},
		{
			code: "auth qboxtest ===\nmac AccessKey SecretKey```",
			cmd: []string{"auth", "qboxtest"},
			codeNext: "mac AccessKey SecretKey```",
			err: "incomplete string, expect ===",
		},
		{
			code: "auth qboxtest ===\rmac AccessKey SecretKey===",
			cmd: []string{"auth", "qboxtest", "mac AccessKey SecretKey"},
			codeNext: "",
			err: "end of file",
		},
		{
			code: "auth qboxtest ===\n\rmac AccessKey SecretKey===",
			cmd: []string{"auth", "qboxtest", "\rmac AccessKey SecretKey"},
			codeNext: "",
			err: "end of file",
		},
		{
			code: "auth qboxtest ===\r\n\nmac AccessKey SecretKey===",
			cmd: []string{"auth", "qboxtest", "\nmac AccessKey SecretKey"},
			codeNext: "",
			err: "end of file",
		},
		{
			code: "auth qboxtest ===mac AccessKey SecretKey===",
			cmd: []string{"auth", "qboxtest", "mac AccessKey SecretKey"},
			codeNext: "",
			err: "end of file",
		},
	}

	ctx := Parser{
		ExecSub: func(code string) (string, error) {
			return "[" + code + "]", nil
		},
		Escape: func(c byte) string {
			return string(c)
		},
	}
	for _, c := range cases {
		cmd, codeNext, err := ctx.ParseCode(c.code)
		if !equalErr(err, c.err) {
			t.Fatal("Parse failed:", c, err)
		}
		if !reflect.DeepEqual(cmd, c.cmd) || codeNext != c.codeNext {
			t.Fatal("Parse failed:", c, cmd, codeNext)
		}
	}
}

// ---------------------------------------------------------------------------

