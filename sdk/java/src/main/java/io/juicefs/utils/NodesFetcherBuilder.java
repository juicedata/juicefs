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

public class NodesFetcherBuilder {
  public static NodesFetcher buildFetcher(String urls, String jfsName) {
    NodesFetcher fetcher;
    if (urls.contains("cluster/nodes") || "yarn".equals(urls.toLowerCase().trim())) {
      fetcher = new YarnNodesFetcher(jfsName);
    } else if (urls.contains("service/presto")) {
      fetcher = new PrestoNodesFetcher(jfsName);
    } else if (urls.contains("/json")) {
      fetcher = new SparkNodesFetcher(jfsName);
    } else if (urls.contains("api/v1/applications")) {
      fetcher = new SparkThriftNodesFetcher(jfsName);
    } else {
      fetcher = new FsNodesFetcher(jfsName);
    }
    return fetcher;
  }
}
