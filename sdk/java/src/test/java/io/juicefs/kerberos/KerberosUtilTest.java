/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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

import org.apache.hadoop.security.SaslRpcServer;
import org.apache.hadoop.security.UserGroupInformation;
import org.junit.After;
import org.junit.Before;
import org.junit.Test;

import javax.security.auth.Subject;
import javax.security.auth.kerberos.KerberosPrincipal;
import javax.security.auth.kerberos.KerberosTicket;
import java.security.AccessController;
import java.security.PrivilegedAction;
import java.util.Date;

import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;
import static org.junit.Assert.fail;

/**
 * Checks which UGI {@link KerberosUtil#genApReq} authenticates as. It does not need a KDC: the
 * ticket below is a placeholder, so building the AP-REQ is expected to fail once the TGT has been
 * found. What matters is whether the TGT held by the caller's UGI is used instead of the one of
 * the static login user.
 */
public class KerberosUtilTest {
  private static final String NO_TGT = "Failed to find any Kerberos tgt";
  private static final String SPN = "server/localhost@EXAMPLE.COM";

  @Before
  public void setUp() {
    UserGroupInformation.reset();
    // the static login user, as overwritten by another component: it holds no Kerberos ticket
    UserGroupInformation.setLoginUser(UserGroupInformation.createRemoteUser("login"));
  }

  @After
  public void tearDown() {
    UserGroupInformation.reset();
  }

  private static UserGroupInformation kerberosUser(String name, boolean withTgt) throws Exception {
    UserGroupInformation ugi = UserGroupInformation.createRemoteUser(name, SaslRpcServer.AuthMethod.KERBEROS);
    if (withTgt) {
      Subject subject = ugi.doAs((PrivilegedAction<Subject>) () -> Subject.getSubject(AccessController.getContext()));
      long now = System.currentTimeMillis();
      subject.getPrivateCredentials().add(new KerberosTicket(new byte[]{1},
          new KerberosPrincipal(name + "@EXAMPLE.COM"),
          new KerberosPrincipal("krbtgt/EXAMPLE.COM@EXAMPLE.COM", KerberosPrincipal.KRB_NT_SRV_INST),
          new byte[32], 18, new boolean[9], new Date(now), new Date(now), new Date(now + 3600_000L), null, null));
    }
    return ugi;
  }

  private static String apReqFailure(UserGroupInformation ugi) throws Exception {
    try {
      ugi.doAs((java.security.PrivilegedExceptionAction<byte[]>) () -> KerberosUtil.genApReq(SPN));
    } catch (Exception e) {
      StringBuilder sb = new StringBuilder();
      for (Throwable t = e; t != null; t = t.getCause()) {
        sb.append(t.getMessage()).append('\n');
      }
      return sb.toString();
    }
    fail("a placeholder ticket must not produce an AP-REQ");
    return null;
  }

  @Test
  public void testUserWithoutTgtFails() throws Exception {
    assertTrue(apReqFailure(kerberosUser("alice", false)).contains(NO_TGT));
  }

  @Test
  public void testCurrentUserTgtIsUsedWhenLoginUserHasNone() throws Exception {
    assertFalse(apReqFailure(kerberosUser("alice", true)).contains(NO_TGT));
  }

  @Test
  public void testProxyUserUsesRealUserTgt() throws Exception {
    UserGroupInformation proxy = UserGroupInformation.createProxyUser("bob", kerberosUser("alice", true));
    assertFalse(apReqFailure(proxy).contains(NO_TGT));
  }
}
