package ts

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

const logStackLevel = 2

func log(skip int, t *testing.T, args ...interface{}) {

	_, file, line, _ := runtime.Caller(skip)
	_, fname := filepath.Split(file)
	args1 := make([]interface{}, len(args)+1)
	args1[0] = fname + ":" + strconv.Itoa(line) + ":"
	copy(args1[1:], args)

	if os.PathSeparator == '/' {
		fmt.Fprintln(os.Stdout, args1...)
	} else {
		t.Log(args1...)
	}
}

func logf(skip int, t *testing.T, format string, args ...interface{}) {

	_, file, line, _ := runtime.Caller(skip)
	_, fname := filepath.Split(file)
	args2 := make([]interface{}, len(args)+2)
	args2[0] = fname
	args2[1] = line
	copy(args2[2:], args)

	if os.PathSeparator == '/' {
		fmt.Fprintf(os.Stderr, "%s:%d: "+format+"\n", args2...)
	} else {
		t.Logf("%s:%d: "+format, args2...)
	}
}

// Log formats its arguments using default formatting, analogous to Print(),
// and records the text in the error log.
func Log(t *testing.T, args ...interface{}) {
	log(logStackLevel, t, args...)
}

// Logf formats its arguments according to the format, analogous to Printf(),
// and records the text in the error log.
func Logf(t *testing.T, format string, args ...interface{}) {
	logf(logStackLevel, t, format, args...)
}

// Error is equivalent to Log() followed by Fail().
func Error(t *testing.T, args ...interface{}) {
	log(logStackLevel, t, args...)
	t.Fail()
}

// Errorf is equivalent to Logf() followed by Fail().
func Errorf(t *testing.T, format string, args ...interface{}) {
	logf(logStackLevel, t, format, args...)
	t.Fail()
}

// Fatal is equivalent to Log() followed by FailNow().
func Fatal(t *testing.T, args ...interface{}) {
	log(logStackLevel, t, args...)
	t.FailNow()
}

// Fatalf is equivalent to Logf() followed by FailNow().
func Fatalf(t *testing.T, format string, args ...interface{}) {
	logf(logStackLevel, t, format, args...)
	t.FailNow()
}
