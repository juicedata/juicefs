/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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
