/*
 * trace.go
 *
 * Copyright 2017-2018 Bill Zissimopoulos
 */
/*
 * This file is part of Cgofuse.
 *
 * It is licensed under the MIT license. The full license text can be found
 * in the License.txt file at the root of this project.
 */

package winfsp

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
)

var (
	TracePattern = os.Getenv("CGOFUSE_TRACE")
)

var traceLogger = log.New(os.Stderr, "", log.LstdFlags|log.Lshortfile)

func SetTraceOutput(file string) {
	if "" == file {
		traceLogger.SetOutput(os.Stderr)
	} else {
		f, err := os.OpenFile(file, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		if nil != err {
			traceLogger.Printf("Error opening trace log file %s: %v", file, err)
			traceLogger.SetOutput(os.Stderr)
			return
		}
		traceLogger.SetOutput(f)
		if TracePattern == "" {
			TracePattern = "*"
		}
	}
}

func traceJoin(deref bool, vals []interface{}) string {
	rslt := ""
	for _, v := range vals {
		if deref {
			switch i := v.(type) {
			case *bool:
				rslt += fmt.Sprintf(", %#v", *i)
			case *int:
				rslt += fmt.Sprintf(", %#v", *i)
			case *int8:
				rslt += fmt.Sprintf(", %#v", *i)
			case *int16:
				rslt += fmt.Sprintf(", %#v", *i)
			case *int32:
				rslt += fmt.Sprintf(", %#v", *i)
			case *int64:
				rslt += fmt.Sprintf(", %#v", *i)
			case *uint:
				rslt += fmt.Sprintf(", %#v", *i)
			case *uint8:
				rslt += fmt.Sprintf(", %#v", *i)
			case *uint16:
				rslt += fmt.Sprintf(", %#v", *i)
			case *uint32:
				rslt += fmt.Sprintf(", %#v", *i)
			case *uint64:
				rslt += fmt.Sprintf(", %#v", *i)
			case *uintptr:
				rslt += fmt.Sprintf(", %#v", *i)
			case *float32:
				rslt += fmt.Sprintf(", %#v", *i)
			case *float64:
				rslt += fmt.Sprintf(", %#v", *i)
			case *complex64:
				rslt += fmt.Sprintf(", %#v", *i)
			case *complex128:
				rslt += fmt.Sprintf(", %#v", *i)
			case *string:
				rslt += fmt.Sprintf(", %#v", *i)
			default:
				rslt += fmt.Sprintf(", %#v", v)
			}
		} else {
			rslt += fmt.Sprintf(", %#v", v)
		}
	}
	if len(rslt) > 0 {
		rslt = rslt[2:]
	}
	return rslt
}

func Trace(skip int, prfx string, vals ...interface{}) func(vals ...interface{}) {
	if "" == TracePattern {
		return func(vals ...interface{}) {
		}
	}
	pc, _, _, ok := runtime.Caller(skip + 1)
	name := "<UNKNOWN>"
	if ok {
		fn := runtime.FuncForPC(pc)
		name = fn.Name()
		if m, _ := filepath.Match(TracePattern, name); !m {
			return func(vals ...interface{}) {
			}
		}
	}
	if "" != prfx {
		prfx = prfx + ": "
	}
	args := traceJoin(false, vals)
	return func(vals ...interface{}) {
		form := "%v%v(%v) = %v"
		rslt := ""
		rcvr := recover()
		if nil != rcvr {
			debug.PrintStack()
			rslt = fmt.Sprintf("!PANIC:%v", rcvr)
		} else {
			if len(vals) != 1 {
				form = "%v%v(%v) = (%v)"
			}
			rslt = traceJoin(true, vals)
		}
		traceLogger.Printf(form, prfx, name, args, rslt)
		if nil != rcvr {
			panic(rcvr)
		}
	}
}
