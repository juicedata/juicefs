/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
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

package io.juicefs.utils;

import org.apache.commons.logging.Log;
import org.apache.commons.logging.LogFactory;
import org.json.JSONArray;
import org.json.JSONObject;

import java.util.HashSet;
import java.util.Set;

// "http://hadoop01:4040/api/v1/applications/";
public class SparkThriftNodesFetcher extends NodesFetcher {
  private static final Log LOG = LogFactory.getLog(SparkThriftNodesFetcher.class);

  public SparkThriftNodesFetcher(String jfsName) {
    super(jfsName);
  }

  @Override
  public Set<String> getNodes(String[] urls) {
    if (urls == null || urls.length == 0) {
      return null;
    }
    for (String url : urls) {
      try {
        JSONArray appArrays = new JSONArray(doGet(url));
        if (appArrays.length() > 0) {
          String id = appArrays.getJSONObject(0).getString("id");
          url = url.endsWith("/") ? url : url + "/";
          return parseNodes(doGet(url + id + "/allexecutors"));
        }
      } catch (Throwable e) {
        LOG.warn("fetch from spark thrift server failed!", e);
      }
    }
    return null;
  }

  @Override
  protected Set<String> parseNodes(String response) throws Exception {
    if (response == null) {
      return null;
    }
    Set<String> res = new HashSet<>();
    for (Object item : new JSONArray(response)) {
      JSONObject obj = (JSONObject) item;
      String id = obj.getString("id");
      boolean isActive = obj.getBoolean("isActive");
      String hostPort = obj.getString("hostPort");
      boolean isBlacklisted = obj.getBoolean("isBlacklisted");
      String[] hAp = hostPort.split(":");
      if (hAp.length > 0 && !"driver".equals(id) && isActive && !isBlacklisted) {
        res.add(hAp[0]);
      }
    }
    return res;
  }
}
