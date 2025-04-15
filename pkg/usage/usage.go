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

package usage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
)

var reportUrl = "https://juicefs.com/report-usage"

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
	_, err = io.ReadAll(resp.Body)
	return err
}

// ReportUsage will send anonymous usage data to juicefs.com to help the team
// understand how the community is using it. You can use `--no-usage-report`
// to disable this.
func ReportUsage(m meta.Meta, version string) {
	ctx := meta.Background()
	var u usage
	if format, err := m.Load(false); err == nil {
		u.VolumeID = format.UUID
		u.DataStore = format.Storage
	}
	u.MetaEngine = m.Name()
	u.SessionID = int64(rand.Uint32())
	u.Version = version
	var start = time.Now()
	for {
		var totalSpace, availSpace, iused, iavail uint64
		_ = m.StatFS(ctx, meta.RootInode, &totalSpace, &availSpace, &iused, &iavail)
		u.Uptime = int64(time.Since(start).Seconds())
		u.UsedSpace = int64(totalSpace - availSpace)
		u.UsedInodes = int64(iused)

		if err := sendUsage(u); err != nil {
			logger.Debugf("send usage: %s", err)
		}
		time.Sleep(time.Hour)
	}
}
