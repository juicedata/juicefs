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
import org.apache.hadoop.conf.Configuration;
import org.json.JSONArray;
import org.json.JSONObject;

import java.util.*;

public class YarnNodesFetcher extends NodesFetcher {
  private static final Log LOG = LogFactory.getLog(YarnNodesFetcher.class);

  public YarnNodesFetcher(String jfsName) {
    super(jfsName);
  }

  @Override
  public Set<String> getNodes(String[] urls) {
    if (urls == null || urls.length == 0) {
      return null;
    }
    List<String> yarnUrls = Arrays.asList(urls);
    for (String url : urls) {
      if ("yarn".equals(url.toLowerCase().trim())) {
        Configuration conf = new Configuration();
        Map<String, String> props = conf.getValByRegex("yarn\\.resourcemanager\\.webapp\\.address.*");
        if (props.size() == 0) {
          return null;
        }
        yarnUrls = new ArrayList<>();
        for (String v : props.values()) {
          yarnUrls.add("http://" + v + "/ws/v1/cluster/nodes/");
        }
        break;
      }
    }
    return super.getNodes(yarnUrls.toArray(new String[0]));
  }

  @Override
  protected Set<String> parseNodes(String response) {
    Set<String> result = new HashSet<>();
    JSONArray allNodes = new JSONObject(response).getJSONObject("nodes").getJSONArray("node");
    for (Object obj : allNodes) {
      if (obj instanceof JSONObject) {
        JSONObject node = (JSONObject) obj;
        String state = node.getString("state");
        String hostname = node.getString("nodeHostName");
        if ("RUNNING".equals(state)) {
          result.add(hostname);
        }
      }
    }
    return result;
  }
}
