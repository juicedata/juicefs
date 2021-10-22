/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
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
	_, portStr, err := net.SplitHostPort(metricsAddr)
	if err != nil {
		logger.Fatalf("Metrics url format err:%s", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		logger.Fatalf("Metrics port set err:%s", err)
	}
	config := consulapi.DefaultConfigWithLogger(hclog.New(&hclog.LoggerOptions{ //nolint:typecheck
		Name:   "consul-api",
		Output: logger.Out,
	}))
	config.Address = consulAddr
	client, err := consulapi.NewClient(config)
	if err != nil {
		logger.Fatalf("Creat consul client failed:%s", err)
	}
	localIp, err := utils.GetLocalIp(consulAddr)
	if err != nil {
		logger.Fatalf("Get local Ip failed:%s", err)
	}

	localMeta := make(map[string]string)
	hostname, err := os.Hostname()
	if err != nil {
		logger.Fatalf("Get hostname failed:%s", err)
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
		ID:      fmt.Sprintf("%s-%s", localIp, mountPoint),
		Name:    "juicefs",
		Port:    port,
		Address: localIp,
		Meta:    localMeta,
		Check:   check,
	}
	if err = client.Agent().ServiceRegister(&registration); err != nil {
		logger.Fatalf("Service register failed:%s", err)
	}
	logger.Info("Juicefs register to consul success")
}
