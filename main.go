package main

import (
	"flag"
	"net/url"
	"os"
	"osync/object"
	"osync/utils"
	"strings"

	"github.com/sirupsen/logrus"
)

var srcURI = flag.String("src", "", "source")
var dstURI = flag.String("dst", "", "destination")
var start = flag.String("start", "", "the start of keys to sync")

var version = flag.Bool("V", false, "show version")
var debug = flag.Bool("v", false, "turn on debug log")
var quiet = flag.Bool("q", false, "change log level to ERROR")

var logger = utils.GetLogger("osync")

func createStorage(uri string) object.ObjectStorage {
	u, err := url.Parse(uri)
	if err != nil {
		logger.Fatalf("Can't parse %s: %s", uri, err.Error())
	}
	secretKey, _ := u.User.Password()
	objStorage := object.CreateStorage(strings.ToLower(u.Scheme), u.Host, u.User.Username(), secretKey)
	if objStorage == nil {
		logger.Fatalf("Invalid storage type: %s", u.Scheme)
	}
	return objStorage
}

var defaultEndpoint string
var defaultURI *url.URL
var defaultKey string
var defaultSecret string
var maxUploads int

func init() {
	defaultEndpoint = os.Getenv("endpoint")
	defaultURI, _ = url.ParseRequestURI(defaultEndpoint)
}

func main() {
	flag.Parse()
	if *debug {
		utils.SetLogLevel(logrus.DebugLevel)
	} else if *quiet {
		utils.SetLogLevel(logrus.ErrorLevel)
	}
	utils.InitLoggers(false)

	src := createStorage(*srcURI)
	dst := createStorage(*dstURI)
	SyncAll(src, dst, *start)
}
