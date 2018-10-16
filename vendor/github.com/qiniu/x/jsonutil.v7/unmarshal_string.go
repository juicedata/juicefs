package jsonutil

import (
	"encoding/json"
	"reflect"
	"unsafe"
)

// ----------------------------------------------------------

func Unmarshal(data string, v interface{}) error {

	sh := *(*reflect.StringHeader)(unsafe.Pointer(&data))
	arr := (*[1<<30]byte)(unsafe.Pointer(sh.Data))
	return json.Unmarshal(arr[:sh.Len], v)
}

// ----------------------------------------------------------

