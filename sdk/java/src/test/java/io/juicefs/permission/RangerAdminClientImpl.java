/*
 * JuiceFS, Copyright 2024 Juicedata, Inc.
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

package io.juicefs.permission;

import org.apache.hadoop.conf.Configuration;
import org.apache.ranger.admin.client.AbstractRangerAdminClient;
import org.apache.ranger.plugin.util.ServicePolicies;
import org.apache.ranger.plugin.util.ServiceTags;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.File;
import java.nio.file.FileSystems;
import java.nio.file.Files;
import java.util.List;

public class RangerAdminClientImpl extends AbstractRangerAdminClient {

  private static final Logger LOG = LoggerFactory.getLogger(RangerAdminClientImpl.class);

  private final static String cacheFilename = "hdfs-policies.json";
  private final static String tagFilename = "hdfs-policies-tag.json";
  public void init(String serviceName, String appId, String configPropertyPrefix, Configuration config) {
    super.init(serviceName, appId, configPropertyPrefix, config);
  }

  public ServicePolicies getServicePoliciesIfUpdated(long lastKnownVersion, long lastActivationTimeInMillis) throws Exception {

    String basedir = System.getProperty("basedir");
    if (basedir == null) {
      basedir = new File(".").getCanonicalPath();
    }
    final String relativePath  = "/src/test/resources/";
    java.nio.file.Path cachePath = FileSystems.getDefault().getPath(basedir, relativePath + cacheFilename);
    byte[] cacheBytes = Files.readAllBytes(cachePath);
    return gson.fromJson(new String(cacheBytes), ServicePolicies.class);
  }

  public ServiceTags getServiceTagsIfUpdated(long lastKnownVersion, long lastActivationTimeInMillis) throws Exception {
    String basedir = System.getProperty("basedir");
    if (basedir == null) {
      basedir = new File(".").getCanonicalPath();
    }
    final String relativePath = "/src/test/resources/";
    java.nio.file.Path cachePath = FileSystems.getDefault().getPath(basedir, relativePath + tagFilename);
    byte[] cacheBytes = Files.readAllBytes(cachePath);
    return gson.fromJson(new String(cacheBytes), ServiceTags.class);
  }

  public List<String> getTagTypes(String tagTypePattern) throws Exception {
    return null;
  }


}
