/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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

package io.juicefs.utils;

import org.apache.hadoop.ipc.CallerContext;


public class CallerContextUtil {

  public static void setContext(String context) throws Exception {
    CallerContext current = CallerContext.getCurrent();
    CallerContext.Builder builder;
    if (current == null || !current.isContextValid()) {
      builder = new CallerContext.Builder(context);
      CallerContext.setCurrent(builder.build());
    } else if (current.getSignature() == null && !current.getContext().endsWith("_" + context)) {
      builder = new CallerContext.Builder(current.getContext() + "_" + context);
      CallerContext.setCurrent(builder.build());
    }
  }
}
