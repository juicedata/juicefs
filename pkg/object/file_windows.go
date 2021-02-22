package object

import "os"

func getOwnerGroup(info os.FileInfo) (string, string) {
	return "", ""
}

func lookupUser(name string) int {
	return 0
}

func lookupGroup(name string) int {
	return 0
}
