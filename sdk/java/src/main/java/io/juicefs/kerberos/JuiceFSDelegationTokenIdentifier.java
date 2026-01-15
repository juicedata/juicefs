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

import org.apache.hadoop.io.Text;
import org.apache.hadoop.security.token.delegation.AbstractDelegationTokenIdentifier;

public class JuiceFSDelegationTokenIdentifier extends AbstractDelegationTokenIdentifier {
  public static final Text TOKEN_KIND = new Text("JUICEFS_DELEGATION_TOKEN");

  public JuiceFSDelegationTokenIdentifier() {
  }

  public JuiceFSDelegationTokenIdentifier(String owner, String renewer, String realUser) {
    super(new Text(owner), new Text(renewer), realUser == null ? null : new Text(realUser));
  }

  @Override
  public Text getKind() {
    return TOKEN_KIND;
  }

  @Override
  public String toString() {
    return "token for " + getUser().getShortUserName() +
        ": " + super.toString();
  }
}
