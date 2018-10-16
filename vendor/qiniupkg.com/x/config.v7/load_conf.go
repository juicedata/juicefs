package config

import (
	"bytes"
	"encoding/json"
	"flag"
	"io/ioutil"

	"qiniupkg.com/x/log.v7"
)

var (
	confName *string
)

func Init(cflag, app, default_conf string) {

	confDir, _ := GetDir(app)
	confName = flag.String(cflag, confDir+"/"+default_conf, "the config file")
}

func GetPath() string {

	if confName != nil {
		return *confName
	}
	return ""
}

func Load(conf interface{}) (err error) {

	if !flag.Parsed() {
		flag.Parse()
	}

	log.Info("Use the config file of ", *confName)
	return LoadEx(conf, *confName)
}

func LoadEx(conf interface{}, confName string) (err error) {

	data, err := ioutil.ReadFile(confName)
	if err != nil {
		log.Error("Load conf failed:", err)
		return
	}
	data = trimComments(data)

	err = json.Unmarshal(data, conf)
	if err != nil {
		log.Error("Parse conf failed:", err)
	}
	return
}

func LoadFile(conf interface{}, confName string) (err error) {

	data, err := ioutil.ReadFile(confName)
	if err != nil {
		return
	}
	data = trimComments(data)

	return json.Unmarshal(data, conf)
}

func LoadBytes(conf interface{}, data []byte) (err error) {

	return json.Unmarshal(trimComments(data), conf)
}

func LoadString(conf interface{}, data string) (err error) {

	return json.Unmarshal(trimComments([]byte(data)), conf)
}

func trimComments(data []byte) (data1 []byte) {

	var line []byte

	data1 = data[:0]
	for {
		pos := bytes.IndexByte(data, '\n')
		if pos < 0 {
			line = data
		} else {
			line = data[:pos+1]
		}
		data1 = append(data1, trimCommentsLine(line)...)
		if pos < 0 {
			return
		}
		data = data[pos+1:]
	}
}

func trimCommentsLine(line []byte) []byte {

	n := len(line)
	quoteCount := 0
	for i := 0; i < n; i++ {
		c := line[i]
		switch c {
		case '\\':
			i++
		case '"':
			quoteCount++
		case '#':
			if (quoteCount&1) == 0 {
				return line[:i]
			}
		}
	}
	return line
}

