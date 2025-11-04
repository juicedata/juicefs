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

import io.juicefs.JuiceFileSystem;
import io.juicefs.JuiceFileSystemImpl;
import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.FileSystem;
import org.apache.hadoop.fs.FilterFileSystem;
import org.apache.hadoop.io.Text;
import org.apache.hadoop.security.token.Token;
import org.apache.hadoop.security.token.TokenRenewer;

import java.io.IOException;
import java.net.URI;

public class JuiceFSTokenRenewer extends TokenRenewer {

  @Override
  public boolean handleKind(Text kind) {
    return JuiceFSDelegationTokenIdentifier.TOKEN_KIND.equals(kind);
  }

  @Override
  public boolean isManaged(Token<?> token) throws IOException {
    return true;
  }

  @Override
  public long renew(Token<?> token, Configuration configuration) throws IOException, InterruptedException {
    String service = token.getService().toString();
    FileSystem fs = FileSystem.get(URI.create(service), configuration);
    if (fs instanceof JuiceFileSystem) {
      return ((JuiceFileSystemImpl) ((FilterFileSystem) fs).getRawFileSystem()).renewToken(token);
    }
    throw new IOException("renew token failed");
  }

  @Override
  public void cancel(Token<?> token, Configuration configuration) throws IOException, InterruptedException {
    String service = token.getService().toString();
    FileSystem fs = FileSystem.get(URI.create(service), configuration);
    if (fs instanceof JuiceFileSystem) {
      ((JuiceFileSystemImpl) ((FilterFileSystem) fs).getRawFileSystem()).cancelToken(token);
      return;
    }
    throw new IOException("cancel token failed");
  }
}
