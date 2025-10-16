/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package utils

import (
	"context"
	"crypto/rand"
	"fmt"
	"mime"
	"net"
	"os"
	"os/user"
	"path"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-isatty"
)

// Exists checks if the file/folder in given path exists
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || !os.IsNotExist(err) //skip mutate
}

// SplitDir splits a path with default path list separator or comma.
func SplitDir(d string) []string {
	dd := strings.Split(d, string(os.PathListSeparator))
	if len(dd) == 1 {
		dd = strings.Split(dd[0], ",")
	}
	return dd
}

// GetLocalIp get the local ip used to access remote address.
func GetLocalIp(address string) (string, error) {
	conn, err := net.Dial("udp", address)
	if err != nil {
		return "", err
	}
	ip, _, err := net.SplitHostPort(conn.LocalAddr().String())
	if err != nil {
		return "", err
	}
	return ip, nil
}

func FindLocalIPs(allowedInterfaces ...string) ([]net.IP, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	// Build a set of allowed interface names for fast lookup
	allowedSet := make(map[string]bool)
	for _, name := range allowedInterfaces {
		allowedSet[name] = true
	}
	checkAllowed := len(allowedSet) > 0

	var ips []net.IP
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue // interface down
		}
		if iface.Flags&net.FlagLoopback != 0 {
			continue // loopback interface
		}
		// Filter by interface name if allowedInterfaces is specified
		if checkAllowed && !allowedSet[iface.Name] {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if len(ip) > 0 && !ip.IsLoopback() {
				ips = append(ips, ip)
			}
		}
	}
	return ips, nil
}

func WithTimeout(pCtx context.Context, f func(context.Context) error, timeout time.Duration) error {
	var done = make(chan int, 1)
	var t = time.NewTimer(timeout)
	var err error
	ctx, cancel := context.WithCancel(pCtx)
	go func() {
		err = f(ctx)
		done <- 1
	}()
	select {
	case <-ctx.Done():
		err = ctx.Err()
		t.Stop()
	case <-done:
		t.Stop()
	case <-t.C:
		err = fmt.Errorf("timeout after %s: %w", timeout, ErrFuncTimeout)
	}
	cancel()
	return err
}

func RemovePassword(uri string) string {
	p := strings.LastIndex(uri, "@")
	if p < 0 {
		return uri
	}
	sp := strings.Index(uri, "://") + 3
	if sp == 2 {
		sp = 0
	}
	cp := strings.Index(uri[sp:], ":")
	if cp < 0 || sp+cp > p {
		return uri
	}
	return uri[:sp+cp] + ":****" + uri[p:]
}

func GuessMimeType(key string) string {
	mimeType := mime.TypeByExtension(path.Ext(key))
	if !strings.ContainsRune(mimeType, '/') {
		mimeType = "application/octet-stream"
	}
	return mimeType
}

func StringContains(s []string, e string) bool {
	for _, item := range s {
		if item == e {
			return true
		}
	}
	return false
}

func FormatBytes(n uint64) string {
	if n < 1024 {
		return fmt.Sprintf("%d Bytes", n)
	}
	units := []string{"K", "M", "G", "T", "P", "E"}
	m := n
	i := 0
	for ; i < len(units)-1 && m >= 1<<20; i++ {
		m = m >> 10
	}
	return fmt.Sprintf("%.2f %siB (%d Bytes)", float64(m)/1024.0, units[i], n)
}

func SupportANSIColor(fd uintptr) bool {
	return isatty.IsTerminal(fd) && runtime.GOOS != "windows"
}

func RandRead(buf []byte) {
	if _, err := rand.Read(buf); err != nil {
		logger.Fatalf("Generate random content: %s", err)
	}
}

var uids = make(map[int]string)
var gids = make(map[int]string)
var users = make(map[string]int)
var groups = make(map[string]int)
var mutex sync.Mutex

var logger = GetLogger("juicefs")

func UserName(uid int) string {
	mutex.Lock()
	defer mutex.Unlock()
	name, ok := uids[uid]
	if !ok {
		if u, err := user.LookupId(strconv.Itoa(uid)); err == nil {
			name = u.Username
		} else {
			logger.Warnf("lookup uid %d: %s", uid, err)
			name = strconv.Itoa(uid)
		}
		uids[uid] = name
	}
	return name
}

func GroupName(gid int) string {
	mutex.Lock()
	defer mutex.Unlock()
	name, ok := gids[gid]
	if !ok {
		if g, err := user.LookupGroupId(strconv.Itoa(gid)); err == nil {
			name = g.Name
		} else {
			logger.Warnf("lookup gid %d: %s", gid, err)
			name = strconv.Itoa(gid)
		}
		gids[gid] = name
	}
	return name
}

func LookupUser(name string) int {
	mutex.Lock()
	defer mutex.Unlock()
	if u, ok := users[name]; ok {
		return u
	}
	var uid = -1
	if u, err := user.Lookup(name); err == nil {
		uid, _ = strconv.Atoi(u.Uid)
	} else {
		if g, e := strconv.Atoi(name); e == nil {
			uid = g
		} else {
			logger.Warnf("lookup user %s: %s", name, err)
		}
	}
	users[name] = uid
	return uid
}

func LookupGroup(name string) int {
	mutex.Lock()
	defer mutex.Unlock()
	if u, ok := groups[name]; ok {
		return u
	}
	var gid = -1
	if u, err := user.LookupGroup(name); err == nil {
		gid, _ = strconv.Atoi(u.Gid)
	} else {
		if g, e := strconv.Atoi(name); e == nil {
			gid = g
		} else {
			logger.Warnf("lookup group %s: %s", name, err)
		}
	}
	groups[name] = gid
	return gid
}

func Duration(s string) time.Duration {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err == nil {
		return time.Microsecond * time.Duration(v*1e6)
	}

	err = nil
	var d time.Duration
	p := strings.Index(s, "d")
	if p >= 0 {
		v, err = strconv.ParseFloat(s[:p], 64)
	}
	if err == nil && s[p+1:] != "" {
		d, err = time.ParseDuration(s[p+1:])
	}

	if err != nil {
		logger.Warnf("Invalid duration value: %s, setting it to 0", s)
		return 0
	}
	return d + time.Hour*time.Duration(v*24)
}
