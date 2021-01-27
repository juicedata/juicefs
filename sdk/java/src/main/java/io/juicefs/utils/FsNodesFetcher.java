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
import org.apache.hadoop.fs.FSDataInputStream;
import org.apache.hadoop.fs.FileSystem;
import org.apache.hadoop.fs.Path;

import java.io.BufferedReader;
import java.io.InputStreamReader;
import java.util.List;
import java.util.Set;
import java.util.stream.Collectors;

public class FsNodesFetcher extends NodesFetcher {
  private static final Log LOG = LogFactory.getLog(FsNodesFetcher.class);

  public FsNodesFetcher(String jfsName) {
    super(jfsName);
  }

  @Override
  public List<String> fetchNodes(String uri) {
    Path path = new Path(uri);
    try {
      FileSystem fs = path.getFileSystem(new Configuration());
      if (!fs.exists(path)) return null;
      FSDataInputStream inputStream = fs.open(path);
      List<String> res = new BufferedReader(new InputStreamReader(inputStream))
              .lines().collect(Collectors.toList());
      inputStream.close();
      return res;
    } catch (Throwable e) {
      LOG.warn(e.getMessage());
    }
    return null;
  }

  @Override
  protected Set<String> parseNodes(String response) throws Exception {
    return null;
  }
}
