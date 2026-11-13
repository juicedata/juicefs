/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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

// Http serves a directory of a volume read-only over HTTP until it gets
// SIGINT or SIGTERM.
//
//	http [-addr 127.0.0.1:8080] [dir]
//
// dir is in the form of fs.ValidPath and defaults to "." (the root of the
// volume).
package main

import (
	"context"
	"errors"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	juicefs "github.com/juicedata/juicefs/sdk/go"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "address to listen on")
	flag.Parse()
	dir := "."
	if flag.NArg() > 0 {
		dir = flag.Arg(0)
	}
	name, metaURL := os.Getenv("JFS_VOLUME"), os.Getenv("JFS_META_URL")
	if name == "" || metaURL == "" {
		log.Fatal("set JFS_VOLUME and JFS_META_URL")
	}
	c, err := juicefs.New(name, metaURL, nil)
	if err != nil {
		log.Fatal(err)
	}
	err = serve(c, *addr, dir)
	if cerr := c.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		log.Fatal(err)
	}
}

func serve(c *juicefs.Client, addr, dir string) error {
	fsys, err := fs.Sub(c.FS(), dir)
	if err != nil {
		return err
	}
	if _, err := fs.Stat(fsys, "."); err != nil {
		return err
	}
	srv := &http.Server{Addr: addr, Handler: http.FileServerFS(fsys)}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Printf("serving %s of %s on http://%s", dir, c.Name(), addr)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	// Finish the requests in flight before the Client is closed.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
