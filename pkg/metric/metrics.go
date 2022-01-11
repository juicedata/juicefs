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

package metric

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	consulapi "github.com/hashicorp/consul/api"
	"github.com/hashicorp/go-hclog"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
)

var logger = utils.GetLogger("juicefs")

var (
	start = time.Now()
	cpu   = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "cpu_usage",
		Help: "Accumulated CPU usage in seconds.",
	}, func() float64 {
		ru := utils.GetRusage()
		return ru.GetStime() + ru.GetUtime()
	})
	memory = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "memory",
		Help: "Used memory in bytes.",
	}, func() float64 {
		_, rss := utils.MemoryUsage()
		return float64(rss)
	})
	uptime = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "uptime",
		Help: "Total running time in seconds.",
	}, func() float64 {
		return time.Since(start).Seconds()
	})
	usedSpace = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "used_space",
		Help: "Total used space in bytes.",
	})
	usedInodes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "used_inodes",
		Help: "Total number of inodes.",
	})
)

func UpdateMetrics(m meta.Meta) {
	prometheus.MustRegister(cpu)
	prometheus.MustRegister(memory)
	prometheus.MustRegister(uptime)
	prometheus.MustRegister(usedSpace)
	prometheus.MustRegister(usedInodes)

	ctx := meta.Background
	for {
		var totalSpace, availSpace, iused, iavail uint64
		err := m.StatFS(ctx, &totalSpace, &availSpace, &iused, &iavail)
		if err == 0 {
			usedSpace.Set(float64(totalSpace - availSpace))
			usedInodes.Set(float64(iused))
		}
		time.Sleep(time.Second * 10)
	}
}

func RegisterToConsul(consulAddr, metricsAddr, mountPoint string) {
	if metricsAddr == "" {
		logger.Errorf("Metrics server start err,so can't register to consul")
		return
	}
	localIp, portStr, err := net.SplitHostPort(metricsAddr)
	if err != nil {
		logger.Errorf("Metrics url format err:%s", err)
		return
	}

	// Don't register 0.0.0.0 to consul
	if localIp == "0.0.0.0" || localIp == "::" {
		localIp, err = utils.GetLocalIp(consulAddr)
		if err != nil {
			logger.Errorf("Get local ip failed: %v", err)
			return
		}
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		logger.Errorf("Metrics port set err:%s", err)
		return
	}
	config := consulapi.DefaultConfigWithLogger(hclog.New(&hclog.LoggerOptions{ //nolint:typecheck
		Name:   "consul-api",
		Output: logger.Out,
	}))
	config.Address = consulAddr
	client, err := consulapi.NewClient(config)
	if err != nil {
		logger.Errorf("Creat consul client failed:%s", err)
		return
	}

	localMeta := make(map[string]string)
	hostname, err := os.Hostname()
	if err != nil {
		logger.Errorf("Get hostname failed:%s", err)
		return
	}
	localMeta["hostName"] = hostname
	localMeta["mountPoint"] = mountPoint

	check := &consulapi.AgentServiceCheck{
		HTTP:                           fmt.Sprintf("http://%s:%d/metrics", localIp, port),
		Timeout:                        "5s",
		Interval:                       "5s",
		DeregisterCriticalServiceAfter: "30s",
	}

	registration := consulapi.AgentServiceRegistration{
		ID:      fmt.Sprintf("%s:%s", localIp, mountPoint),
		Name:    "juicefs",
		Port:    port,
		Address: localIp,
		Meta:    localMeta,
		Check:   check,
	}
	if err = client.Agent().ServiceRegister(&registration); err != nil {
		logger.Errorf("Service register failed:%s", err)
	} else {
		logger.Info("Juicefs register to consul success")
	}
}
