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

import org.apache.ranger.authorization.hadoop.config.RangerConfiguration;
import org.apache.ranger.authorization.hadoop.config.RangerPluginConfig;
import org.apache.ranger.plugin.policyengine.RangerPolicyEngineOptions;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.File;
import java.net.MalformedURLException;
import java.net.URL;

public class RangerPluginCfg extends RangerPluginConfig {
  private static final Logger LOG = LoggerFactory.getLogger(RangerPluginCfg.class);

  @Override
  public boolean addResourceIfReadable(String aResourceName) {
    URL fUrl = this.getFileLocation(aResourceName);
    if (fUrl != null) {
      try {
        this.addResource(fUrl);
      } catch (Exception e) {
        LOG.error("Unable to load the resource name [" + aResourceName + "]. Ignoring the resource:" + fUrl);
      }
    }
    return true;
  }

  public static boolean isEmpty(String str) {
    return str == null || str.length() == 0;
  }

  private URL getFileLocation(String fileName) {
    URL lurl = null;
    if (!isEmpty(fileName)) {
      lurl = RangerConfiguration.class.getClassLoader().getResource(fileName);

      if (lurl == null ) {
        lurl = RangerConfiguration.class.getClassLoader().getResource("/" + fileName);
      }

      if (lurl == null ) {
        File f = new File(fileName);
        if (f.exists()) {
          try {
            lurl=f.toURI().toURL();
          } catch (MalformedURLException e) {
            LOG.error("Unable to load the resource name [" + fileName + "]. Ignoring the resource:" + f.getPath());
          }
        } else {
          if(LOG.isDebugEnabled()) {
            LOG.debug("Conf file path " + fileName + " does not exists");
          }
        }
      }
    }
    return lurl;
  }

  public RangerPluginCfg(String serviceType, String serviceName, String appId, String clusterName, String clusterType, RangerPolicyEngineOptions policyEngineOptions) {
    super(serviceType, serviceName, appId, clusterName, clusterType, policyEngineOptions);
  }
}