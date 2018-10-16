package config

import (
	"errors"
	"os"
)

var homeEnvNames = [][]string{
	{"HOME"},
	{"HOMEDRIVE", "HOMEPATH"},
}

var (
	ErrHomeNotFound = errors.New("$HOME not found")
)

func getEnv(name []string) (v string) {

	if len(name) == 1 {
		return os.Getenv(name[0])
	}
	for _, k := range name {
		v += os.Getenv(k)
	}
	return
}

func GetDir(app string) (dir string, err error) {

	for _, name := range homeEnvNames {
		home := getEnv(name)
		if home == "" {
			continue
		}
		dir = home + "/." + app
		err = os.MkdirAll(dir, 0777)
		return
	}
	return "", ErrHomeNotFound
}

