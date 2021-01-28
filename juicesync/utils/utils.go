// Copyright (C) 2018-present Juicedata Inc.

package utils

import (
	"io"
	"os"
)

func Min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func CopyFile(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	if err != nil {
		return err
	}
	return out.Close()
}
