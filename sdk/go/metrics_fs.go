//go:build !libjfs

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
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/push"
)

// newRegistry creates a registry per client, as libjfs does for the Python
// SDK, because /.stats is gathered from it.
func newRegistry() (*prometheus.Registry, prometheus.Registerer) {
	registry := prometheus.NewRegistry()
	registerer := prometheus.WrapRegistererWithPrefix("juicefs_", registry)
	registerer.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	registerer.MustRegister(collectors.NewGoCollector())
	return registry, registerer
}

// pushLabels builds the labels as jfs_init does; extra is validated.
func pushLabels(name, extra string) map[string]string {
	labels := map[string]string{"vol_name": name, "mp": "sdk-" + strconv.Itoa(os.Getpid())}
	if h, err := os.Hostname(); err == nil {
		labels["instance"] = h
	} else {
		logger.Warnf("cannot get hostname: %s", err)
	}
	if extra == "" {
		return labels
	}
	for _, kv := range strings.Split(extra, ";") {
		k, v, _ := strings.Cut(kv, ":")
		if k == "mp" || k == "vol_name" || k == "instance" {
			logger.Warnf("overriding reserved label: %s", k)
		}
		labels[k] = v
	}
	return labels
}

// pushGateway is push2Gateway of libjfs that can be stopped.
type pushGateway struct {
	pusher *push.Pusher
	stop   chan struct{}
	done   chan struct{}
}

func startPushGateway(addr, auth string, interval time.Duration, gatherer prometheus.Gatherer, labels map[string]string) *pushGateway {
	pusher := push.New(addr, "juicefs").Gatherer(gatherer)
	for k, v := range labels {
		pusher.Grouping(k, v)
	}
	if user, password, ok := strings.Cut(auth, ":"); ok {
		pusher.BasicAuth(user, password)
	}
	pusher.Client(&http.Client{Timeout: 2 * time.Second})
	g := &pushGateway{pusher: pusher, stop: make(chan struct{}), done: make(chan struct{})}
	go g.loop(interval)
	return g
}

func (g *pushGateway) loop(interval time.Duration) {
	defer close(g.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-g.stop:
			return
		case <-ticker.C:
			g.push()
		}
	}
}

func (g *pushGateway) push() {
	if err := g.pusher.Push(); err != nil {
		logger.Warnf("error pushing to PushGateway: %s", err)
	}
}

// close pushes once more, as jfs_term does.
func (g *pushGateway) close() {
	close(g.stop)
	<-g.done
	g.push()
}
