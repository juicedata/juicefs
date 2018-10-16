package jsonutil

import (
	"testing"
)

func Test(t *testing.T) {

	var ret struct {
		Id string `json:"id"`
	}
	err := Unmarshal(`{"id": "123"}`, &ret)
	if err != nil {
		t.Fatal("Unmarshal failed:", err)
	}
	if ret.Id != "123" {
		t.Fatal("Unmarshal uncorrect:", ret.Id)
	}
}
