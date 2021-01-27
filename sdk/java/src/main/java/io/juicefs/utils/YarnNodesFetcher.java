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
