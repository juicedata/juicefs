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

package com.juicefs.utils;

import org.apache.commons.logging.Log;
import org.apache.commons.logging.LogFactory;
import org.json.JSONArray;
import org.json.JSONObject;

import java.util.HashSet;
import java.util.Set;

// https://tommy%40juicedata.io:Hadoop%40jfs666@dbc-469b2be9-9f7f.cloud.databricks.com/api/2.0/clusters/get?cluster_name=jfs
public class DatabricksNodesFetcher extends NodesFetcher {
    private static final Log LOG = LogFactory.getLog(DatabricksNodesFetcher.class);

    public DatabricksNodesFetcher(String jfsName) {
        super(jfsName);
    }

    @Override
    public Set<String> getNodes(String[] urls) {
        if (urls == null) {
            return null;
        }

        for (String url : urls) {
            try {
                String word = "get?cluster_name=";
                int start_index = url.indexOf(word);
                String cluster_name = url.substring(start_index + word.length()).replace("/", "");
                String listUrl = url.substring(0, start_index) + "list";
                String clusterId = queryClusterId(listUrl, cluster_name);
                String getUrl = url.substring(0, start_index) + "get?cluster_id=" + clusterId;
                String response = doGet(getUrl);
                if (response == null) {
                    continue;
                }
                return parseNodes(response);
            } catch (Throwable e) {
                LOG.warn("fetch from:" + url + " failed, switch to another url", e);
            }
        }

        return null;
    }

    private String queryClusterId(String url, String clusterName) {
        String response = doGet(url);
        if(response != null) {
            JSONArray clusters = new JSONObject(response).getJSONArray("clusters");
            for (Object cluster : clusters) {
                if (clusterName.equals(((JSONObject) cluster).getString("cluster_name")))  {
                    return ((JSONObject) cluster).getString("cluster_id");
                }
            }
        }
        return null;
    }

    @Override
    protected Set<String> parseNodes(String response) throws Exception {
        Set<String> result = new HashSet<>();
        JSONArray nodes = new JSONObject(response).getJSONArray("executors");
        for (Object node : nodes) {
            String host = ((JSONObject) node).getString("private_ip");
            result.add(host);
        }
        return result;
    }
}
