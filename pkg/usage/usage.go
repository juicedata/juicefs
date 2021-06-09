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

package usage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
)

const reportUrl = "https://juicefs.com/report-usage"

var logger = utils.GetLogger("juicefs")

type usage struct {
	VolumeID   string `json:"volumeID"`
	SessionID  int64  `json:"sessionID"`
	UsedSpace  int64  `json:"usedBytes"`
	UsedInodes int64  `json:"usedInodes"`
	Version    string `json:"version"`
	Uptime     int64  `json:"uptime"`
	MetaEngine string `json:"metaEngine"` // type of meta engine
	DataStore  string `json:"dataStore"`  // type of object store
}

func sendUsage(u usage) error {
	body, err := json.Marshal(u)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", reportUrl, bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("got %s", resp.Status)
	}
	_, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return nil
}

// ReportUsage will send anonymous usage data to juicefs.io to help the team
// understand how the community is using it. You can use `--no-usage-report`
// to disable this.
func ReportUsage(m meta.Meta, version string) {
	ctx := meta.Background
	var u usage
	if format, err := m.Load(); err == nil {
		u.VolumeID = format.UUID
		u.DataStore = format.Storage
	}
	u.MetaEngine = m.Name()
	u.SessionID = int64(rand.Uint32())
	u.Version = version
	var start = time.Now()
	for {
		var totalSpace, availSpace, iused, iavail uint64
		_ = m.StatFS(ctx, &totalSpace, &availSpace, &iused, &iavail)
		u.Uptime = int64(time.Since(start).Seconds())
		u.UsedSpace = int64(totalSpace - availSpace)
		u.UsedInodes = int64(iused)

		if err := sendUsage(u); err != nil {
			logger.Debugf("send usage: %s", err)
		}
		time.Sleep(time.Minute * 10)
	}
}
