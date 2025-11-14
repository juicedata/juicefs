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
import org.ietf.jgss.GSSException;
import org.ietf.jgss.GSSManager;
import org.ietf.jgss.GSSName;
import sun.security.jgss.GSSHeader;
import sun.security.util.DerValue;

import java.io.ByteArrayInputStream;
import java.io.IOException;
import java.security.PrivilegedExceptionAction;

public class KerberosUtil {
  public static byte[] genApReq(String spn) throws IOException, InterruptedException {
    UserGroupInformation loginUser = UserGroupInformation.getLoginUser();
    if (UserGroupInformation.isLoginKeytabBased()) {
      loginUser.checkTGTAndReloginFromKeytab();
    } else if (UserGroupInformation.isLoginTicketBased()) {
      loginUser.reloginFromTicketCache();
    }
    return loginUser.doAs((PrivilegedExceptionAction<byte[]>) () -> {
      GSSManager manager = GSSManager.getInstance();

      GSSName serverName = manager.createName(spn, GSSName.NT_USER_NAME, org.apache.hadoop.security.authentication.util.KerberosUtil.GSS_KRB5_MECH_OID);
      GSSContext context = manager.createContext(serverName, org.apache.hadoop.security.authentication.util.KerberosUtil.GSS_KRB5_MECH_OID, null, GSSContext.DEFAULT_LIFETIME);

      byte[] token = new byte[0];
      token = context.initSecContext(token, 0, token.length);

      ByteArrayInputStream in = new ByteArrayInputStream(token, 0, token.length);
      new GSSHeader(in);
      int tokenId = ((in.read() << 8) | in.read());
      if (tokenId != 0x0100)
        throw new GSSException(GSSException.DEFECTIVE_TOKEN, -1,
            "AP_REQ token id does not match!");
      return new DerValue(in).toByteArray();
    });
  }
}
