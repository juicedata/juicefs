package errors

import (
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
)

const (
	prefix = " ==> "
)

// --------------------------------------------------------------------

func New(msg string) error {
	return errors.New(msg)
}

// --------------------------------------------------------------------

type appendDetailer interface {
	AppendErrorDetail(b []byte) []byte
}

func appendErrorDetail(b []byte, err error) []byte {
	if e, ok := err.(appendDetailer); ok {
		return e.AppendErrorDetail(b)
	}
	b = append(b, prefix...)
	return append(b, err.Error()...)
}

// --------------------------------------------------------------------

type errorDetailer interface {
	ErrorDetail() string
}

func Detail(err error) string {
	if e, ok := err.(errorDetailer); ok {
		return e.ErrorDetail()
	}
	return err.Error()
}

// --------------------------------------------------------------------

type summaryErr interface {
	SummaryErr() error
}

func Err(err error) error {
	if e, ok := err.(summaryErr); ok {
		return e.SummaryErr()
	}
	return err
}

// --------------------------------------------------------------------

type ErrorInfo struct {
	err  error
	why  error
	cmd  []interface{}
	pc   uintptr
}

func shortFile(file string) string {
	pos := strings.LastIndex(file, "/src/")
	if pos != -1 {
		return file[pos+5:]
	}
	return file
}

func Info(err error, cmd ...interface{}) *ErrorInfo {
	pc, _, _, ok := runtime.Caller(1)
	if !ok {
		pc = 0
	}
	return &ErrorInfo{cmd: cmd, err: Err(err), pc: pc}
}

func InfoEx(calldepth int, err error, cmd ...interface{}) *ErrorInfo {
	pc, _, _, ok := runtime.Caller(calldepth+1)
	if !ok {
		pc = 0
	}
	return &ErrorInfo{cmd: cmd, err: Err(err), pc: pc}
}

func (r *ErrorInfo) Detail(err error) *ErrorInfo {
	r.why = err
	return r
}

func (r *ErrorInfo) NestedObject() interface{} {
	return r.err
}

func (r *ErrorInfo) SummaryErr() error {
	return r.err
}

func (r *ErrorInfo) Error() string {
	return r.err.Error()
}

func (r *ErrorInfo) ErrorDetail() string {
	b := make([]byte, 1, 64)
	b[0] = '\n'
	b = r.AppendErrorDetail(b)
	return string(b)
}

func (r *ErrorInfo) AppendErrorDetail(b []byte) []byte {
	b = append(b, prefix...)
	if r.pc != 0 {
		f := runtime.FuncForPC(r.pc)
		if f != nil {
			file, line := f.FileLine(r.pc)
			b = append(b, shortFile(file)...)
			b = append(b, ':')
			b = append(b, strconv.Itoa(line)...)
			b = append(b, ':', ' ')

			fnName := f.Name()
			fnName = fnName[strings.LastIndex(fnName, "/")+1:]
			fnName = fnName[strings.Index(fnName, ".")+1:]
			b = append(b, '[')
			b = append(b, fnName...)
			b = append(b, ']', ' ')
		}
	}
	b = append(b, Detail(r.err)...)
	b = append(b, ' ', '~', ' ')
	b = append(b, fmt.Sprintln(r.cmd...)...)
	if r.why != nil {
		b = appendErrorDetail(b, r.why)
	}
	return b
}

// --------------------------------------------------------------------
