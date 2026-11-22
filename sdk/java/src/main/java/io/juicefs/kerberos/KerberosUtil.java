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

import org.apache.hadoop.security.UserGroupInformation;
import org.ietf.jgss.GSSContext;
import org.ietf.jgss.GSSManager;
import org.ietf.jgss.GSSName;

import java.io.IOException;
import java.security.PrivilegedExceptionAction;

public class KerberosUtil {
  public static byte[] genApReq(String spn) throws IOException, InterruptedException {
    // Like Hadoop's RPC client, authenticate as the caller (current user, or the real user behind a
    // proxy user) rather than the JVM-wide login user, which other components may have replaced.
    UserGroupInformation ugi = UserGroupInformation.getCurrentUser();
    if (ugi.getAuthenticationMethod() == UserGroupInformation.AuthenticationMethod.PROXY && ugi.getRealUser() != null) {
      ugi = ugi.getRealUser();
    }
    if (ugi.isFromKeytab()) {
      ugi.checkTGTAndReloginFromKeytab();
    } else if (ugi.hasKerberosCredentials()) {
      ugi.reloginFromTicketCache();
    }
    return ugi.doAs((PrivilegedExceptionAction<byte[]>) () -> {
      GSSManager manager = GSSManager.getInstance();
      GSSName serverName = manager.createName(spn, GSSName.NT_USER_NAME, org.apache.hadoop.security.authentication.util.KerberosUtil.GSS_KRB5_MECH_OID);
      GSSContext context = manager.createContext(serverName, org.apache.hadoop.security.authentication.util.KerberosUtil.GSS_KRB5_MECH_OID, null, GSSContext.DEFAULT_LIFETIME);
      byte[] token = new byte[0];
      return context.initSecContext(token, 0, token.length);
    });
  }
}
