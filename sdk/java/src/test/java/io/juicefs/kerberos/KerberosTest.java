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
package io.juicefs.kerberos;

import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.FileStatus;
import org.apache.hadoop.fs.FileSystem;
import org.apache.hadoop.fs.Path;
import org.apache.hadoop.security.UserGroupInformation;
import org.apache.hadoop.security.token.Token;
import org.apache.hadoop.security.token.delegation.AbstractDelegationTokenIdentifier;
import org.junit.Test;

import java.io.IOException;
import java.security.PrivilegedExceptionAction;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.fail;

public class KerberosTest {
  private static final String clientPrincipal = "client/localhost";
  private static final String clientKeytab = "/tmp/client.keytab";
  private static final String tomPrincipal = "tom/localhost";
  private static final String tomKeytab = "/tmp/tom.keytab";

  private static final String jerryPrincipal = "jerry/localhost";
  private static final String jerryKeytab = "/tmp/jerry.keytab";
  private static final String serverPrincipal = "server/localhost";


  @Test
  public void testWithoutKrb() throws Exception {
    UserGroupInformation.reset();
    Configuration cfg = new Configuration();
    cfg.addResource(KerberosTest.class.getClassLoader().getResourceAsStream("core-site.xml"));
    try (FileSystem fs = FileSystem.newInstance(cfg)) {
      fail("should not success without kerberos login");
    } catch (IOException ignored) {
    }
    UserGroupInformation.reset();
  }

  @Test
  public void test() throws Exception {
    UserGroupInformation.reset();
    Configuration cfg = new Configuration();
    cfg.set("hadoop.security.authentication", "kerberos");
    cfg.set("juicefs.server-principal", serverPrincipal);
    cfg.set("juicefs.mountpoint", "/jfs"); // to new another jfs
    UserGroupInformation.setConfiguration(cfg);
    UserGroupInformation.loginUserFromKeytab(clientPrincipal, clientKeytab);
    cfg.addResource(KerberosTest.class.getClassLoader().getResourceAsStream("core-site.xml"));
    FileSystem fs = FileSystem.newInstance(cfg);
    fs.listStatus(new Path("/"));
    UserGroupInformation.reset();
    fs.close();
  }

  @Test
  public void testToken() throws Exception {
    UserGroupInformation.reset();
    Configuration cfg = new Configuration();
    cfg.set("hadoop.security.authentication", "kerberos");
    cfg.set("juicefs.server-principal", serverPrincipal);
    cfg.set("juicefs.mountpoint", "/jfs"); // to new another jfs
    UserGroupInformation.setConfiguration(cfg);
    UserGroupInformation.loginUserFromKeytab(clientPrincipal, clientKeytab);
    cfg.addResource(KerberosTest.class.getClassLoader().getResourceAsStream("core-site.xml"));
    FileSystem fs = FileSystem.newInstance(cfg);
    long start = System.currentTimeMillis();
    Token<?> t = fs.getDelegationToken(UserGroupInformation.getCurrentUser().getShortUserName());
    long end = System.currentTimeMillis();
    System.out.println("get token time: " + (end - start) + " ms");
    JuiceFSTokenRenewer renewer = new JuiceFSTokenRenewer();
    start = System.currentTimeMillis();
    System.out.println(renewer.renew(t, cfg));
    AbstractDelegationTokenIdentifier identifier = (AbstractDelegationTokenIdentifier) t.decodeIdentifier();
    System.out.println("token id: " + identifier.getMasterKeyId());
    end = System.currentTimeMillis();
    System.out.println("renew token time: " + (end - start) + " ms");
    fs.close();
    UserGroupInformation.reset();
  }

  @Test
  public void testProxyUser() throws Exception {
    UserGroupInformation.reset();
    Configuration cfg = new Configuration();
    cfg.set("hadoop.security.authentication", "kerberos");
    cfg.set("juicefs.server-principal", serverPrincipal);
    UserGroupInformation.setConfiguration(cfg);
    UserGroupInformation.loginUserFromKeytab(clientPrincipal, clientKeytab);
    cfg.addResource(KerberosTest.class.getClassLoader().getResourceAsStream("core-site.xml"));
    UserGroupInformation realUser = UserGroupInformation.getCurrentUser();
    UserGroupInformation foo = UserGroupInformation.createProxyUser("foo", realUser);
    foo.doAs(new PrivilegedExceptionAction<Object>() {
      @Override
      public Object run() throws Exception {
        cfg.set("juicefs.mountpoint", "/jfs1"); // to new another jfs
        FileSystem fs = FileSystem.newInstance(cfg);
        fs.close();
        return null;
      }
    });

    UserGroupInformation bar = UserGroupInformation.createProxyUser("bar", realUser);
    bar.doAs(new PrivilegedExceptionAction<Object>() {
      @Override
      public Object run() throws Exception {
        try {
          cfg.set("juicefs.mountpoint", "/jfs2"); // to new another jfs
          FileSystem fs = FileSystem.newInstance(cfg);
          fail("user bar should not proxyed");
        } catch (Exception ignored){
        }
        return null;
      }
    });
  }

