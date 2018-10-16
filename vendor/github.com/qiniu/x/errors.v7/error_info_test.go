package errors

import (
	"errors"
	"syscall"
	"testing"
)

func MysqlError(err error, cmd ...interface{}) error {

	return InfoEx(1, syscall.EINVAL, cmd...).Detail(err)
}

func (r *ErrorInfo) makeError() error {

	err := errors.New("detail error")
	return MysqlError(err, "do sth failed")
}

func TestErrorsInfo(t *testing.T) {

	err := new(ErrorInfo).makeError()
	msg := Detail(err)
	if msg != `
 ==> qiniupkg.com/x/errors.v7/error_info_test.go:17: [(*ErrorInfo).makeError] invalid argument ~ do sth failed
 ==> detail error` {
		t.Fatal("TestErrorsInfo failed")
	}
}

