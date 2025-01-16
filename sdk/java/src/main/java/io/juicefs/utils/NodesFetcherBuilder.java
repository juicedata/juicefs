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

import org.apache.hadoop.conf.Configuration;

public class NodesFetcherBuilder {
  public static NodesFetcher buildFetcher(String urls, String jfsName, Configuration conf) {
    NodesFetcher fetcher;
    if ((urls.startsWith("http") && urls.contains("cluster/nodes"))
        || "yarn".equals(urls.toLowerCase().trim())) {
      fetcher = new YarnNodesFetcher(jfsName);
    } else if (urls.startsWith("http") && urls.contains("service/presto")) {
      fetcher = new PrestoNodesFetcher(jfsName);
    }  else if (urls.startsWith("http") && urls.contains("/json")) {
      fetcher = new SparkNodesFetcher(jfsName);
    } else if (urls.startsWith("http") && urls.contains("api/v1/applications")) {
      fetcher = new SparkThriftNodesFetcher(jfsName);
    } else {
      fetcher = new FsNodesFetcher(jfsName);
      ((FsNodesFetcher) fetcher).setConf(conf);
    }
    return fetcher;
  }
}
