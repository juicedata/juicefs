/**
 * Licensed to the Apache Software Foundation (ASF) under one
 * or more contributor license agreements.  See the NOTICE file
 * distributed with this work for additional information
 * regarding copyright ownership.  The ASF licenses this file
 * to you under the Apache License, Version 2.0 (the
 * "License"); you may not use this file except in compliance
 * with the License.  You may obtain a copy of the License at
 * <p>
 * http://www.apache.org/licenses/LICENSE-2.0
 * <p>
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */
package io.juicefs.utils;

import org.apache.hadoop.classification.InterfaceAudience;
import org.apache.hadoop.fs.permission.FsPermission;

/**
 * HDFS permission subclass used to indicate an ACL is present and/or that the
 * underlying file/dir is encrypted. The ACL/encrypted bits are not visible
 * directly to users of {@link FsPermission} serialization.  This is
 * done for backwards compatibility in case any existing clients assume the
 * value of FsPermission is in a particular range.
 */
@InterfaceAudience.Private
public class FsPermissionExtension extends FsPermission {
  private final static short ACL_BIT = 1 << 12;
  private final static short ENCRYPTED_BIT = 1 << 13;
  private final boolean aclBit;
  private final boolean encryptedBit;

  /**
   * Constructs a new FsPermissionExtension based on the given FsPermission.
   *
   * @param perm FsPermission containing permission bits
   */
  public FsPermissionExtension(FsPermission perm, boolean hasAcl,
                               boolean isEncrypted) {
    super(perm.toShort());
    aclBit = hasAcl;
    encryptedBit = isEncrypted;
  }

  @Override
  public short toExtendedShort() {
    return (short) (toShort() |
            (aclBit ? ACL_BIT : 0) | (encryptedBit ? ENCRYPTED_BIT : 0));
  }

  public boolean getAclBit() {
    return aclBit;
  }

  @Override
  public boolean getEncryptedBit() {
    return encryptedBit;
  }
}