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

package juicefs

import (
	"sync/atomic"
	"syscall"

	"github.com/prometheus/client_golang/prometheus"
)

// Client is a handle to a JuiceFS volume, modeled on os.Root. Its methods are
// safe for concurrent use.
type Client struct {
	name    string
	backend backend
	umask   uint16

	closed atomic.Bool
}

// New connects to the volume at metaURL. name is only used in metric labels
// and logs. A nil conf means DefaultConfig().
func New(name, metaURL string, conf *Config) (*Client, error) {
	if conf == nil {
		conf = DefaultConfig()
	}
	if err := conf.validate(); err != nil {
		return nil, err
	}
	b, err := newBackend(name, metaURL, conf)
	if err != nil {
		return nil, err
	}
	return &Client{name: name, backend: b, umask: getUmask()}, nil
}

// getUmask reads the umask, which syscall.Umask can only read by setting it.
func getUmask() uint16 {
	mask := syscall.Umask(0)
	syscall.Umask(mask)
	return uint16(mask)
}

func (c *Client) Name() string {
	return c.name
}

func (c *Client) Gatherer() prometheus.Gatherer {
	return c.backend.gatherer()
}

// Close flushes and closes the session. Later calls of other methods return
// fs.ErrClosed; later calls of Close return nil.
func (c *Client) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	return c.backend.close()
}
