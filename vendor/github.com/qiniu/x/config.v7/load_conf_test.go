package config_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"qiniupkg.com/x/config.v7"
)

func TestTrimComments(t *testing.T) {

	confData := `{
    "debug_level": 0, # 调试级别
    "rs_host": "http://localhost:15001", #RS服务
    "limit": 5, #限制数
    "retryTimes": 56,
    "quote0": "###",
    "quote": "quo\\\"\\#",
    "ant": "ant\\#" #123
}`

	confDataExp := `{
    "debug_level": 0,
    "rs_host": "http://localhost:15001",
    "limit": 5,
    "retryTimes": 56,
    "quote0": "###",
    "quote": "quo\\\"\\#",
    "ant": "ant\\#"
}`

	var (
		conf, confExp interface{}
	)
	err := config.LoadString(&conf, confData)
	if err != nil {
		t.Fatal("config.LoadString(conf) failed:", err)
	}
	err = config.LoadString(&confExp, confDataExp)
	if err != nil {
		t.Fatal("config.LoadString(confExp) failed:", err)
	}

	b, err := json.Marshal(conf)
	if err != nil {
		t.Fatal("json.Marshal failed:", err)
	}

	bExp, err := json.Marshal(confExp)
	if err != nil {
		t.Fatal("json.Marshal(exp) failed:", err)
	}

	if !bytes.Equal(b, bExp) {
		t.Fatal("b != bExp")
	}
}

