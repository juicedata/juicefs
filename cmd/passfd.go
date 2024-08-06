//go:build !windows
// +build !windows

/*
 * JuiceFS, Copyright 2024 Juicedata, Inc.
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

package cmd

import (
	"fmt"
	"net"
	"os"
	"sync"
	"syscall"

	"github.com/juicedata/juicefs/pkg/utils"
)

// Get receives file descriptors from a Unix domain socket.
//
// Num specifies the expected number of file descriptors in one message.
// Internal files' names to be assigned are specified via optional filenames
// argument.
//
// You need to close all files in the returned slice. The slice can be
// non-empty even if this function returns an error.
func getFd(via *net.UnixConn, num int) ([]byte, []int, error) {
	if num < 1 {
		return nil, nil, nil
	}

	// get the underlying socket
	viaf, err := via.File()
	if err != nil {
		return nil, nil, err
	}
	defer viaf.Close()
	socket := int(viaf.Fd())

	// recvmsg
	msg := make([]byte, syscall.CmsgSpace(100))
	oob := make([]byte, syscall.CmsgSpace(num*4))
	n, oobn, _, _, err := syscall.Recvmsg(socket, msg, oob, 0)
	if err != nil {
		return nil, nil, err
	}

	// parse control msgs
	msgs, err := syscall.ParseSocketControlMessage(oob[:oobn])

	// convert fds to files
	fds := make([]int, 0, len(msgs))
	for _, msg := range msgs {
		var rights []int
		rights, err = syscall.ParseUnixRights(&msg)
		fds = append(fds, rights...)
		if err != nil {
			for i := range fds {
				syscall.Close(fds[i])
			}
			fds = nil
			break
		}
	}
	return msg[:n], fds, err
}

// putFd sends file descriptors to Unix domain socket.
//
// Please note that the number of descriptors in one message is limited
// and is rather small.
func putFd(via *net.UnixConn, msg []byte, fds ...int) error {
	if len(fds) == 0 {
		return nil
	}
	viaf, err := via.File()
	if err != nil {
		return err
	}
	defer viaf.Close()
	socket := int(viaf.Fd())
	rights := syscall.UnixRights(fds...)
	return syscall.Sendmsg(socket, msg, rights, nil, 0)
}

var fuseMu sync.Mutex
var fuseFd int = 0
var fuseSetting = []byte("FUSE")
var serverAddress string = fmt.Sprintf("/tmp/fuse_fd_comm.%d", os.Getpid())
var csiCommPath = os.Getenv("JFS_SUPER_COMM")

func handleFDRequest(conn *net.UnixConn) {
	defer conn.Close()
	var fds = []int{0}
	fuseMu.Lock()
	if fuseFd > 0 {
		fds = append(fds, fuseFd)
		logger.Debugf("send FUSE fd: %d", fuseFd)
	}
	err := putFd(conn, fuseSetting, fds...)
	if err != nil {
		fuseMu.Unlock()
		logger.Errorf("send fuse fds: %s", err)
		return
	}
	if fuseFd > 0 {
		_ = syscall.Close(fuseFd)
		fuseFd = -1
	}
	fuseMu.Unlock()

	var msg []byte
	msg, fds, err = getFd(conn, 1)
	if err != nil {
		logger.Debugf("recv fuse fds: %s", err)
		return
	}
	fuseMu.Lock()
	if string(msg) != "CLOSE" && fuseFd <= 0 && len(fds) == 1 {
		logger.Debugf("recv FUSE fd: %d", fds[0])
		fuseFd = fds[0]
		fuseSetting = msg
		if csiCommPath != "" {
			err = sendFuseFd(csiCommPath, fuseSetting, fuseFd)
			if err != nil {
				logger.Warnf("send fd to %s: %v", csiCommPath, err)
			}
		}
	} else {
		for _, fd := range fds {
			_ = syscall.Close(fd)
		}
		logger.Debugf("msg: %s fds: %+v", string(msg), fds)
	}
	fuseMu.Unlock()
}

func serveFuseFD(path string) {
	if csiCommPath != "" {
		fd, fSetting := getFuseFd(csiCommPath)
		if fd > 0 {
			fuseFd, fuseSetting = fd, fSetting
		}
	}
	_ = os.Remove(path)
	sock, err := net.Listen("unix", path)
	if err != nil {
		logger.Error(err)
		return
	}
	go func() {
		defer os.Remove(path)
		defer sock.Close()
		for {
			conn, err := sock.Accept()
			if err != nil {
				logger.Warnf("accept : %s", err)
				continue
			}
			go handleFDRequest(conn.(*net.UnixConn))
		}
	}()
}

func getFuseFd(path string) (int, []byte) {
	if !utils.Exists(path) {
		return -1, nil
	}
	conn, err := net.Dial("unix", path)
	if err != nil {
		logger.Warnf("dial %s: %s", path, err)
		return -1, nil
	}
	defer conn.Close()
	msg, fds, err := getFd(conn.(*net.UnixConn), 2)
	if err != nil {
		logger.Warnf("recv fds: %s", err)
		return -1, nil
	}
	_ = syscall.Close(fds[0])
	if len(fds) > 1 {
		// for old version
		_ = putFd(conn.(*net.UnixConn), []byte("CLOSE"), 0) // close it
		logger.Debugf("recv FUSE fd: %d", fds[1])
		return fds[1], msg
	}
	return 0, nil
}

func sendFuseFd(path string, msg []byte, fd int) error {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, fds, err := getFd(conn.(*net.UnixConn), 2)
	if err != nil {
		logger.Warnf("recv fds: %s", err)
		return err
	}
	for _, fd := range fds {
		_ = syscall.Close(fd)
	}
	logger.Debugf("send FUSE fd: %d", fd)
	return putFd(conn.(*net.UnixConn), msg, fd)
}
