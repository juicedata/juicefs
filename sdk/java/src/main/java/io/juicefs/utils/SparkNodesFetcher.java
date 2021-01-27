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

import org.json.JSONArray;
import org.json.JSONObject;

import java.util.HashSet;
import java.util.Set;

public class SparkNodesFetcher extends NodesFetcher {
  public SparkNodesFetcher(String jfsName) {
    super(jfsName);
  }

  // url like "http://host:8888/json/"
  @Override
  protected Set<String> parseNodes(String response) throws Exception {
    Set<String> result = new HashSet<>();
    JSONArray workers = new JSONObject(response).getJSONArray("workers");
    for (Object worker : workers) {
      if (((JSONObject) worker).getString("state").equals("ALIVE")) {
        result.add(((JSONObject) worker).getString("host"));
      }
    }
    return result;
  }
}
