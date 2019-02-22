// Copyright (C) 2018-present Juicedata Inc.

package main

import (
	"flag"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/juicedata/juicesync/object"
	"github.com/juicedata/juicesync/utils"
	"golang.org/x/crypto/ssh/terminal"

	"github.com/sirupsen/logrus"
)

var start = flag.String("start", "", "the first key to sync")
var end = flag.String("end", "", "the last key to sync")
var threads = flag.Int("p", 50, "number of concurrent threads")

var verbose = flag.Bool("v", false, "turn on debug log")
var quiet = flag.Bool("q", false, "change log level to ERROR")

var logger = utils.GetLogger("juicesync")

func supportHTTPS(name, endpoint string) bool {
	if name == "ufile" {
		return !(strings.Contains(endpoint, ".internal-") || strings.HasSuffix(endpoint, ".ucloud.cn"))
	} else if name == "oss" {
		return !(strings.Contains(endpoint, ".vpc100-oss") || strings.Contains(endpoint, "internal.aliyuncs.com"))
	} else if name == "jss" {
		// the internal endpoint does not support HTTPS
		return false
	} else {
		return true
	}
}

func createStorage(uri string) object.ObjectStorage {
	if !strings.Contains(uri, "://") {
		if strings.Contains(uri, ":") {
			var user string
			if strings.Contains(uri, "@") {
				parts := strings.Split(uri, "@")
				user = parts[0]
				uri = parts[1]
			}
			var pass string
			if strings.Contains(user, ":") {
				parts := strings.Split(uri, ":")
				user = parts[0]
				pass = parts[1]
			} else {
				fmt.Print("Enter Password: ")
				bytePassword, err := terminal.ReadPassword(int(syscall.Stdin))
				if err != nil {
					logger.Fatalf("Read password: %s", err.Error())
				}
				pass = string(bytePassword)
			}
			return object.CreateStorage("sftp", uri, user, pass)
		}
		var e error
		uri, e = filepath.Abs(uri)
		if e != nil {
			logger.Fatalf("invalid path: %s", e.Error())
		}
		uri = "file://" + uri
	}
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
	name := strings.ToLower(u.Scheme)
	endpoint := u.Host
	if name == "file" {
		endpoint = u.Path
	} else if supportHTTPS(name, endpoint) {
		endpoint = "https://" + endpoint
	} else {
		endpoint = "http://" + endpoint
	}

	store := object.CreateStorage(name, endpoint, accessKey, secretKey)
	if store == nil {
		logger.Fatalf("Invalid storage type: %s", u.Scheme)
	}
	if name != "file" && len(u.Path) > 1 {
		store = object.WithPrefix(store, u.Path[1:])
	}
	return store
}

func main() {
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: juicesync [options] SRC DST")
		fmt.Fprintln(os.Stderr, "\tSRC and DST should be [NAME://][ACCESS_KEY:SECRET_KEY@]BUCKET.ENDPOINT[/PREFIX]")
		fmt.Fprintln(os.Stderr, "Options:")
		flag.PrintDefaults()
	}
	flag.Parse()
	args := flag.Args()
	if len(args) != 2 {
		println("juicesync [options] SRC DST")
		return
	}
	go http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", 6070), nil)

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