  @Test
  public void testSuperUser() throws Exception {
    UserGroupInformation.reset();
    Configuration cfg = new Configuration();
    cfg.set("hadoop.security.authentication", "kerberos");
    cfg.set("juicefs.server-principal", serverPrincipal);
    cfg.set("juicefs.mountpoint", "/jfs3"); // to new another jfs
    cfg.addResource(KerberosTest.class.getClassLoader().getResourceAsStream("core-site.xml"));

    UserGroupInformation.setConfiguration(cfg);
    UserGroupInformation.loginUserFromKeytab(clientPrincipal, clientKeytab);
    FileSystem fs = FileSystem.newInstance(cfg);
    Path dir = new Path("/testsuperuser");
    fs.delete(dir);
    fs.mkdirs(dir);
    fs.setOwner(dir, "foo", "foo"); // only superuser has permission
  }

  @Test
  public void testMapRule() throws Exception {
    UserGroupInformation.reset();
    Configuration cfg = new Configuration();
    cfg.set("hadoop.security.authentication", "kerberos");
    cfg.set("juicefs.server-principal", serverPrincipal);
    cfg.set("juicefs.mountpoint", "/jfs4"); // to new another jfs
    cfg.addResource(KerberosTest.class.getClassLoader().getResourceAsStream("core-site.xml"));
    cfg.set("hadoop.security.auth_to_local", "RULE:[2:$1/$2@$0](jerry/.*@EXAMPLE\\.COM)s/.*/jerry_map/\nDEFAULT");

    UserGroupInformation.setConfiguration(cfg);
    UserGroupInformation.loginUserFromKeytab(jerryPrincipal, jerryKeytab);
    FileSystem fs = FileSystem.newInstance(cfg);
    Path dir = new Path("/testAuthToLocal");
    fs.delete(dir);
    fs.mkdirs(dir);
    FileStatus[] statuses = fs.listStatus(new Path("/"));
    assertEquals("jerry_map", fs.getFileStatus(dir).getOwner());
    fs.close();
  }

  @Test
  public void testMapRuleWithProxyUser() throws Exception {
    // test for proxy user
    UserGroupInformation.reset();
    Configuration cfg = new Configuration();
    cfg.set("hadoop.security.authentication", "kerberos");
    cfg.set("juicefs.server-principal", serverPrincipal);
    cfg.set("juicefs.mountpoint", "/jfs5"); // to new another jfs
    cfg.addResource(KerberosTest.class.getClassLoader().getResourceAsStream("core-site.xml"));
    // map tom to client
    cfg.set("hadoop.security.auth_to_local", "RULE:[2:$1/$2@$0](tom/.*@EXAMPLE\\.COM)s/.*/client/\nDEFAULT");
    UserGroupInformation.setConfiguration(cfg);
    UserGroupInformation.loginUserFromKeytab(tomPrincipal, tomKeytab);
    UserGroupInformation foo = UserGroupInformation.createProxyUser("foo", UserGroupInformation.getCurrentUser());
    foo.doAs((PrivilegedExceptionAction<Object>) () -> {
      FileSystem fs = FileSystem.newInstance(cfg);
      Path dir = new Path("/testAuthToLocalWithProxyUser");
      fs.delete(dir);
      fs.mkdirs(dir);
      FileStatus[] statuses = fs.listStatus(new Path("/"));
      for (FileStatus status : statuses) {
        System.out.println(status.getPath().toString() + " " + status.getOwner() + " " + status.getGroup());
      }
      assertEquals("foo", fs.getFileStatus(dir).getOwner());
      fs.close();
      return null;
    });

    // test for proxy user
    UserGroupInformation.reset();
    Configuration cfg2 = new Configuration();
    cfg2.set("hadoop.security.authentication", "kerberos");
    cfg2.set("juicefs.server-principal", serverPrincipal);
    cfg2.set("juicefs.mountpoint", "/jfs6"); // to new another jfs
    cfg2.addResource(KerberosTest.class.getClassLoader().getResourceAsStream("core-site.xml"));
    // map tom to client
    cfg2.set("hadoop.security.auth_to_local", "RULE:[2:$1/$2@$0](tom/.*@EXAMPLE\\.COM)s/.*/client/\nDEFAULT");
    UserGroupInformation.setConfiguration(cfg2);
    UserGroupInformation.loginUserFromKeytab(tomPrincipal, tomKeytab);
    UserGroupInformation bar = UserGroupInformation.createProxyUser("bar", UserGroupInformation.getCurrentUser());
    bar.doAs((PrivilegedExceptionAction<Object>) () -> {
      try {
        FileSystem fs = FileSystem.newInstance(cfg2);
        fs.close();
        fail("user client should not proxy bar");
      } catch (Exception ignored) {
      }
      return null;
    });
  }
}
