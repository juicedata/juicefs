// Copyright (C) 2018-present Juicedata Inc.

package main

import (
	"flag"
	"net/url"
	"strings"

	"github.com/juicedata/juicesync/object"
	"github.com/juicedata/juicesync/utils"

	"github.com/sirupsen/logrus"
)

var start = flag.String("start", "", "the start of keys to sync")
var end = flag.String("end", "", "the last keys to sync")
var threads = flag.Int("p", 50, "number of concurrent threads")

var verbose = flag.Bool("v", false, "turn on debug log")
var quiet = flag.Bool("q", false, "change log level to ERROR")

var logger = utils.GetLogger("osync")

func createStorage(uri string) object.ObjectStorage {
	u, err := url.Parse(uri)
	if err != nil {
		logger.Fatalf("Can't parse %s: %s", uri, err.Error())
	}
	user := u.User
	var accessKey, secretKey string
	if user != nil {
		accessKey = user.Username()
		secretKey, _ = user.Password()
	}
	endpoint := u.Host
	if u.Scheme == "file" {
		endpoint = u.Path
	}
	objStorage := object.CreateStorage(strings.ToLower(u.Scheme), endpoint, accessKey, secretKey)
	if objStorage == nil {
		logger.Fatalf("Invalid storage type: %s", u.Scheme)
	}
	return objStorage
}

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) != 2 {
		println("osync [options] SRC DST")
		return
	}

	if *verbose {
		utils.SetLogLevel(logrus.DebugLevel)
	} else if *quiet {
		utils.SetLogLevel(logrus.ErrorLevel)
	}
	utils.InitLoggers(false)

	src := createStorage(args[0])
	dst := createStorage(args[1])
	Sync(src, dst, *start, *end)
}
