/*
 * JuiceFS, Copyright 2025 Juicedata, Inc.
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
package io.juicefs.tools;

import com.beust.jcommander.Parameter;
import com.beust.jcommander.Parameters;
import io.juicefs.JuiceFileSystemImpl;
import io.juicefs.Main;
import io.juicefs.permission.RangerPermissionChecker;
import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.security.UserGroupInformation;

import java.io.IOException;
import java.net.URI;

@Parameters(commandDescription = "Download policies from ranger and save to JuiceFS")
public class RangerDownloader extends Main.Command {

  @Parameter(names = {"--fs"}, description = "JuiceFileSystem: jfs://{JFS_VOL_NAME}", required = true)
  private String fs;

  @Parameter(names = {"--keytab"}, description = "local keytab file location")
  private String keytab;

  @Parameter(names = {"--principal"}, description = "principal allowed access ranger admin")
  private String principal;

  @Override
  public void init() throws IOException {

  }

  @Override
  public void run() throws IOException {
    UserGroupInformation ugi = UserGroupInformation.getCurrentUser();
    if (!ugi.hasKerberosCredentials() && (keytab == null || principal == null)) {
      throw new IllegalArgumentException("No kerberos credential was found! Parameter \"--keytab\" and \"--principal\" must be provided.");
    }
    if (keytab != null) {
      UserGroupInformation.loginUserFromKeytab(principal, keytab);
    }
    Configuration cfg = new Configuration();
    JuiceFileSystemImpl jfs = new JuiceFileSystemImpl(true);
    jfs.initialize(URI.create(fs), cfg);
    new RangerPermissionChecker(jfs, jfs.checkAndGetRangerParams(cfg));
  }

  @Override
  public void close() throws IOException {

  }

  @Override
  public String getCommand() {
    return "ranger";
  }
}
