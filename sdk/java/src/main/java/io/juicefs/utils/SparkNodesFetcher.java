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
