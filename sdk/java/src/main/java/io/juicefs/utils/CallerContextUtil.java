/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
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
