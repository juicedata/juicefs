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

import io.juicefs.utils.ReflectionUtil;
import org.apache.hadoop.fs.FileSystem;
import org.apache.ranger.admin.client.RangerAdminClient;
import org.apache.ranger.authorization.hadoop.config.RangerPluginConfig;
import org.apache.ranger.plugin.service.RangerBasePlugin;
import org.apache.ranger.plugin.service.RangerChainedPlugin;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.List;

public class RangerJfsPlugin extends RangerBasePlugin {
  private static final Logger LOG = LoggerFactory.getLogger(RangerJfsPlugin.class);

  private FileSystem fs;
  private String rangerUrl;
  private RangerAdminRefresher refresher;
  private long pollingIntervalMs;

  public RangerJfsPlugin(FileSystem fs, String serviceName, String rangerUrl, long pollingIntervalMs) {
    super(new RangerPluginCfg("hdfs", serviceName, "jfs", null, null, null));
    this.fs = fs;
    this.rangerUrl = rangerUrl;
    RangerPluginConfig config = getConfig();
    config.addResource(fs.getConf());
    this.pollingIntervalMs = pollingIntervalMs;
  }

  @Override
  public void init() {
    cleanup();
    RangerAdminClient admin = createAdminClient(getConfig());
    refresher = new RangerAdminRefresher(this, admin, fs, rangerUrl, pollingIntervalMs);
    refresher.start();
    List<RangerChainedPlugin> chainedPlugins = null;
    try {
      chainedPlugins = (List<RangerChainedPlugin>) ReflectionUtil.getField(RangerBasePlugin.class.getName(), "chainedPlugins", this);
    } catch (Exception e) {
      LOG.warn("Get field \"chainedPlugins\" failed", e);
    }
    if (chainedPlugins != null) {
      for (RangerChainedPlugin plugin : chainedPlugins) {
        plugin.init();
      }
    }
  }

  @Override
  public void cleanup() {
    super.cleanup();
    if (refresher != null) {
      refresher.stop();
    }
  }
}
